package invoist

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/invoisthq/invoist-go/internal/generated"
)

// Methods for a business workspace. Each needs the key scope named in its
// comment; a key without it gets an *InvoistError with status 403.

const (
	// DefaultWaitTimeout is how long WaitForExtraction polls by default.
	DefaultWaitTimeout = 120 * time.Second
	// DefaultPollInterval is WaitForExtraction's default pause between polls.
	DefaultPollInterval = 2 * time.Second
)

func requireID(what, id string) error {
	if id == "" {
		return fmt.Errorf("invoist: %s id is empty", what)
	}
	return nil
}

// Extract uploads a PDF, PNG or JPEG (up to 20 MB) for reading
// (POST /v1/extract, scope documents:write). It returns once the file is
// stored, usually with the extraction still pending; use
// WaitForExtraction, ExtractAndWait, or the extraction.completed webhook
// for the result.
//
// Uploading bytes this workspace already extracted returns the existing
// document with Duplicate set, and bills nothing, so retrying an upload
// is safe. params may be nil.
func (c *Client) Extract(ctx context.Context, file File, params *ExtractParams) (*ExtractResult, error) {
	body, contentType, err := multipartBody("file", []File{file}, func(int) string { return "upload" })
	if err != nil {
		return nil, err
	}
	req := &request{method: http.MethodPost, path: "/v1/extract", body: body, contentType: contentType}
	if params != nil && params.IdempotencyKey != "" {
		req.headers = map[string]string{"Idempotency-Key": params.IdempotencyKey}
	}
	var out ExtractResult
	if err := c.call(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ExtractBatch uploads up to 10 files in one request
// (POST /v1/extract/batch, scope documents:write). Each file goes through
// the same checks as Extract, and one bad file does not fail the others:
// look at each entry of Results, which are in the order given.
func (c *Client) ExtractBatch(ctx context.Context, files []File) (*BatchResult, error) {
	if len(files) == 0 {
		return nil, errors.New("invoist: ExtractBatch needs at least one file")
	}
	body, contentType, err := multipartBody("files", files, func(i int) string { return fmt.Sprintf("upload-%d", i+1) })
	if err != nil {
		return nil, err
	}
	var out BatchResult
	if err := c.call(ctx, &request{method: http.MethodPost, path: "/v1/extract/batch", body: body, contentType: contentType}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListDocuments lists the workspace's documents, newest first
// (GET /v1/documents, scope documents:read). params may be nil.
func (c *Client) ListDocuments(ctx context.Context, params *ListDocumentsParams) ([]DocumentListItem, error) {
	var out generated.ListDocuments200JSONResponseBody
	if err := c.call(ctx, &request{method: http.MethodGet, path: "/v1/documents", query: encodeQuery(params)}, &out); err != nil {
		return nil, err
	}
	return out.Documents, nil
}

// GetDocument fetches a document with its extraction and review items
// (GET /v1/documents/{id}, scope documents:read).
func (c *Client) GetDocument(ctx context.Context, id string) (*DocumentDetail, error) {
	if err := requireID("document", id); err != nil {
		return nil, err
	}
	var out DocumentDetail
	if err := c.call(ctx, &request{method: http.MethodGet, path: pathFor("/v1/documents", id)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetDocumentFile downloads a document's original file, byte for byte
// (GET /v1/documents/{id}/file, scope documents:read).
func (c *Client) GetDocumentFile(ctx context.Context, id string) (*Download, error) {
	if err := requireID("document", id); err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, &request{method: http.MethodGet, path: pathFor("/v1/documents", id, "file")})
	if err != nil {
		return nil, err
	}
	return downloadFrom(resp), nil
}

// WaitForExtraction polls GetDocument until the document's extraction is
// no longer pending, and returns it. A failed extraction is returned, not
// raised: it is an answer about the document (see its ErrorMessage).
// Past the timeout it returns an *InvoistError with status 0 and code
// extraction_timeout. params may be nil.
func (c *Client) WaitForExtraction(ctx context.Context, documentID string, params *WaitParams) (*Extraction, error) {
	if err := requireID("document", documentID); err != nil {
		return nil, err
	}
	timeout, interval := DefaultWaitTimeout, DefaultPollInterval
	if params != nil {
		if params.Timeout > 0 {
			timeout = params.Timeout
		}
		if params.PollInterval > 0 {
			interval = params.PollInterval
		}
	}
	deadline := c.now().Add(timeout)
	for {
		detail, err := c.GetDocument(ctx, documentID)
		if err != nil {
			return nil, err
		}
		if x := detail.Extraction; x != nil && x.Status != ExtractionStatusPending {
			return x, nil
		}
		remaining := deadline.Sub(c.now())
		if remaining <= 0 {
			return nil, &InvoistError{
				Code:    CodeExtractionTimeout,
				Message: fmt.Sprintf("The extraction of document %s did not finish within %s", documentID, timeout),
			}
		}
		if err := c.sleep(ctx, min(interval, remaining)); err != nil {
			return nil, err
		}
	}
}

// ExtractAndWait uploads a file and waits for its extraction: Extract
// followed by WaitForExtraction. params may be nil.
func (c *Client) ExtractAndWait(ctx context.Context, file File, params *ExtractAndWaitParams) (*ExtractResult, error) {
	var p ExtractAndWaitParams
	if params != nil {
		p = *params
	}
	result, err := c.Extract(ctx, file, &ExtractParams{IdempotencyKey: p.IdempotencyKey})
	if err != nil {
		return nil, err
	}
	if x := result.Extraction; x != nil && x.Status != ExtractionStatusPending {
		return result, nil
	}
	extraction, err := c.WaitForExtraction(ctx, result.Document.ID, &WaitParams{Timeout: p.Timeout, PollInterval: p.PollInterval})
	if err != nil {
		return nil, err
	}
	result.Extraction = extraction
	return result, nil
}

// ListReviewQueue lists the items flagged for a person to check
// (GET /v1/review-queue, scope review:read). params may be nil.
func (c *Client) ListReviewQueue(ctx context.Context, params *ListReviewQueueParams) ([]ReviewQueueListItem, error) {
	var out generated.ListReviewQueue200JSONResponseBody
	if err := c.call(ctx, &request{method: http.MethodGet, path: "/v1/review-queue", query: encodeQuery(params)}, &out); err != nil {
		return nil, err
	}
	return out.ReviewQueue, nil
}

// ResolveReviewItem closes a review item as ReviewStatusResolved or
// ReviewStatusDismissed (POST /v1/review-queue/{id}/resolve, scope
// review:write). An empty status means resolved.
func (c *Client) ResolveReviewItem(ctx context.Context, id string, status ReviewStatus) (*ReviewQueueItem, error) {
	if err := requireID("review item", id); err != nil {
		return nil, err
	}
	if status == "" {
		status = ReviewStatusResolved
	}
	req, err := jsonRequest(http.MethodPost, pathFor("/v1/review-queue", id, "resolve"), map[string]ReviewStatus{"status": status})
	if err != nil {
		return nil, err
	}
	var out generated.ResolveReviewQueueItem200JSONResponseBody
	if err := c.call(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out.ReviewQueueItem, nil
}

// ListCategories lists the workspace's spend categories
// (GET /v1/categories, scope workspace:read).
func (c *Client) ListCategories(ctx context.Context) ([]Category, error) {
	var out generated.ListCategories200JSONResponseBody
	if err := c.call(ctx, &request{method: http.MethodGet, path: "/v1/categories"}, &out); err != nil {
		return nil, err
	}
	return out.Categories, nil
}

// GetUsage returns the current billing period's usage
// (GET /v1/usage, scope usage:read).
func (c *Client) GetUsage(ctx context.Context) (*Usage, error) {
	var out Usage
	if err := c.call(ctx, &request{method: http.MethodGet, path: "/v1/usage"}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetStats returns the workspace's document counts and plan allowance
// (GET /v1/stats/overview, scope stats:read).
func (c *Client) GetStats(ctx context.Context) (*StatsOverview, error) {
	var out StatsOverview
	if err := c.call(ctx, &request{method: http.MethodGet, path: "/v1/stats/overview"}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListWebhookEndpoints lists the registered webhook endpoints and the
// event types the API can send (GET /v1/webhook-endpoints, scope
// webhooks:manage).
func (c *Client) ListWebhookEndpoints(ctx context.Context) (*WebhookEndpointList, error) {
	var out WebhookEndpointList
	if err := c.call(ctx, &request{method: http.MethodGet, path: "/v1/webhook-endpoints"}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateWebhookEndpoint registers a URL to receive signed events
// (POST /v1/webhook-endpoints, scope webhooks:manage). The result's
// Secret, which VerifyWebhook needs, is returned only this once: store it.
func (c *Client) CreateWebhookEndpoint(ctx context.Context, params CreateWebhookEndpointParams) (*WebhookEndpointWithSecret, error) {
	if params.URL == "" {
		return nil, errors.New("invoist: CreateWebhookEndpoint needs a URL")
	}
	req, err := jsonRequest(http.MethodPost, "/v1/webhook-endpoints", params)
	if err != nil {
		return nil, err
	}
	var out generated.CreateWebhookEndpoint201JSONResponseBody
	if err := c.call(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out.Endpoint, nil
}

// DeleteWebhookEndpoint removes a webhook endpoint; deliveries to it stop
// at once (DELETE /v1/webhook-endpoints/{id}, scope webhooks:manage).
func (c *Client) DeleteWebhookEndpoint(ctx context.Context, id string) error {
	if err := requireID("webhook endpoint", id); err != nil {
		return err
	}
	return c.call(ctx, &request{method: http.MethodDelete, path: pathFor("/v1/webhook-endpoints", id)}, nil)
}
