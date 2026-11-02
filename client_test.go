package invoist

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestNewRequiresAKey(t *testing.T) {
	for _, key := range []string{"", "  "} {
		c, err := New(key)
		if !errors.Is(err, ErrMissingAPIKey) || c != nil {
			t.Errorf("New(%q) = %v, %v; want ErrMissingAPIKey", key, c, err)
		}
	}
}

func TestNewRejectsABadBaseURL(t *testing.T) {
	for _, u := range []string{"api.invoi.st", "ftp://api.invoi.st", "://", "https://"} {
		if _, err := New("k", WithBaseURL(u)); err == nil {
			t.Errorf("WithBaseURL(%q) accepted", u)
		}
	}
}

func TestDefaults(t *testing.T) {
	c, err := New("k")
	if err != nil {
		t.Fatal(err)
	}
	if c.baseURL != DefaultBaseURL || c.timeout != DefaultTimeout || c.maxRetries != DefaultMaxRetries || c.httpClient != http.DefaultClient {
		t.Errorf("defaults = %q %s %d", c.baseURL, c.timeout, c.maxRetries)
	}
	if c.Bills == nil || c.HeldEmails == nil {
		t.Error("services not set")
	}
	c, _ = New("k", WithMaxRetries(-3), WithHTTPClient(nil))
	if c.maxRetries != 0 || c.httpClient != http.DefaultClient {
		t.Errorf("maxRetries = %d", c.maxRetries)
	}
}

func TestEveryRequestCarriesAuthAndUserAgent(t *testing.T) {
	ft := &fakeTransport{}
	c := newTestClient(t, ft, WithBaseURL("https://api.test///"))
	if _, err := c.ListCategories(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ResolveReviewItem(context.Background(), "r1", ""); err != nil {
		t.Fatal(err)
	}
	reqs := ft.seen()
	for _, r := range reqs {
		if got := r.header.Get("Authorization"); got != "Bearer inv_sk_test" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		if got := r.header.Get("User-Agent"); !regexp.MustCompile(`^invoist-go/` + regexp.QuoteMeta(Version) + ` \(go[0-9][^)]*\)$`).MatchString(got) {
			t.Errorf("User-Agent = %q", got)
		}
	}
	if reqs[0].path != "/v1/categories" {
		t.Errorf("trailing slashes not trimmed: %q", reqs[0].path)
	}
	if got := reqs[0].header.Get("Content-Type"); got != "" {
		t.Errorf("GET sent Content-Type %q", got)
	}
	if got := reqs[1].header.Get("Content-Type"); got != "application/json" {
		t.Errorf("JSON Content-Type = %q", got)
	}
	if got := string(reqs[1].body); got != `{"status":"resolved"}` {
		t.Errorf("resolve body = %s", got)
	}
}

func TestQueryEncoding(t *testing.T) {
	month := time.Date(2026, time.October, 15, 12, 0, 0, 0, time.Local)
	tests := []struct {
		name string
		call func(c *Client) error
		want string
	}{
		{"nil params send nothing", func(c *Client) error { _, err := c.Bills.List(context.Background(), nil); return err }, ""},
		{"zero params send nothing", func(c *Client) error { _, err := c.Bills.List(context.Background(), &ListBillsParams{}); return err }, ""},
		{"paid false is sent", func(c *Client) error {
			_, err := c.Bills.List(context.Background(), &ListBillsParams{Paid: new(false), Month: MonthOf(month), Limit: 10})
			return err
		}, "limit=10&month=2026-10&paid=false"},
		{"paid true and text", func(c *Client) error {
			_, err := c.Bills.List(context.Background(), &ListBillsParams{Paid: new(true), Sender: "Northwind Oy", Q: "a&b", Offset: 20})
			return err
		}, "offset=20&paid=true&q=a%26b&sender=Northwind+Oy"},
		{"documents", func(c *Client) error {
			_, err := c.ListDocuments(context.Background(), &ListDocumentsParams{Flagged: true, Status: DocumentStatusDone, Category: "none", Limit: 5})
			return err
		}, "category=none&flagged=true&limit=5&status=done"},
		{"documents unflagged omitted", func(c *Client) error {
			_, err := c.ListDocuments(context.Background(), &ListDocumentsParams{Q: "acme"})
			return err
		}, "q=acme"},
		{"review queue", func(c *Client) error {
			_, err := c.ListReviewQueue(context.Background(), &ListReviewQueueParams{Status: ReviewStatusPending})
			return err
		}, "status=pending"},
		{"summary", func(c *Client) error {
			_, err := c.Bills.Summary(context.Background(), &BillsSummaryParams{Today: DateOf(month), Days: 7})
			return err
		}, "days=7&today=2026-10-15"},
		{"stats", func(c *Client) error {
			_, err := c.Bills.Stats(context.Background(), &BillsStatsParams{Month: "2026-09", Sender: "Globex"})
			return err
		}, "month=2026-09&sender=Globex"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ft := &fakeTransport{}
			c := newTestClient(t, ft)
			if err := tt.call(c.Client); err != nil {
				t.Fatal(err)
			}
			if got := ft.seen()[0].query; got != tt.want {
				t.Errorf("query = %q, want %q", got, tt.want)
			}
		})
	}
}

