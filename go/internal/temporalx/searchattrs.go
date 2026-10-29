// Package temporalx holds the Temporal plumbing shared by every Go binary: namespace and type constants,
// the typed Search Attribute keys, client dialing (envconfig + encrypting DataConverter), and startup
// verification of the namespace's Search Attributes.
package temporalx

import (
	"fmt"

	"go.temporal.io/sdk/temporal"
)

const (
	// Namespace is shared by all FIs. WHY one namespace: Fairness and per-tenant rate limits operate within a
	// task queue, and a task queue lives in one namespace (see docs/ARCHITECTURE.md, "Tenancy", for the per-FI alternative).
	Namespace = "bulkwire"
	// WorkflowType is the one and only workflow type in this system.
	WorkflowType = "BulkWireBatch"
)

// WorkflowID is the batch's business identifier: "bulkwire/{fiId}/{batchId}". It contains no PII.
// PATTERN: Business Workflow ID — https://docs.temporal.io/workflow-execution/workflowid-runid
// Every entry point (Draft click, second tab, Core approval callback) computes the same ID, so they all reach
// the same execution.
func WorkflowID(fiID, batchID string) string {
	return fmt.Sprintf("bulkwire/%s/%s", fiID, batchID)
}

// PATTERN: Search Attributes for Operations — https://docs.temporal.io/search-attribute
// Indexed, queryable, and NOT encrypted by the payload codec, so none of them carries PII. They power lists
// (my batches, approvals queue, exception queue); live progress comes from the Query, not from these.
var (
	SABulkWireStatus = temporal.NewSearchAttributeKeyKeyword("BulkWireStatus") // phase, upserted on every change
	SAFiID           = temporal.NewSearchAttributeKeyKeyword("FiId")           // tenant, set at start
	SACustomerID     = temporal.NewSearchAttributeKeyKeyword("CustomerId")     // set at start
	SAInitiatedBy    = temporal.NewSearchAttributeKeyKeyword("InitiatedBy")    // uploader, set at start
	SANeedsAttention = temporal.NewSearchAttributeKeyBool("NeedsAttention")    // true while anything is parked
)

// SAWorkerDeploymentVersion is the built-in attribute recording which deployment version a run is pinned to.
const SAWorkerDeploymentVersion = "TemporalWorkerDeploymentVersion"

// RequiredSearchAttributes are the custom attributes this system needs, with their indexed types.
var RequiredSearchAttributes = map[string]string{
	"BulkWireStatus": "Keyword",
	"FiId":           "Keyword",
	"CustomerId":     "Keyword",
	"InitiatedBy":    "Keyword",
	"NeedsAttention": "Bool",
}
