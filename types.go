package invoist

import "github.com/invoisthq/invoist-go/internal/generated"

// Result types of operations whose response is not a single schema. The
// schemas themselves are re-exported in types_gen.go.

// ExtractResult is what Extract and ExtractAndWait return.
type ExtractResult struct {
	// Document is the stored document.
	Document Document `json:"document"`
	// Extraction is the reading of the document. After Extract it is
	// usually still pending (a test-mode key answers "done" at once);
	// after ExtractAndWait it is done or failed. It can be nil on a
	// duplicate whose extraction no longer exists.
	Extraction *Extraction `json:"extraction,omitempty"`
	// Duplicate is true when these exact bytes were already extracted in
	// this workspace: Document is the existing one and nothing was billed.
	Duplicate bool `json:"duplicate"`
}

// DocumentDetail is what GetDocument returns: the document, its
// extraction (nil until one exists), the job producing it, and its review
// items.
type DocumentDetail = generated.GetDocument200JSONResponseBody

// BatchResult is what ExtractBatch returns: one entry per file, in the
// order the files were given, and how many were accepted and rejected.
type BatchResult = generated.ExtractDocumentBatch200JSONResponseBody

// WebhookEndpointList is what ListWebhookEndpoints returns: the registered
// endpoints and every event type the API can send.
type WebhookEndpointList = generated.ListWebhookEndpoints200JSONResponseBody
