package invoist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the production API.
	DefaultBaseURL = "https://api.invoi.st"
	// DefaultTimeout bounds each attempt of a request, including reading
	// the response. It is generous because uploads can be large.
	DefaultTimeout = 60 * time.Second
	// DefaultMaxRetries is how many times a retryable failure is retried,
	// so a call makes at most DefaultMaxRetries+1 attempts.
	DefaultMaxRetries = 2
)

const (
	retryBaseDelay = 500 * time.Millisecond
	retryMaxDelay  = 8 * time.Second
	retryJitter    = 0.2
	// maxRetryAfter is the longest Retry-After the client waits for. A
	// server asking for longer gets its error returned at once.
	maxRetryAfter = 60 * time.Second
)

// userAgent is sent with every request, e.g. "invoist-go/1.0.0 (go1.26.2)".
var userAgent = "invoist-go/" + Version + " (" + runtime.Version() + ")"

// Client calls the Invoist API. Create one with New and reuse it: a
// *Client is safe for concurrent use by multiple goroutines.
//
// Methods for a business workspace (documents, extraction, the review
// queue, webhooks) are on Client itself; a household's bills are on
// Client.Bills and Client.HeldEmails.
type Client struct {
	// Bills holds a household's bills (Invoist Personal). The key needs
	// the bills:read scope, and bills:write to mark bills paid.
	Bills *BillsService
	// HeldEmails holds emails forwarded to a household that wait for a
	// person to approve their sender.
	HeldEmails *HeldEmailsService

	apiKey     string
	baseURL    string
	httpClient *http.Client
	timeout    time.Duration
	maxRetries int

	// Test seams: how the client waits between retries and polls, the
	// clock WaitForExtraction reads, and the jitter source.
	sleep  func(ctx context.Context, d time.Duration) error
	now    func() time.Time
	jitter func() float64
}

// Option configures a Client. Pass any number of them to New.
type Option func(*Client)

// WithBaseURL points the client at another API origin, such as a staging
// deployment or a local server. Trailing slashes are ignored.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(baseURL, "/") }
}

// WithHTTPClient makes the client send requests through hc, for a custom
// transport, a proxy, or a fake in tests. The client's own timeout still
// applies to each attempt.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// WithTimeout sets how long one attempt of a request may take, from
// sending it to reading the whole response (default 60s). Zero or less
// disables the limit; the context you pass still applies.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.timeout = d }
}

// WithMaxRetries sets how many times a retryable failure (no response, a
// 429, or a 5xx) is retried (default 2). Zero disables retries.
func WithMaxRetries(n int) Option {
	return func(c *Client) { c.maxRetries = max(n, 0) }
}

// New returns a Client that authenticates with apiKey: a workspace or
// household API key (inv_sk_...) or an OAuth access token (inv_at_...).
// It returns ErrMissingAPIKey when apiKey is empty, and an error when
// WithBaseURL was given something that is not an absolute URL.
func New(apiKey string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, ErrMissingAPIKey
	}
	c := &Client{
		apiKey:     apiKey,
		baseURL:    DefaultBaseURL,
		httpClient: http.DefaultClient,
		timeout:    DefaultTimeout,
		maxRetries: DefaultMaxRetries,
		sleep:      sleepContext,
		now:        time.Now,
		jitter:     rand.Float64,
	}
	for _, opt := range opts {
		opt(c)
	}
	u, err := url.Parse(c.baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invoist: base URL %q is not an absolute http(s) URL", c.baseURL)
	}
	c.Bills = &BillsService{client: c}
	c.HeldEmails = &HeldEmailsService{client: c}
	return c, nil
}

// request is one API call, before it is sent.
type request struct {
	method      string
	path        string
	query       url.Values
	body        []byte // buffered, so a retry can resend it
	contentType string
	headers     map[string]string
}

// response is a successful (2xx) answer.
type response struct {
	status int
	header http.Header
	body   []byte
}

// jsonRequest builds a request whose body is v encoded as JSON.
func jsonRequest(method, path string, v any) (*request, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("invoist: encoding request body: %w", err)
	}
	return &request{method: method, path: path, body: body, contentType: "application/json"}, nil
}