// formPart is one part of a multipart/form-data body.
type formPart struct {
	field, filename, contentType string
	data                         []byte
}

// parts reads a multipart/form-data request body.
func parts(t *testing.T, r seenRequest) []formPart {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(r.header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		t.Fatalf("Content-Type = %q", r.header.Get("Content-Type"))
	}
	mr := multipart.NewReader(bytes.NewReader(r.body), params["boundary"])
	var out []formPart
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, formPart{p.FormName(), p.FileName(), p.Header.Get("Content-Type"), data})
	}
}

func TestExtractUploadsMultipart(t *testing.T) {
	ft := &fakeTransport{replies: []reply{{status: 201, body: `{"document":{"id":"d1"},"extraction":{"id":"x1","status":"pending"},"duplicate":false}`}}}
	c := newTestClient(t, ft)
	result, err := c.Extract(context.Background(), FileFromBytes("Lasku \"10\".pdf", []byte("%PDF-1.7 hello")), &ExtractParams{IdempotencyKey: "key-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Document.ID != "d1" || result.Extraction == nil || result.Extraction.Status != ExtractionStatusPending || result.Duplicate {
		t.Errorf("result = %+v", result)
	}
	r := ft.seen()[0]
	if r.method != http.MethodPost || r.path != "/v1/extract" {
		t.Errorf("%s %s", r.method, r.path)
	}
	if got := r.header.Get("Idempotency-Key"); got != "key-1" {
		t.Errorf("Idempotency-Key = %q", got)
	}
	ps := parts(t, r)
	if len(ps) != 1 {
		t.Fatalf("%d parts", len(ps))
	}
	if p := ps[0]; p.field != "file" || p.filename != `Lasku "10".pdf` || p.contentType != "application/pdf" || string(p.data) != "%PDF-1.7 hello" {
		t.Errorf("part = %+v", p)
	}
}

func TestExtractWithoutKeyOrName(t *testing.T) {
	ft := &fakeTransport{replies: []reply{{status: 200, body: `{"document":{"id":"d1"},"duplicate":true}`}}}
	c := newTestClient(t, ft)
	result, err := c.Extract(context.Background(), File{Data: []byte{1, 2}, ContentType: "image/png"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Duplicate || result.Extraction != nil {
		t.Errorf("result = %+v", result)
	}
	r := ft.seen()[0]
	if _, ok := r.header["Idempotency-Key"]; ok {
		t.Error("Idempotency-Key sent without a key")
	}
	p := parts(t, r)[0]
	if p.filename != "upload" || p.contentType != "image/png" || string(p.data) != "\x01\x02" {
		t.Errorf("part = %+v", p)
	}
}

func TestExtractBatchUploadsEveryFile(t *testing.T) {
	ft := &fakeTransport{replies: []reply{{status: 200, body: `{"results":[{"filename":"a.pdf","document_id":"d1"},{"filename":"upload-2","error":{"code":"unsupported_type","message":"no"}}],"accepted":1,"rejected":1}`}}}
	c := newTestClient(t, ft)
	result, err := c.ExtractBatch(context.Background(), []File{FileFromBytes("a.pdf", []byte("A")), {Data: []byte("B")}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted != 1 || result.Rejected != 1 || len(result.Results) != 2 || result.Results[1].Error.Code != "unsupported_type" {
		t.Errorf("result = %+v", result)
	}
	r := ft.seen()[0]
	if r.path != "/v1/extract/batch" {
		t.Errorf("path %s", r.path)
	}
	ps := parts(t, r)
	if len(ps) != 2 || ps[0].field != "files" || ps[1].field != "files" || ps[0].filename != "a.pdf" || ps[1].filename != "upload-2" ||
		string(ps[0].data) != "A" || string(ps[1].data) != "B" {
		t.Fatalf("parts = %+v", ps)
	}
	if ps[1].contentType != "application/octet-stream" {
		t.Errorf("default content type = %q", ps[1].contentType)
	}
	if _, err := c.ExtractBatch(context.Background(), nil); err == nil {
		t.Error("empty batch accepted")
	}
}

func TestUploadRetryResendsTheSameBytes(t *testing.T) {
	ft := &fakeTransport{replies: []reply{{status: 503}, {status: 0}, {status: 201, body: `{"document":{"id":"d1"},"extraction":{"status":"pending"},"duplicate":false}`}}}
	c := newTestClient(t, ft)
	if _, err := c.Extract(context.Background(), FileFromBytes("a.pdf", []byte("%PDF")), nil); err != nil {
		t.Fatal(err)
	}
	reqs := ft.seen()
	if len(reqs) != 3 {
		t.Fatalf("%d attempts", len(reqs))
	}
	for _, r := range reqs[1:] {
		if !bytes.Equal(r.body, reqs[0].body) || r.header.Get("Content-Type") != reqs[0].header.Get("Content-Type") {
			t.Error("retry sent a different body")
		}
	}
	if len(c.waits) != 2 || c.waits[0] != 500*time.Millisecond || c.waits[1] != time.Second {
		t.Errorf("waits = %v", c.waits)
	}
}

func TestFileConstructors(t *testing.T) {
	path := t.TempDir() + "/scan.jpg"
	if err := os.WriteFile(path, []byte("jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := FileFromPath(path)
	if err != nil || f.Filename != "scan.jpg" || string(f.Data) != "jpeg" || f.contentType() != "image/jpeg" {
		t.Errorf("FileFromPath = %+v, %v", f, err)
	}
	if _, err := FileFromPath(path + ".missing"); err == nil {
		t.Error("missing file accepted")
	}
	f, err = FileFromReader("r.png", strings.NewReader("png"))
	if err != nil || f.Filename != "r.png" || string(f.Data) != "png" {
		t.Errorf("FileFromReader = %+v, %v", f, err)
	}
	if _, err := FileFromReader("x", iotestErrReader{}); err == nil {
		t.Error("reader error swallowed")
	}
}

// delay is retryDelay for cases where Retry-After is within the limit.
func (c *testClient) delay(attempt int, retryAfter string) time.Duration {
	d, _ := c.retryDelay(attempt, retryAfter)
	return d
}

type iotestErrReader struct{}

func (iotestErrReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestNoContentResponse(t *testing.T) {
	ft := &fakeTransport{replies: []reply{{status: 204}}}
	c := newTestClient(t, ft)
	if err := c.DeleteWebhookEndpoint(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	if r := ft.seen()[0]; r.method != http.MethodDelete || r.path != "/v1/webhook-endpoints/w1" || len(r.body) != 0 {
		t.Errorf("%s %s %q", r.method, r.path, r.body)
	}
}

func TestBinaryDownloads(t *testing.T) {
	pdf := "%PDF-1.7\x00\xff\xfe binary"
	for name, get := range map[string]func(c *Client) (*Download, error){
		"/v1/documents/d%2F1/file": func(c *Client) (*Download, error) { return c.GetDocumentFile(context.Background(), "d/1") },
		"/v1/bills/b1/file":        func(c *Client) (*Download, error) { return c.Bills.GetFile(context.Background(), "b1") },
	} {
		t.Run(name, func(t *testing.T) {
			ft := &fakeTransport{replies: []reply{{status: 200, body: pdf, header: map[string]string{
				"Content-Type":        "application/pdf",
				"Content-Disposition": `inline; filename="lasku.pdf"`,
			}}}}
			c := newTestClient(t, ft)
			d, err := get(c.Client)
			if err != nil {
				t.Fatal(err)
			}
			if string(d.Data) != pdf || d.ContentType != "application/pdf" || d.Filename != "lasku.pdf" {
				t.Errorf("download = %q %q %q", d.Data, d.ContentType, d.Filename)
			}
			if got := ft.seen()[0].path; got != name {
				t.Errorf("path = %q", got)
			}
		})
	}
}

func TestDownloadErrorsAreInvoistErrors(t *testing.T) {
	ft := &fakeTransport{replies: []reply{{status: 404, body: `{"error":{"code":"not_found","message":"document not found"}}`}}}
	c := newTestClient(t, ft)
	_, err := c.GetDocumentFile(context.Background(), "d1")
	if e := asInvoistError(t, err); !e.IsNotFound() || len(ft.seen()) != 1 {
		t.Errorf("err = %v after %d attempts", err, len(ft.seen()))
	}
}

func TestRetryAfterIsHonoured(t *testing.T) {
	ft := &fakeTransport{replies: []reply{
		{status: 429, header: map[string]string{"Retry-After": "3"}},
		{status: 503, header: map[string]string{"Retry-After": "Wed, 21 Oct 2026 07:28:00 GMT"}},
		{status: 200, body: `{}`},
	}}
	c := newTestClient(t, ft)
	if _, err := c.GetStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	// An HTTP-date is not an integer, so the second wait is the backoff.
	if len(c.waits) != 2 || c.waits[0] != 3*time.Second || c.waits[1] != time.Second {
		t.Errorf("waits = %v", c.waits)
	}
}

func TestRetryDelay(t *testing.T) {
	c := newTestClient(t, &fakeTransport{})
	for attempt, want := range []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second} {
		if got := c.delay(attempt, ""); got != want {
			t.Errorf("attempt %d: %s, want %s", attempt, got, want)
		}
	}
	if got := c.delay(100, ""); got != 8*time.Second {
		t.Errorf("attempt 100: %s", got)
	}
	c.jitter = func() float64 { return 0 }
	if got := c.delay(0, ""); got != 400*time.Millisecond {
		t.Errorf("-20%%: %s", got)
	}
	c.jitter = func() float64 { return 0.999999 }
	if got := c.delay(1, ""); got < 1199*time.Millisecond || got > 1200*time.Millisecond {
		t.Errorf("+20%%: %s", got)
	}
	if got := c.delay(4, ""); got != 8*time.Second {
		t.Errorf("cap after jitter: %s", got)
	}
	if got := c.delay(0, " 2 "); got != 2*time.Second {
		t.Errorf("Retry-After 2: %s", got)
	}
	if got := c.delay(0, "0"); got != 0 {
		t.Errorf("Retry-After 0: %s", got)
	}
}

func TestRetryAfterLimit(t *testing.T) {
	c := newTestClient(t, &fakeTransport{})
	if d, ok := c.retryDelay(0, "60"); !ok || d != time.Minute {
		t.Errorf("60: %s %v", d, ok)
	}
	if _, ok := c.retryDelay(0, "61"); ok {
		t.Error("61s Retry-After accepted")
	}
	ft := &fakeTransport{replies: []reply{{status: 503, header: map[string]string{"Retry-After": "3600"}, body: `{"error":{"code":"maintenance","message":"back soon"}}`}}}
	c = newTestClient(t, ft)
	_, err := c.GetUsage(context.Background())
	if e := asInvoistError(t, err); e.Code != "maintenance" || len(ft.seen()) != 1 || len(c.waits) != 0 {
		t.Errorf("err = %v after %d attempts, waits %v", err, len(ft.seen()), c.waits)
	}
}

func TestNoRetriesWhenDisabled(t *testing.T) {
	ft := &fakeTransport{replies: []reply{{status: 503}, {status: 200, body: `{}`}}}
	c := newTestClient(t, ft, WithMaxRetries(0))
	if _, err := c.GetUsage(context.Background()); err == nil {
		t.Fatal("want an error")
	}
	if len(ft.seen()) != 1 {
		t.Errorf("%d attempts", len(ft.seen()))
	}
}

func TestConnectionFailureIsANetworkError(t *testing.T) {
	ft := &fakeTransport{replies: []reply{{status: 0}}}
	c := newTestClient(t, ft)
	_, err := c.GetUsage(context.Background())
	e := asInvoistError(t, err)
	if e.Status != 0 || e.Code != CodeNetworkError || !e.IsRetryable() || e.Err == nil || len(ft.seen()) != 3 {
		t.Errorf("err = %#v after %d attempts", e, len(ft.seen()))
	}
	if !strings.Contains(e.Error(), "connection refused") || !strings.Contains(e.Error(), "code network_error") {
		t.Errorf("Error() = %q", e.Error())
	}
	if errors.Unwrap(e) == nil {
		t.Error("Unwrap() = nil")
	}
}

func TestTimeoutIsRetriedAndReported(t *testing.T) {
	ft := &fakeTransport{handler: func(r *http.Request, _ []byte) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	}}
	c := newTestClient(t, ft, WithTimeout(5*time.Millisecond), WithMaxRetries(1))
	_, err := c.GetUsage(context.Background())
	e := asInvoistError(t, err)
	if e.Code != CodeTimeout || e.Status != 0 || !errors.Is(err, context.DeadlineExceeded) || len(ft.seen()) != 2 {
		t.Errorf("err = %v after %d attempts", err, len(ft.seen()))
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestHTTPClientTimeoutIsATimeout(t *testing.T) {
	ft := &fakeTransport{handler: func(*http.Request, []byte) (*http.Response, error) { return nil, timeoutError{} }}
	c := newTestClient(t, ft, WithTimeout(0), WithMaxRetries(0))
	_, err := c.GetUsage(context.Background())
	if e := asInvoistError(t, err); e.Code != CodeTimeout {
		t.Errorf("err = %v", err)
	}
}

func TestCallerCancellationIsNotRetried(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ft := &fakeTransport{handler: func(r *http.Request, _ []byte) (*http.Response, error) {
		cancel()
		<-r.Context().Done()
		return nil, r.Context().Err()
	}}
	c := newTestClient(t, ft)
	_, err := c.GetUsage(ctx)
	if !errors.Is(err, context.Canceled) || len(ft.seen()) != 1 {
		t.Errorf("err = %v after %d attempts", err, len(ft.seen()))
	}
	var e *InvoistError
	if errors.As(err, &e) {
		t.Error("cancellation surfaced as an InvoistError")
	}
}

func TestCancelledContextSendsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ft := &fakeTransport{}
	c := newTestClient(t, ft)
	if _, err := c.GetUsage(ctx); !errors.Is(err, context.Canceled) || len(ft.seen()) != 0 {
		t.Errorf("err = %v after %d requests", err, len(ft.seen()))
	}
}

func TestCancellationDuringRetryWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ft := &fakeTransport{replies: []reply{{status: 503}}}
	c := newTestClient(t, ft)
	c.sleep = func(ctx context.Context, d time.Duration) error { cancel(); return sleepContext(ctx, d) }
	if _, err := c.GetUsage(ctx); !errors.Is(err, context.Canceled) || len(ft.seen()) != 1 {
		t.Errorf("err = %v after %d attempts", err, len(ft.seen()))
	}
}

func TestSleepContext(t *testing.T) {
	if err := sleepContext(context.Background(), time.Millisecond); err != nil {
		t.Error(err)
	}
	if err := sleepContext(context.Background(), 0); err != nil {
		t.Error(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepContext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Error(err)
	}
}

func TestUndecodableSuccessIsUnexpectedResponse(t *testing.T) {
	ft := &fakeTransport{replies: []reply{{status: 200, body: "<html>", header: map[string]string{"X-Request-Id": "req_9"}}}}
	c := newTestClient(t, ft)
	_, err := c.GetUsage(context.Background())
	e := asInvoistError(t, err)
	if e.Code != CodeUnexpectedResponse || e.Status != 200 || e.RequestID != "req_9" || string(e.Body) != "<html>" {
		t.Errorf("err = %#v", e)
	}
}

func TestUnknownFieldsAreIgnored(t *testing.T) {
	ft := &fakeTransport{replies: []reply{{status: 200, body: `{"unpaid":3,"overdue":1,"due_soon":[{"currency":"EUR","count":2,"total_cents":12345,"new":true}],"days":30,"today":"2026-11-01","added_later":{"x":1}}`}}}
	c := newTestClient(t, ft)
	s, err := c.Bills.Summary(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Unpaid != 3 || s.DueSoon[0].TotalCents != 12345 || s.Today != "2026-11-01" {
		t.Errorf("summary = %+v", s)
	}
}

func TestEmptyIDsAreRejectedBeforeSending(t *testing.T) {
	ft := &fakeTransport{}
	c := newTestClient(t, ft)
	ctx := context.Background()
	calls := map[string]func() error{
		"GetDocument":           func() error { _, err := c.GetDocument(ctx, ""); return err },
		"GetDocumentFile":       func() error { _, err := c.GetDocumentFile(ctx, ""); return err },
		"WaitForExtraction":     func() error { _, err := c.WaitForExtraction(ctx, "", nil); return err },
		"ResolveReviewItem":     func() error { _, err := c.ResolveReviewItem(ctx, "", ""); return err },
		"DeleteWebhookEndpoint": func() error { return c.DeleteWebhookEndpoint(ctx, "") },
		"CreateWebhookEndpoint": func() error { _, err := c.CreateWebhookEndpoint(ctx, CreateWebhookEndpointParams{}); return err },
		"Bills.Get":             func() error { _, err := c.Bills.Get(ctx, ""); return err },
		"Bills.GetFile":         func() error { _, err := c.Bills.GetFile(ctx, ""); return err },
		"Bills.MarkPaid":        func() error { _, err := c.Bills.MarkPaid(ctx, ""); return err },
		"Bills.MarkUnpaid":      func() error { _, err := c.Bills.MarkUnpaid(ctx, ""); return err },
	}
	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s accepted an empty id", name)
		}
	}
	if n := len(ft.seen()); n != 0 {
		t.Errorf("%d requests sent", n)
	}
}

func TestErrorString(t *testing.T) {
	e := &InvoistError{Status: 401, Code: "invalid_api_key", Message: "invalid API key", RequestID: "req_1"}
	if got := e.Error(); got != "invoist: invalid API key (status 401, code invalid_api_key, request req_1)" {
		t.Errorf("Error() = %q", got)
	}
	e = &InvoistError{Code: CodeExtractionTimeout, Message: "late"}
	if got := e.Error(); got != "invoist: late (code extraction_timeout)" {
		t.Errorf("Error() = %q", got)
	}
}

func TestErrorFromResponseEdgeCases(t *testing.T) {
	for body, want := range map[string][2]string{
		`{"error":{"message":"no code"}}`:    {CodeUnexpectedResponse, "no code"},
		`{"error":null}`:                     {CodeUnexpectedResponse, "Request failed with status 418"},
		`{"error":["x"]}`:                    {CodeUnexpectedResponse, "Request failed with status 418"},
		`{"error":{"code":5,"message":"x"}}`: {CodeUnexpectedResponse, "Request failed with status 418"},
		`[1,2]`:                              {CodeUnexpectedResponse, "Request failed with status 418"},
	} {
		e := errorFromResponse(418, []byte(body), "")
		if e.Code != want[0] || e.Message != want[1] {
			t.Errorf("%s: %q %q", body, e.Code, e.Message)
		}
	}
}
