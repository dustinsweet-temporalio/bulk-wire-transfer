// DEMO-ONLY: encryption demo. Shows the keyring (ids and dates, never key material), rotates keys, and inspects
// a batch's stored payloads: what the Temporal Service actually holds (ciphertext, fetched with a client that has
// NO codec) next to the same payloads decoded with the keyring.
// PRODUCTION NOTE: keys come from a KMS/HSM; rotation adds a key, makes it active, and keeps old keys for decoding.
package control

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/converter"

	"bulkwire/internal/codec"
)

func (s *Service) readKeyring() (codec.Keyring, error) {
	data, err := os.ReadFile(s.Cfg.KeyringPath)
	if err != nil {
		return codec.Keyring{}, err
	}
	return codec.ParseKeyring(data)
}

func (s *Service) keys(w http.ResponseWriter, _ *http.Request) {
	k, err := s.readKeyring()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type keyInfo struct {
		ID        string    `json:"id"`
		CreatedAt time.Time `json:"createdAt"`
		Active    bool      `json:"active"`
	}
	out := []keyInfo{}
	for _, key := range k.Keys {
		out = append(out, keyInfo{ID: key.ID, CreatedAt: key.CreatedAt, Active: key.ID == k.ActiveKeyID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"activeKeyId": k.ActiveKeyID, "keys": out})
}

// rotateKey adds a fresh random 256-bit key, makes it active, and writes the keyring atomically (temp file +
// rename). Every worker, bulkwire-service, and the Codec Server hot-reload it within seconds; old keys stay, so
// existing histories still decode.
func (s *Service) rotateKey(w http.ResponseWriter, _ *http.Request) {
	k, err := s.readKeyring()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now().UTC()
	id := "k" + now.Format("20060102-150405")
	k.Keys = append(k.Keys, codec.Key{ID: id, Key: base64.StdEncoding.EncodeToString(raw), CreatedAt: now})
	k.ActiveKeyID = id
	data, _ := json.MarshalIndent(k, "", "  ")
	tmp := filepath.Join(filepath.Dir(s.Cfg.KeyringPath), ".keyring.json.tmp")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.Rename(tmp, s.Cfg.KeyringPath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("DEMO-ONLY key rotated", "activeKeyId", id, "keys", len(k.Keys))
	writeJSON(w, http.StatusOK, map[string]any{"activeKeyId": id, "keys": len(k.Keys)})
}

type storedPayload struct {
	Event    string            `json:"event"`
	Metadata map[string]string `json:"metadata"`
	DataB64  string            `json:"dataBase64"` // first 120 base64 characters as stored
	Size     int               `json:"sizeBytes"`
	Decoded  any               `json:"decoded"`
	Error    string            `json:"error,omitempty"`
}

// inspect fetches the history with the RAW client (default converter, no codec): this is exactly what the
// Temporal Service stores.
func (s *Service) inspect(w http.ResponseWriter, r *http.Request) {
	wfID := r.URL.Query().Get("workflowId")
	if wfID == "" {
		writeError(w, http.StatusBadRequest, "workflowId is required")
		return
	}
	ctx, cancel := timeout(r.Context(), 15*time.Second)
	defer cancel()
	it := s.Raw.GetWorkflowHistory(ctx, wfID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var events []*historypb.HistoryEvent
	for it.HasNext() && len(events) < 400 {
		e, err := it.Next()
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		events = append(events, e)
	}
	out := []storedPayload{}
	for _, p := range pickPayloads(events) {
		out = append(out, s.describePayload(p.event, p.payload))
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflowId": wfID, "activeKeyId": s.Codec.ActiveKeyID(), "payloads": out})
}

type eventPayload struct {
	event   string
	payload *commonpb.Payload
}

// pickPayloads returns the workflow input and the first Activity input, as stored.
func pickPayloads(events []*historypb.HistoryEvent) []eventPayload {
	var out []eventPayload
	for _, e := range events {
		if a := e.GetWorkflowExecutionStartedEventAttributes(); a != nil && len(a.GetInput().GetPayloads()) > 0 {
			out = append(out, eventPayload{"WorkflowExecutionStarted input", a.GetInput().GetPayloads()[0]})
			break
		}
	}
	for _, e := range events {
		if a := e.GetActivityTaskScheduledEventAttributes(); a != nil && len(a.GetInput().GetPayloads()) > 0 {
			out = append(out, eventPayload{fmt.Sprintf("ActivityTaskScheduled input (%s, %s)", a.GetActivityType().GetName(), a.GetActivityId()), a.GetInput().GetPayloads()[0]})
			break
		}
	}
	return out
}

func (s *Service) describePayload(event string, p *commonpb.Payload) storedPayload {
	sp := storedPayload{Event: event, Metadata: map[string]string{}, Size: len(p.GetData())}
	for k, v := range p.GetMetadata() {
		sp.Metadata[k] = string(v)
	}
	b64 := base64.StdEncoding.EncodeToString(p.GetData())
	sp.DataB64 = b64[:min(len(b64), 120)]
	dec, err := s.Codec.Decode([]*commonpb.Payload{p})
	if err != nil {
		sp.Error = err.Error()
		return sp
	}
	var v any
	if err := converter.GetDefaultDataConverter().FromPayload(dec[0], &v); err != nil {
		sp.Decoded = string(bytes.TrimSpace(dec[0].GetData()))
	} else {
		sp.Decoded = v
	}
	return sp
}
