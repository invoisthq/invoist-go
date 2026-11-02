package invoist

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// seenRequest is what fakeTransport records of each request.
type seenRequest struct {
	method string
	path   string
	query  string
	header http.Header
	body   []byte
}

// reply is one scripted answer. status 0 means the connection fails.
type reply struct {
	status int
	header map[string]string
	body   string
}

// fakeTransport is an http.RoundTripper that answers from a script (the
// last reply repeats) or from a handler, and records every request.
type fakeTransport struct {
	mu       sync.Mutex
	replies  []reply
	handler  func(r *http.Request, body []byte) (*http.Response, error)
	requests []seenRequest
}

func (f *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
		r.Body.Close()
	}
	f.mu.Lock()
	f.requests = append(f.requests, seenRequest{method: r.Method, path: r.URL.EscapedPath(), query: r.URL.RawQuery, header: r.Header.Clone(), body: body})
	n := len(f.requests)
	f.mu.Unlock()

	if f.handler != nil {
		return f.handler(r, body)
	}
	if len(f.replies) == 0 {
		return respond(r, 200, nil, "{}"), nil
	}
	rep := f.replies[min(n, len(f.replies))-1]
	if rep.status == 0 {
		return nil, errors.New("connection refused")
	}
	return respond(r, rep.status, rep.header, rep.body), nil
}

func (f *fakeTransport) seen() []seenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seenRequest(nil), f.requests...)
}

func respond(r *http.Request, status int, header map[string]string, body string) *http.Response {
	h := http.Header{}
	for k, v := range header {
		h.Set(k, v)
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}
}

// testClient returns a client over ft whose waits are recorded instead of
// slept, on a fake clock that the waits advance.
type testClient struct {
	*Client
	ft    *fakeTransport
	waits []time.Duration
	clock time.Time
}

func newTestClient(t *testing.T, ft *fakeTransport, opts ...Option) *testClient {
	t.Helper()
	opts = append([]Option{WithBaseURL("https://api.test/"), WithHTTPClient(&http.Client{Transport: ft})}, opts...)
	c, err := New("inv_sk_test", opts...)
	if err != nil {
		t.Fatal(err)
	}
	tc := &testClient{Client: c, ft: ft, clock: time.Unix(1_793_520_000, 0)}
	c.sleep = func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		tc.waits = append(tc.waits, d)
		tc.clock = tc.clock.Add(d)
		return nil
	}
	c.now = func() time.Time { return tc.clock }
	c.jitter = func() float64 { return 0.5 } // exactly no jitter
	return tc
}

func asInvoistError(t *testing.T, err error) *InvoistError {
	t.Helper()
	var apiErr *InvoistError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v (%T) is not an *InvoistError", err, err)
	}
	return apiErr
}

// loadVectors decodes testdata/conformance/<name>.json.
func loadVectors(t *testing.T, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "conformance", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}
