// Tests for the AES-256-GCM PayloadCodec: plaintext never survives encoding, old keys decode after rotation,
// tampering and a swapped key id (AAD) are detected, and the keyring hot reload keeps the last good keyring.

package codec

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"google.golang.org/protobuf/proto"
)

func newKey(t *testing.T, id string) Key {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return Key{ID: id, Key: base64.StdEncoding.EncodeToString(b), CreatedAt: time.Now()}
}

func plainPayload(t *testing.T, v any) *commonpb.Payload {
	t.Helper()
	p, err := converter.GetDefaultDataConverter().ToPayload(v)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRoundTripHidesPlaintext(t *testing.T) {
	c, err := New(Keyring{ActiveKeyID: "a", Keys: []Key{newKey(t, "a")}})
	if err != nil {
		t.Fatal(err)
	}
	in := plainPayload(t, map[string]string{"beneficiary": "Acme Co"})
	enc, err := c.Encode([]*commonpb.Payload{in})
	if err != nil {
		t.Fatal(err)
	}
	if string(enc[0].Metadata[converter.MetadataEncoding]) != EncodingEncrypted || string(enc[0].Metadata[MetadataKeyID]) != "a" {
		t.Fatalf("metadata: %v", enc[0].Metadata)
	}
	if bytes.Contains(enc[0].Data, []byte("Acme")) {
		t.Fatal("plaintext visible in ciphertext")
	}
	dec, err := c.Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(dec[0], in) {
		t.Fatalf("round trip mismatch: %v vs %v", dec[0], in)
	}
}

func TestDecodeWithNonActiveKeyAfterRotation(t *testing.T) {
	a, b := newKey(t, "a"), newKey(t, "b")
	c, _ := New(Keyring{ActiveKeyID: "a", Keys: []Key{a}})
	old, _ := c.Encode([]*commonpb.Payload{plainPayload(t, "old batch")})

	if err := c.Swap(Keyring{ActiveKeyID: "b", Keys: []Key{a, b}}); err != nil {
		t.Fatal(err)
	}
	fresh, _ := c.Encode([]*commonpb.Payload{plainPayload(t, "new batch")})
	if string(fresh[0].Metadata[MetadataKeyID]) != "b" {
		t.Fatal("new payloads must use the new active key")
	}
	for _, p := range [][]*commonpb.Payload{old, fresh} {
		if _, err := c.Decode(p); err != nil {
			t.Fatalf("decode after rotation: %v", err)
		}
	}
}

func TestPassThroughForUnencodedPayloads(t *testing.T) {
	c, _ := New(Keyring{ActiveKeyID: "a", Keys: []Key{newKey(t, "a")}})
	in := plainPayload(t, 42)
	out, err := c.Decode([]*commonpb.Payload{in})
	if err != nil || out[0] != in {
		t.Fatalf("pass-through failed: %v", err)
	}
}

func TestTamperDetection(t *testing.T) {
	c, _ := New(Keyring{ActiveKeyID: "a", Keys: []Key{newKey(t, "a")}})
	enc, _ := c.Encode([]*commonpb.Payload{plainPayload(t, "x")})
	enc[0].Data[len(enc[0].Data)-1] ^= 0x01
	if _, err := c.Decode(enc); err == nil {
		t.Fatal("tampered ciphertext decoded")
	}
}

func TestKeyIDIsBoundAsAAD(t *testing.T) {
	// Two ids for the SAME key bytes: relabeling a payload only fails because the id is authenticated data.
	k := newKey(t, "a")
	alias := k
	alias.ID = "a-alias"
	c, _ := New(Keyring{ActiveKeyID: "a", Keys: []Key{k, alias}})
	enc, _ := c.Encode([]*commonpb.Payload{plainPayload(t, "x")})
	enc[0].Metadata[MetadataKeyID] = []byte("a-alias")
	if _, err := c.Decode(enc); err == nil {
		t.Fatal("relabeled payload decoded")
	}
}

func TestInvalidKeyringsAreRejected(t *testing.T) {
	for name, k := range map[string]Keyring{
		"no keys":        {ActiveKeyID: "a"},
		"missing active": {ActiveKeyID: "z", Keys: []Key{newKey(t, "a")}},
		"short key":      {ActiveKeyID: "a", Keys: []Key{{ID: "a", Key: base64.StdEncoding.EncodeToString([]byte("short"))}}},
	} {
		if _, err := New(k); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestHotReloadKeepsLastGoodKeyring(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keyring.json")
	write := func(data []byte) {
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
	}
	a, b := newKey(t, "a"), newKey(t, "b")
	first, _ := json.Marshal(Keyring{ActiveKeyID: "a", Keys: []Key{a}})
	write(first)
	c, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Watch(ctx, path, 10*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))

	time.Sleep(20 * time.Millisecond) // distinct mtime
	write([]byte("{not json"))
	time.Sleep(60 * time.Millisecond)
	if c.ActiveKeyID() != "a" {
		t.Fatal("malformed keyring replaced a good one")
	}

	second, _ := json.Marshal(Keyring{ActiveKeyID: "b", Keys: []Key{a, b}})
	write(second)
	deadline := time.Now().Add(2 * time.Second)
	for c.ActiveKeyID() != "b" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if c.ActiveKeyID() != "b" {
		t.Fatal("rotation not picked up")
	}
}

func TestFailureDetailsAreEncrypted(t *testing.T) {
	c, _ := New(Keyring{ActiveKeyID: "a", Keys: []Key{newKey(t, "a")}})
	fc := NewFailureConverter(NewDataConverter(c))
	err := temporal.NewApplicationError("CORE_UNAVAILABLE", "CoreTransientError", map[string]string{"account": "4401220033"})
	f := fc.ErrorToFailure(err)
	p := f.GetApplicationFailureInfo().GetDetails().GetPayloads()[0]
	if string(p.Metadata[converter.MetadataEncoding]) != EncodingEncrypted || bytes.Contains(p.Data, []byte("4401220033")) {
		t.Fatalf("failure details must be encrypted, got encoding %q", p.Metadata[converter.MetadataEncoding])
	}
	var got map[string]string
	var appErr *temporal.ApplicationError
	if !errors.As(fc.FailureToError(f), &appErr) || appErr.Details(&got) != nil || got["account"] != "4401220033" {
		t.Fatalf("details did not round trip: %v", got)
	}
}
