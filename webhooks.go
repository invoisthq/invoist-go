package invoist

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Webhook request headers.
const (
	// SignatureHeader carries the signature VerifyWebhook checks:
	// "t=<unix seconds>,v1=<hex HMAC-SHA256>".
	SignatureHeader = "X-Invoist-Signature"
	// EventHeader carries the event's type, such as "extraction.completed".
	EventHeader = "X-Invoist-Event"
	// DeliveryHeader carries an id for this delivery attempt, the same on
	// every retry of one delivery: use it to ignore repeats.
	DeliveryHeader = "X-Invoist-Delivery"
)

// DefaultWebhookTolerance is how far a webhook's timestamp may be from
// the current time, either way, before VerifyWebhook rejects it.
const DefaultWebhookTolerance = 5 * time.Minute

// EventType is the type of a webhook event.
type EventType string

// The event types the API sends.
const (
	// EventDocumentCreated: a document was stored.
	EventDocumentCreated EventType = "document.created"
	// EventExtractionCompleted: a document was read.
	EventExtractionCompleted EventType = "extraction.completed"
	// EventExtractionFailed: a document could not be read.
	EventExtractionFailed EventType = "extraction.failed"
	// EventReviewItemCreated: something was flagged for a person to check.
	EventReviewItemCreated EventType = "review_item.created"
	// EventReviewItemResolved: a review item was resolved or dismissed.
	EventReviewItemResolved EventType = "review_item.resolved"
	// EventDocumentRequestFulfilled: someone sent a document that was asked for.
	EventDocumentRequestFulfilled EventType = "document_request.fulfilled"
	// EventAll subscribes a webhook endpoint to every event type, in
	// CreateWebhookEndpointParams.Events. No event has this type.
	EventAll EventType = "*"
)

// WebhookEvent is a verified webhook delivery.
type WebhookEvent struct {
	// ID identifies the event.
	ID string `json:"id"`
	// Type is what happened, such as EventExtractionCompleted.
	Type EventType `json:"type"`
	// CreatedAt is when it happened.
	CreatedAt time.Time `json:"created_at"`
	// Data is the event's payload, as sent. Decode it with DecodeData.
	Data json.RawMessage `json:"data"`
}

// DecodeData decodes the event's Data into v, typically a struct with the
// fields you need or a map[string]any.
func (e *WebhookEvent) DecodeData(v any) error {
	if len(e.Data) == 0 {
		return fmt.Errorf("invoist: webhook event %s has no data", e.ID)
	}
	return json.Unmarshal(e.Data, v)
}

// WebhookOption configures VerifyWebhook.
type WebhookOption func(*webhookOptions)

type webhookOptions struct {
	tolerance time.Duration
	now       time.Time
}

// WithTolerance sets how far the signature's timestamp may be from now,
// in either direction (default DefaultWebhookTolerance). Zero turns the
// check off, which only makes sense when replaying a captured delivery.
func WithTolerance(d time.Duration) WebhookOption {
	return func(o *webhookOptions) { o.tolerance = d }
}

// WithNow sets the time the timestamp is compared against, instead of
// time.Now. For tests.
func WithNow(t time.Time) WebhookOption {
	return func(o *webhookOptions) { o.now = t }
}

// VerifyWebhook checks a delivery's X-Invoist-Signature header against
// your endpoint's signing secret, and returns the event.
//
// payload must be the raw request body, byte for byte: read it with
// io.ReadAll(r.Body) before anything parses it, because decoding and
// re-encoding JSON changes the bytes and breaks the signature.
//
// The signature is HMAC-SHA256 over "<t>.<payload>"; it passes when any
// v1 in the header matches (there are two while a secret is rotated).
// On failure it returns an *InvoistError with status 400 and code
// invalid_signature, or signature_too_old when the timestamp is more
// than the tolerance away from now. Answer such a request with 400.
func VerifyWebhook(payload []byte, signatureHeader, secret string, opts ...WebhookOption) (*WebhookEvent, error) {
	o := webhookOptions{tolerance: DefaultWebhookTolerance}
	for _, opt := range opts {
		opt(&o)
	}
	if o.now.IsZero() {
		o.now = time.Now()
	}

	timestamp, signatures, ok := parseSignatureHeader(signatureHeader)
	if !ok {
		return nil, webhookError(CodeInvalidSignature, "The "+SignatureHeader+" header is missing or malformed")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(payload)
	expected := []byte(hex.EncodeToString(mac.Sum(nil)))
	matched := false
	for _, sig := range signatures {
		// Check every candidate, so timing says nothing about which matched.
		if hmac.Equal(expected, []byte(sig)) {
			matched = true
		}
	}
	if !matched {
		return nil, webhookError(CodeInvalidSignature, "The webhook signature does not match")
	}

	if o.tolerance > 0 {
		seconds, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil {
			return nil, webhookError(CodeInvalidSignature, "The "+SignatureHeader+" timestamp is not a number")
		}
		if skew := o.now.Sub(time.Unix(seconds, 0)).Abs(); skew > o.tolerance {
			return nil, webhookError(CodeSignatureTooOld, fmt.Sprintf("The webhook was signed %s from now, more than the %s tolerance", skew.Round(time.Second), o.tolerance))
		}
	}

	var event WebhookEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		e := webhookError(CodeInvalidPayload, "The webhook body is not a JSON event")
		e.Err = err
		return nil, e
	}
	return &event, nil
}

// parseSignatureHeader reads "t=<digits>,v1=<hex>[,v1=<hex>...]".
// Whitespace around parts and unknown keys are ignored.
func parseSignatureHeader(header string) (timestamp string, signatures []string, ok bool) {
	for part := range strings.SplitSeq(header, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch key {
		case "t":
			if timestamp != "" || !isDigits(value) {
				return "", nil, false
			}
			timestamp = value
		case "v1":
			if value != "" {
				signatures = append(signatures, value)
			}
		}
	}
	return timestamp, signatures, timestamp != "" && len(signatures) > 0
}

func isDigits(s string) bool {
	if s == "" || len(s) > 18 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func webhookError(code, message string) *InvoistError {
	return &InvoistError{Status: 400, Code: code, Message: message}
}
