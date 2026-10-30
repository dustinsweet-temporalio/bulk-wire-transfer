// Package coreclient is the typed HTTP client for each FI's core-api, and the place where Core responses are
// classified into transient (retry), structural (park), and contract (park) failures.
//
// PATTERN: Tenant Context in Input. Every call names its FI; the client maps it to THAT FI's Core base URL from
// CORE_ENDPOINTS (one Core per FI). The URL is deployment configuration and never enters workflow history, so
// running workflows are never pinned to a stale address. An outage at one FI's Core only slows that FI.
//
// The client sets no timeout of its own: every call uses its caller's context. Inside an Activity that is
// the Activity context, whose deadline is the StartToClose timeout (60 s), so a hung Core call is abandoned
// when Temporal would give up on the attempt anyway.
package coreclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"bulkwire/internal/staging"
)

// InternalTokenHeader authenticates service-to-service calls to Core.
const InternalTokenHeader = "X-Internal-Token"

// Client calls the right FI's core-api.
type Client struct {
	endpoints map[string]string
	tokens    map[string]string
	http      *http.Client
}

// New builds a client from fiId -> base URL and fiId -> internal token. Each call sends the token of the FI it
// calls, and each Core accepts only its own FI's token.
func New(endpoints, internalTokens map[string]string) *Client {
	return &Client{endpoints: endpoints, tokens: internalTokens, http: &http.Client{}}
}

// FiIDs lists the FIs this client knows.
func (c *Client) FiIDs() map[string]string { return c.endpoints }

// Serves reports whether this client holds an endpoint for the FI. A Core-queue worker's client holds only the
// FIs its queue serves (cmd/worker).
func (c *Client) Serves(fiID string) bool { _, ok := c.endpoints[fiID]; return ok }

// ---- Activity-facing calls ---------------------------------------------------------------------------------

// EnsureGenTxBatch creates (or finds) Core's batch header. Idempotent by ExternalBatchID.
func (c *Client) EnsureGenTxBatch(ctx context.Context, fiID string, req EnsureGenTxBatchRequest) (EnsureGenTxBatchResponse, error) {
	var out EnsureGenTxBatchResponse
	return out, c.Do(ctx, fiID, http.MethodPost, "/api/gentx-batches", req, &out)
}

// CheckGroupPolicy evaluates one group's daily limit. A DENY is a 200 result, not an error.
func (c *Client) CheckGroupPolicy(ctx context.Context, fiID string, genTxBatchID int64, req PolicyCheckRequest) (PolicyCheckResponse, error) {
	var out PolicyCheckResponse
	return out, c.Do(ctx, fiID, http.MethodPost, fmt.Sprintf("/api/gentx-batches/%d/policy-checks", genTxBatchID), req, &out)
}

// DraftChunk writes one chunk through usp_InsertDraftWires (TVP). Returns CHUNK-level totals.
func (c *Client) DraftChunk(ctx context.Context, fiID string, genTxBatchID int64, req DraftChunkRequest) (DraftChunkResponse, error) {
	var out DraftChunkResponse
	return out, c.Do(ctx, fiID, http.MethodPost, fmt.Sprintf("/api/gentx-batches/%d/draft-chunks", genTxBatchID), req, &out)
}

// AuthorizeChunk flips one chunk's Drafted wires to Authorized.
func (c *Client) AuthorizeChunk(ctx context.Context, fiID string, genTxBatchID int64, chunkID string, req AuthorizeChunkRequest) (ChunkTotals, error) {
	var out ChunkTotals
	return out, c.Do(ctx, fiID, http.MethodPost,
		fmt.Sprintf("/api/gentx-batches/%d/chunks/%s/authorize", genTxBatchID, url.PathEscape(chunkID)), req, &out)
}

// RejectChunk flips one chunk's Drafted wires to Rejected (operator decision in the authorize phase).
func (c *Client) RejectChunk(ctx context.Context, fiID string, genTxBatchID int64, chunkID string, req RejectChunkRequest) (ChunkTotals, error) {
	var out ChunkTotals
	return out, c.Do(ctx, fiID, http.MethodPost,
		fmt.Sprintf("/api/gentx-batches/%d/chunks/%s/reject", genTxBatchID, url.PathEscape(chunkID)), req, &out)
}

// SetGenTxBatchStatus moves Core's batch header (idempotent; same status is a no-op).
func (c *Client) SetGenTxBatchStatus(ctx context.Context, fiID string, genTxBatchID int64, req SetStatusRequest) (GenTxBatchSummary, error) {
	var out GenTxBatchSummary
	return out, c.Do(ctx, fiID, http.MethodPost, fmt.Sprintf("/api/gentx-batches/%d/status", genTxBatchID), req, &out)
}

// ---- Staging (bulkwire-service) ----------------------------------------------------------------------------

// ValidateAccounts implements staging.AccountChecker: ONE call per upload validates every distinct
// originating account (a TVP on the Core side).
func (c *Client) ValidateAccounts(ctx context.Context, fiID, customerID string, accounts []string) (map[string]staging.AccountStatus, error) {
	var out ValidateAccountsResponse
	if err := c.Do(ctx, fiID, http.MethodPost, "/api/accounts:validate", ValidateAccountsRequest{CustomerID: customerID, Accounts: accounts}, &out); err != nil {
		return nil, err
	}
	m := make(map[string]staging.AccountStatus, len(out.Results))
	for _, r := range out.Results {
		m[r.Account] = staging.AccountStatus{Exists: r.Exists, OwnedByCustomer: r.OwnedByCustomer, Status: r.Status}
	}
	return m, nil
}

var _ staging.AccountChecker = (*Client)(nil)

// Healthz returns nil when the FI's Core can reach its own database.
func (c *Client) Healthz(ctx context.Context, fiID string) error {
	return c.Do(ctx, fiID, http.MethodGet, "/healthz", nil, nil)
}

// Do performs one JSON call against an FI's Core and classifies any failure into *Error. It is exported for
// control-service's DEMO-ONLY admin calls.
func (c *Client) Do(ctx context.Context, fiID, method, path string, body, out any) error {
	base, ok := c.endpoints[fiID]
	if !ok {
		return &Error{Kind: Contract, Code: "CORE_UNKNOWN_FI", Message: fmt.Sprintf("no Core endpoint configured for FI %q", fiID)}
	}
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return &Error{Kind: Contract, Code: "CORE_CONTRACT", Message: err.Error()}
		}
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rdr)
	if err != nil {
		return &Error{Kind: Contract, Code: "CORE_CONTRACT", Message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(InternalTokenHeader, c.tokens[fiID])

	resp, err := c.http.Do(req)
	if err != nil {
		// Connection refused, reset, DNS failure, or the Activity deadline passing: all transient.
		return &Error{Kind: Transient, Code: "CORE_UNREACHABLE", Message: describeNetworkError(err), Err: err}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	if resp.StatusCode/100 == 2 {
		if out == nil || len(data) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return &Error{Kind: Contract, HTTPStatus: resp.StatusCode, Code: "CORE_CONTRACT", Message: "unreadable Core response: " + err.Error()}
		}
		return nil
	}
	return classify(resp.StatusCode, data)
}

func describeNetworkError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "Core did not answer before the attempt deadline"
	}
	if errors.Is(err, context.Canceled) {
		return "call canceled"
	}
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return "Core unreachable: " + uerr.Err.Error()
	}
	return "Core unreachable: " + err.Error()
}
