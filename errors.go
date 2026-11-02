package invoist

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrMissingAPIKey is returned by New when the API key is empty.
var ErrMissingAPIKey = errors.New("invoist: an API key is required (create one in the Invoist dashboard and pass it to invoist.New)")

// Error codes the library itself sets on an *InvoistError, alongside the
// codes the API sends in its error envelope.
const (
	// CodeUnexpectedResponse means the response was not the API's
	// {"error":{"code","message"}} envelope: a proxy's HTML page, an empty
	// body, or JSON this library could not read.
	CodeUnexpectedResponse = "unexpected_response"
	// CodeNetworkError means no response arrived (Status is 0).
	CodeNetworkError = "network_error"
	// CodeTimeout means the request took longer than the client's timeout
	// (Status is 0).
	CodeTimeout = "timeout"
	// CodeExtractionTimeout means WaitForExtraction gave up before the
	// extraction finished (Status is 0).
	CodeExtractionTimeout = "extraction_timeout"
	// CodeInvalidSignature means a webhook's signature header was missing,
	// malformed, or did not match (Status is 400).
	CodeInvalidSignature = "invalid_signature"
	// CodeSignatureTooOld means a webhook's timestamp is outside the
	// allowed tolerance (Status is 400).
	CodeSignatureTooOld = "signature_too_old"
	// CodeInvalidPayload means a correctly signed webhook body is not a
	// JSON event (Status is 400).
	CodeInvalidPayload = "invalid_payload"
)

// InvoistError is the error every failed call returns, whether the API
// answered with an error, no answer arrived, or a webhook did not verify.
// Use errors.As (or errors.AsType) to reach it:
//
//	var apiErr *invoist.InvoistError
//	if errors.As(err, &apiErr) && apiErr.IsNotFound() { ... }
//
// Branch on Code, which is stable, never on Message.
//
// A cancelled or expired context is not an InvoistError: the call returns
// the context's own error (context.Canceled or context.DeadlineExceeded).
type InvoistError struct {
	// Status is the HTTP status, or 0 when no response arrived.
	Status int
	// Code is a stable machine-readable code: the one in the API's error
	// envelope, or one of the Code constants in this package.
	Code string
	// Message is human-readable text. Its wording may change.
	Message string
	// RequestID is the X-Request-Id response header, worth quoting to
	// support. Empty when there was no response or no header.
	RequestID string
	// Body is the raw response body, when there was one.
	Body []byte
	// Err is the underlying cause when no response arrived (a dial error,
	// a timeout), and nil otherwise.
	Err error
}

// Error returns a one-line description of the failure.
func (e *InvoistError) Error() string {
	var b strings.Builder
	b.WriteString("invoist: ")
	b.WriteString(e.Message)
	b.WriteString(" (")
	if e.Status != 0 {
		fmt.Fprintf(&b, "status %d, ", e.Status)
	}
	b.WriteString("code ")
	b.WriteString(e.Code)
	if e.RequestID != "" {
		b.WriteString(", request ")
		b.WriteString(e.RequestID)
	}
	b.WriteString(")")
	return b.String()
}

// Unwrap returns the underlying cause, so errors.Is and errors.As can
// look through a network failure.
func (e *InvoistError) Unwrap() error { return e.Err }

// IsAuthError reports whether the key is missing, malformed or revoked (401).
func (e *InvoistError) IsAuthError() bool { return e.Status == 401 }

// IsPermissionError reports whether the key is valid but may not do this
// (403): a missing scope, or a business key on a household route.
func (e *InvoistError) IsPermissionError() bool { return e.Status == 403 }

// IsNotFound reports whether the resource does not exist (404).
func (e *InvoistError) IsNotFound() bool { return e.Status == 404 }

// IsRateLimited reports whether the request was throttled (429).
func (e *InvoistError) IsRateLimited() bool { return e.Status == 429 }

// IsRetryable reports whether sending the same request again could
// succeed: no response arrived, the request was throttled, or the server
// failed (5xx). The client already retries these, up to its retry limit.
func (e *InvoistError) IsRetryable() bool {
	return e.Status == 0 || e.Status == 429 || e.Status >= 500
}

// errorFromResponse reads the API's {"error":{"code","message"}} envelope,
// and falls back to unexpected_response when the body is anything else.
func errorFromResponse(status int, body []byte, requestID string) *InvoistError {
	e := &InvoistError{
		Status:    status,
		Code:      CodeUnexpectedResponse,
		Message:   fmt.Sprintf("Request failed with status %d", status),
		RequestID: requestID,
	}
	if len(body) > 0 {
		e.Body = body
	}
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return e
	}
	var detail struct {
		Code    *string `json:"code"`
		Message *string `json:"message"`
	}
	if len(envelope.Error) == 0 || envelope.Error[0] != '{' || json.Unmarshal(envelope.Error, &detail) != nil {
		return e
	}
	if detail.Code != nil && *detail.Code != "" {
		e.Code = *detail.Code
	}
	if detail.Message != nil && *detail.Message != "" {
		e.Message = *detail.Message
	}
	return e
}