// call sends req and, when out is non-nil and the response has a body,
// decodes the JSON into out.
func (c *Client) call(ctx context.Context, req *request, out any) error {
	resp, err := c.send(ctx, req)
	if err != nil {
		return err
	}
	if out == nil || len(bytes.TrimSpace(resp.body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(resp.body, out); err != nil {
		return &InvoistError{
			Status:    resp.status,
			Code:      CodeUnexpectedResponse,
			Message:   fmt.Sprintf("Could not read the response (status %d): %v", resp.status, err),
			RequestID: resp.header.Get("X-Request-Id"),
			Body:      resp.body,
			Err:       err,
		}
	}
	return nil
}

// send makes the request, retrying what is safe to retry, and
// returns the first 2xx response or the last failure.
func (c *Client) send(ctx context.Context, req *request) (*response, error) {
	target := c.baseURL + req.path
	if len(req.query) > 0 {
		target += "?" + req.query.Encode()
	}
	for attempt := 0; ; attempt++ {
		resp, retryAfter, err := c.attempt(ctx, req, target)
		if err == nil {
			return resp, nil
		}
		var apiErr *InvoistError
		if !errors.As(err, &apiErr) || !apiErr.IsRetryable() || attempt >= c.maxRetries {
			return nil, err
		}
		delay, ok := c.retryDelay(attempt, retryAfter)
		if !ok {
			return nil, err
		}
		if err := c.sleep(ctx, delay); err != nil {
			return nil, err
		}
	}
}

// attempt sends req once. It returns the Retry-After header of a failed
// response alongside the error, for the retry loop.
func (c *Client) attempt(ctx context.Context, req *request, target string) (*response, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	attemptCtx, cancel := ctx, context.CancelFunc(func() {})
	if c.timeout > 0 {
		attemptCtx, cancel = context.WithTimeout(ctx, c.timeout)
	}
	defer cancel()

	var body io.Reader
	if req.body != nil {
		body = bytes.NewReader(req.body)
	}
	httpReq, err := http.NewRequestWithContext(attemptCtx, req.method, target, body)
	if err != nil {
		return nil, "", fmt.Errorf("invoist: building request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", userAgent)
	if req.contentType != "" {
		httpReq.Header.Set("Content-Type", req.contentType)
	}
	for k, v := range req.headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, "", c.transportError(ctx, attemptCtx, err)
	}
	defer httpResp.Body.Close()
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, "", c.transportError(ctx, attemptCtx, err)
	}

	if httpResp.StatusCode >= 200 && httpResp.StatusCode < 300 {
		return &response{status: httpResp.StatusCode, header: httpResp.Header, body: respBody}, "", nil
	}
	apiErr := errorFromResponse(httpResp.StatusCode, respBody, httpResp.Header.Get("X-Request-Id"))
	return nil, httpResp.Header.Get("Retry-After"), apiErr
}

// transportError classifies a failure where no (complete) response
// arrived. The caller's own cancellation is returned as is and never
// retried; everything else is a status-0 InvoistError.
func (c *Client) transportError(ctx, attemptCtx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
		return &InvoistError{
			Code:    CodeTimeout,
			Message: fmt.Sprintf("The request timed out after %s", c.timeout),
			Err:     err,
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &InvoistError{Code: CodeTimeout, Message: "The request timed out", Err: err}
	}
	return &InvoistError{
		Code:    CodeNetworkError,
		Message: "Network error: " + err.Error(),
		Err:     err,
	}
}

// retryDelay is how long to wait before retry number attempt+1: the
// response's Retry-After seconds when it sent an integer, otherwise
// 500ms × 2^attempt with ±20% jitter, capped at 8s. A non-integer
// Retry-After (such as an HTTP date) is ignored. ok is false when
// Retry-After asks for more than 60s: the call then fails at once.
func (c *Client) retryDelay(attempt int, retryAfter string) (delay time.Duration, ok bool) {
	if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && seconds >= 0 {
		if d := time.Duration(seconds) * time.Second; d <= maxRetryAfter {
			return d, true
		}
		return 0, false
	}
	backoff := float64(retryBaseDelay) * float64(uint64(1)<<min(attempt, 30))
	backoff *= 1 - retryJitter + 2*retryJitter*c.jitter()
	return min(time.Duration(backoff), retryMaxDelay), true
}

// sleepContext waits for d, or returns the context's error if it ends first.
func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// pathFor joins escaped path segments onto a route, e.g.
// pathFor("/v1/bills", id, "paid").
func pathFor(base string, segments ...string) string {
	var b strings.Builder
	b.WriteString(base)
	for _, s := range segments {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(s))
	}
	return b.String()
}
