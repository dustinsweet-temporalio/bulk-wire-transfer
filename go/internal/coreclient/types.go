// Request/response DTOs mirroring core/CoreApi/Contracts (camelCase JSON, money in int64 cents).
package coreclient

import "bulkwire/internal/model"

type EnsureGenTxBatchRequest struct {
	ExternalBatchID string `json:"externalBatchId"`
	CustomerID      string `json:"customerId"`
	InitiatedBy     string `json:"initiatedBy"`
	RowCount        int    `json:"rowCount"`
	AmountCents     int64  `json:"amountCents"`
}

type EnsureGenTxBatchResponse struct {
	GenTxBatchID int64  `json:"genTxBatchId"`
	Created      bool   `json:"created"`
	Status       string `json:"status"`
}

type PolicyCheckRequest struct {
	GroupKey           string `json:"groupKey"`
	OriginatingAccount string `json:"originatingAccount"`
	ProcessDate        string `json:"processDate"`
	AmountCents        int64  `json:"amountCents"`
}

type PolicyCheckResponse struct {
	Decision       string `json:"decision"` // ALLOW | DENY
	Code           string `json:"code"`     // "" | DAILY_LIMIT_EXCEEDED
	LimitCents     int64  `json:"limitCents"`
	UsedCents      int64  `json:"usedCents"`
	RequestedCents int64  `json:"requestedCents"`
}

type ForceReject struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type DraftChunkRequest struct {
	ChunkID     string          `json:"chunkId"`
	Actor       string          `json:"actor"`
	ForceReject *ForceReject    `json:"forceReject"`
	Wires       []model.WireRow `json:"wires"`
}

type RejectedWire struct {
	RowNumber      int    `json:"rowNumber"`
	ExternalWireID string `json:"externalWireId"`
	Code           string `json:"code"`
	Message        string `json:"message"`
}

type DraftChunkResponse struct {
	ChunkDrafted     int            `json:"chunkDrafted"`
	ChunkRejected    int            `json:"chunkRejected"`
	InsertedThisCall int            `json:"insertedThisCall"`
	AlreadyPresent   int            `json:"alreadyPresent"`
	Rejected         []RejectedWire `json:"rejected"`
}

type AuthorizeChunkRequest struct {
	ApprovalRef string `json:"approvalRef"`
	Actor       string `json:"actor"`
}

type RejectChunkRequest struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Actor   string `json:"actor"`
}

type ChunkTotals struct {
	ChunkAuthorized int `json:"chunkAuthorized"`
	ChunkRejected   int `json:"chunkRejected"`
	FlippedThisCall int `json:"flippedThisCall"`
}

type SetStatusRequest struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	Actor  string `json:"actor"`
}

type StatusCounts struct {
	Drafted    int `json:"drafted"`
	Authorized int `json:"authorized"`
	Rejected   int `json:"rejected"`
	Expired    int `json:"expired"`
	Denied     int `json:"denied"`
	Cancelled  int `json:"cancelled"`
	Total      int `json:"total"`
}

type GenTxBatchSummary struct {
	GenTxBatchID    int64        `json:"genTxBatchId"`
	ExternalBatchID string       `json:"externalBatchId"`
	Status          string       `json:"status"`
	StatusReason    string       `json:"statusReason"`
	Counts          StatusCounts `json:"counts"`
}

type ValidateAccountsRequest struct {
	CustomerID string   `json:"customerId"`
	Accounts   []string `json:"accounts"`
}

type ValidateAccountsResponse struct {
	Results []struct {
		Account         string `json:"account"`
		Exists          bool   `json:"exists"`
		OwnedByCustomer bool   `json:"ownedByCustomer"`
		Status          string `json:"status"`
	} `json:"results"`
}
