// Application error TYPES that cross the Activity -> workflow boundary.
//
// PATTERN: Non-Retryable Errors — https://docs.temporal.io/design-patterns/non-retryable-errors
// An Activity marks a failure retryable or not; the RetryPolicy then retries the retryable ones with backoff
// and returns the non-retryable ones to the workflow immediately, where they park the chunk for a human.
// The workflow matches on these type names, so they are defined once here.
package model

const (
	// ErrTypeCoreTransient: Core 503/504/500/429, network error, timeout. Retryable: Temporal retries in place.
	ErrTypeCoreTransient = "CoreTransientError"
	// ErrTypeCoreStructural: Core 422 (account on hold/closed/unknown), 409 (batch closed, invalid transition),
	// 404 (batch not found). Non-retryable: retrying cannot fix it; the chunk parks.
	ErrTypeCoreStructural = "CoreStructuralError"
	// ErrTypeCoreContract: Core 400 (or an unknown FI): a defect in the request. Non-retryable: parks.
	ErrTypeCoreContract = "CoreContractError"
	// ErrTypeClaimCheckNotFound: staged data is gone (expired or never staged). Non-retryable: parks; an
	// operator typically cancels and the user re-uploads.
	ErrTypeClaimCheckNotFound = "ClaimCheckNotFound"
	// ErrTypeClaimCheckUnavailable: the claim-check store is temporarily unreachable. Retryable.
	ErrTypeClaimCheckUnavailable = "ClaimCheckUnavailable"
	// ErrTypeStagingInvalid: the manifest says INVALID (defense in depth; drafting refuses invalid batches).
	// Non-retryable: parks.
	ErrTypeStagingInvalid = "StagingInvalid"
	// ErrTypeWrongFiRouting: a Core Activity reached a worker that holds no Core client for its FI (the batch's
	// Core queue does not serve that FI: a routing misconfiguration). Non-retryable: retrying on the same queue
	// cannot fix it, so the chunk parks for an operator instead of failing the workflow.
	ErrTypeWrongFiRouting = "WrongFiRouting"
)

// NonRetryableErrorTypes are the types that park a chunk when an Activity returns them.
var NonRetryableErrorTypes = []string{ErrTypeCoreStructural, ErrTypeCoreContract, ErrTypeClaimCheckNotFound, ErrTypeStagingInvalid, ErrTypeWrongFiRouting}
