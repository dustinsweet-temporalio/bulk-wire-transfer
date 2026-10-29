// Core-facing types shared by coreclient (which produces them), the Activities (which attach them to
// failures), and the workflow (which displays them while a chunk is parked).
package model

// CoreErrorDetails is attached as ApplicationError details to every non-retryable Core failure, so the
// workflow can show code and message without parsing error strings.
type CoreErrorDetails struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"httpStatus"`
}

// Rejection is one wire Core wrote as Rejected (a business outcome, not an error).
type Rejection struct {
	RowNumber      int    `json:"rowNumber"`
	ExternalWireID string `json:"externalWireId"`
	Code           string `json:"code"`
	Message        string `json:"message"`
}
