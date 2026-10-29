// Core error classification — PATTERN: Non-Retryable Errors
// (https://docs.temporal.io/design-patterns/non-retryable-errors).
//
//	Core response                                          Kind        Activity returns              Effect
//	2xx                                                  -           result                        -
//	503 CORE_UNAVAILABLE, 504 SQL_TIMEOUT, 500, 429,       Transient   CoreTransientError (retryable)  Temporal retries with
//	  network error, timeout                                                                      backoff
//	422 ACCOUNT_*, 409 GENTX_BATCH_CLOSED/INVALID_TRANSITION, Structural  CoreStructuralError (non-ret.)  workflow parks the chunk
//	  404 GENTX_BATCH_NOT_FOUND
//	400 (and other 4xx)                                  Contract    CoreContractError (non-ret.)    parks (a defect needs a human)
//
// Business outcomes (policy DENY, blocked beneficiary, already present) are 200 responses and never reach
// this file: they are results (PATTERN: Business Outcome as Result).
package coreclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"go.temporal.io/sdk/temporal"

	"bulkwire/internal/model"
)

// Kind is the classification of a Core failure.
type Kind int

const (
	Transient  Kind = iota // retry
	Structural             // park: retrying cannot fix it
	Contract               // park: a request defect
)

func (k Kind) String() string {
	return [...]string{"transient", "structural", "contract"}[k]
}

// Error is a classified Core failure.
type Error struct {
	Kind       Kind
	HTTPStatus int    // 0 for network errors
	Code       string // Core's code, e.g. ACCOUNT_ON_HOLD
	Message    string // Core's human message (account numbers already masked by Core)
	Err        error
}

func (e *Error) Error() string {
	if e.HTTPStatus == 0 {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s (HTTP %d)", e.Code, e.Message, e.HTTPStatus)
}

func (e *Error) Unwrap() error { return e.Err }

// IsTransient reports whether err is a retryable Core failure.
func IsTransient(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == Transient
}

func classify(status int, body []byte) *Error {
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &payload)
	if payload.Code == "" {
		payload.Code = fmt.Sprintf("HTTP_%d", status)
	}
	if payload.Message == "" {
		payload.Message = http.StatusText(status)
	}
	// Core's message becomes a Temporal failure message, which is stored unencrypted: keep it free of PII.
	payload.Message = model.RedactAccountNumbers(payload.Message)
	e := &Error{HTTPStatus: status, Code: payload.Code, Message: payload.Message}
	switch {
	case status >= 500, status == http.StatusTooManyRequests, status == http.StatusRequestTimeout:
		e.Kind = Transient
	case status == http.StatusUnprocessableEntity, status == http.StatusConflict, status == http.StatusNotFound:
		e.Kind = Structural
	default:
		e.Kind = Contract
	}
	return e
}

// AsActivityError converts a Core failure into the Temporal ApplicationError an Activity returns. Structural
// and contract failures are NON-retryable and carry model.CoreErrorDetails, so the workflow can show the code
// and message of a parked chunk without parsing strings. Transient failures are retryable: the Activity's
// RetryPolicy (no attempt cap, bounded by ScheduleToClose) retries them in place.
func AsActivityError(err error) error {
	var e *Error
	if !errors.As(err, &e) {
		return err
	}
	details := model.CoreErrorDetails{Code: e.Code, Message: e.Message, HTTPStatus: e.HTTPStatus}
	switch e.Kind {
	case Structural:
		return temporal.NewNonRetryableApplicationError(e.Error(), model.ErrTypeCoreStructural, nil, details)
	case Contract:
		return temporal.NewNonRetryableApplicationError(e.Error(), model.ErrTypeCoreContract, nil, details)
	default:
		return temporal.NewApplicationError(e.Error(), model.ErrTypeCoreTransient, details)
	}
}
