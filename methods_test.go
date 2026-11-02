package invoist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Every API method: the HTTP method and path it calls, and that it
// returns the unwrapped value rather than the envelope.
func TestEveryMethodHitsItsRoute(t *testing.T) {
	ctx := context.Background()
	file := FileFromBytes("a.pdf", []byte("%PDF"))
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		call   func(c *Client) (any, error)
		check  func(v any) bool
	}{
		{"Extract", "POST", "/v1/extract", `{"document":{"id":"d1"},"extraction":{"status":"pending"},"duplicate":false}`,
			func(c *Client) (any, error) { return c.Extract(ctx, file, nil) },
			func(v any) bool { return v.(*ExtractResult).Document.ID == "d1" }},
		{"ExtractBatch", "POST", "/v1/extract/batch", `{"results":[{"filename":"a.pdf"}],"accepted":1,"rejected":0}`,
			func(c *Client) (any, error) { return c.ExtractBatch(ctx, []File{file}) },
			func(v any) bool { return v.(*BatchResult).Accepted == 1 }},
		{"ListDocuments", "GET", "/v1/documents", `{"documents":[{"id":"d1","total_cents":9007199254740993}]}`,
			func(c *Client) (any, error) { return c.ListDocuments(ctx, nil) },
			func(v any) bool {
				d := v.([]DocumentListItem)
				return len(d) == 1 && *d[0].TotalCents == 9007199254740993
			}},
		{"GetDocument", "GET", "/v1/documents/d1", `{"document":{"id":"d1"},"review_queue":[{"id":"r1"}]}`,
			func(c *Client) (any, error) { return c.GetDocument(ctx, "d1") },
			func(v any) bool {
				d := v.(*DocumentDetail)
				return d.Document.ID == "d1" && d.ReviewQueue[0].ID == "r1"
			}},
		{"GetDocumentFile", "GET", "/v1/documents/d1/file", `PDF`,
			func(c *Client) (any, error) { return c.GetDocumentFile(ctx, "d1") },
			func(v any) bool { return string(v.(*Download).Data) == "PDF" }},
		{"WaitForExtraction", "GET", "/v1/documents/d1", `{"document":{"id":"d1"},"extraction":{"id":"x1","status":"done"},"review_queue":[]}`,
			func(c *Client) (any, error) { return c.WaitForExtraction(ctx, "d1", nil) },
			func(v any) bool { return v.(*Extraction).ID == "x1" }},
		{"ListReviewQueue", "GET", "/v1/review-queue", `{"review_queue":[{"id":"r1"}]}`,
			func(c *Client) (any, error) { return c.ListReviewQueue(ctx, nil) },
			func(v any) bool { return v.([]ReviewQueueListItem)[0].ID == "r1" }},
		{"ResolveReviewItem", "POST", "/v1/review-queue/r1/resolve", `{"review_queue_item":{"id":"r1","status":"dismissed"}}`,
			func(c *Client) (any, error) { return c.ResolveReviewItem(ctx, "r1", ReviewStatusDismissed) },
			func(v any) bool { return v.(*ReviewQueueItem).Status == ReviewStatusDismissed }},
		{"ListCategories", "GET", "/v1/categories", `{"categories":[{"id":"c1","name":"Travel"}]}`,
			func(c *Client) (any, error) { return c.ListCategories(ctx) },
			func(v any) bool { return v.([]Category)[0].Name == "Travel" }},
		{"GetUsage", "GET", "/v1/usage", `{"total_cost_cents":1250,"by_day":[{"day":"2026-11-01","calls":3}]}`,
			func(c *Client) (any, error) { return c.GetUsage(ctx) },
			func(v any) bool { u := v.(*Usage); return u.TotalCostCents == 1250 && *u.ByDay[0].Day == "2026-11-01" }},
		{"GetStats", "GET", "/v1/stats/overview", `{"documents_total":7,"plan":"growth"}`,
			func(c *Client) (any, error) { return c.GetStats(ctx) },
			func(v any) bool {
				s := v.(*StatsOverview)
				return s.DocumentsTotal == 7 && s.Plan == StatsOverviewPlanGrowth
			}},
		{"ListWebhookEndpoints", "GET", "/v1/webhook-endpoints", `{"endpoints":[{"id":"w1","url":"https://x"}],"known_events":["document.created"]}`,
			func(c *Client) (any, error) { return c.ListWebhookEndpoints(ctx) },
			func(v any) bool {
				l := v.(*WebhookEndpointList)
				return l.Endpoints[0].ID == "w1" && l.KnownEvents[0] == "document.created"
			}},
		{"CreateWebhookEndpoint", "POST", "/v1/webhook-endpoints", `{"endpoint":{"id":"w1","url":"https://x","secret":"whsec_1"}}`,
			func(c *Client) (any, error) {
				return c.CreateWebhookEndpoint(ctx, CreateWebhookEndpointParams{URL: "https://x", Events: []EventType{EventExtractionCompleted}})
			},
			func(v any) bool { return v.(*WebhookEndpointWithSecret).Secret == "whsec_1" }},
		{"DeleteWebhookEndpoint", "DELETE", "/v1/webhook-endpoints/w1", ``,
			func(c *Client) (any, error) { return nil, c.DeleteWebhookEndpoint(ctx, "w1") },
			func(any) bool { return true }},
		{"Bills.List", "GET", "/v1/bills", `{"bills":[{"id":"b1","kind":"reminder","letter":null,"bill":{"paid_at":null,"paid_by":null,"assigned_to":null},"related":{"document_id":null,"link":""}}]}`,
			func(c *Client) (any, error) { return c.Bills.List(ctx, nil) },
			func(v any) bool {
				b := v.([]Bill)
				return b[0].ID == "b1" && *b[0].Kind == BillKindReminder && b[0].Letter == nil && b[0].Bill.PaidAt == nil
			}},
		{"Bills.Get", "GET", "/v1/bills/b1", `{"document":{"id":"b1"},"review_queue":[],"bill":{"paid_at":"2026-10-30T08:00:00Z"},"chain":{"root_id":"b1","members":[{"document_id":"b1","date":"2026-10-01"}]}}`,
			func(c *Client) (any, error) { return c.Bills.Get(ctx, "b1") },
			func(v any) bool {
				b := v.(*BillDetail)
				return b.Bill.PaidAt != nil && b.Chain.Members[0].Date == "2026-10-01"
			}},
		{"Bills.GetFile", "GET", "/v1/bills/b1/file", `PNG`,
			func(c *Client) (any, error) { return c.Bills.GetFile(ctx, "b1") },
			func(v any) bool { return string(v.(*Download).Data) == "PNG" }},
		{"Bills.Summary", "GET", "/v1/bills/summary", `{"unpaid":2,"overdue":1,"due_soon":[],"days":30,"today":"2026-11-01"}`,
			func(c *Client) (any, error) { return c.Bills.Summary(ctx, nil) },
			func(v any) bool { return v.(*BillsSummary).Overdue == 1 }},
		{"Bills.Stats", "GET", "/v1/bills/stats", `{"month":"2026-10","totals":[{"currency":"EUR","count":4,"paid_count":3,"total_cents":45600,"paid_cents":30000}],"trend":[],"top_senders":[],"senders":[]}`,
			func(c *Client) (any, error) { return c.Bills.Stats(ctx, nil) },
			func(v any) bool { s := v.(*BillsStats); return s.Month == "2026-10" && s.Totals[0].PaidCents == 30000 }},
		{"Bills.MarkPaid", "POST", "/v1/bills/b1/paid", `{"bill":{"paid_at":"2026-11-01T10:00:00Z","paid_by":"u1","assigned_to":null}}`,
			func(c *Client) (any, error) { return c.Bills.MarkPaid(ctx, "b1") },
			func(v any) bool { return *v.(*BillRecord).PaidBy == "u1" }},
		{"Bills.MarkUnpaid", "DELETE", "/v1/bills/b1/paid", `{"bill":{"paid_at":null,"paid_by":null,"assigned_to":null}}`,
			func(c *Client) (any, error) { return c.Bills.MarkUnpaid(ctx, "b1") },
			func(v any) bool { return v.(*BillRecord).PaidAt == nil }},
		{"HeldEmails.List", "GET", "/v1/held-emails", `{"held_emails":[{"id":"h1","sender":"unknown","hold_reason":"new_sender","files":[]}]}`,
			func(c *Client) (any, error) { return c.HeldEmails.List(ctx) },
			func(v any) bool {
				h := v.([]HeldEmail)
				return h[0].Sender == HeldEmailSenderUnknown && *h[0].HoldReason == HeldEmailHoldReasonNewSender
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := 200
			if tt.body == "" {
				status = 204
			}
			ft := &fakeTransport{replies: []reply{{status: status, body: tt.body}}}
			c := newTestClient(t, ft)
			v, err := tt.call(c.Client)
			if err != nil {
				t.Fatal(err)
			}
			reqs := ft.seen()
			if len(reqs) != 1 || reqs[0].method != tt.method || reqs[0].path != tt.path {
				t.Fatalf("requests = %+v, want %s %s", reqs, tt.method, tt.path)
			}
			if !tt.check(v) {
				t.Errorf("result = %+v", v)
			}
		})
	}
}

func TestCreateWebhookEndpointBody(t *testing.T) {
	ft := &fakeTransport{replies: []reply{{status: 201, body: `{"endpoint":{"id":"w1"}}`}}}
	c := newTestClient(t, ft)
	if _, err := c.CreateWebhookEndpoint(context.Background(), CreateWebhookEndpointParams{URL: "https://example.com/hook", Description: "books", Events: []EventType{EventAll}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateWebhookEndpoint(context.Background(), CreateWebhookEndpointParams{URL: "https://example.com/hook"}); err != nil {
		t.Fatal(err)
	}
	reqs := ft.seen()
	if got := string(reqs[0].body); got != `{"url":"https://example.com/hook","description":"books","events":["*"]}` {
		t.Errorf("body = %s", got)
	}
	if got := string(reqs[1].body); got != `{"url":"https://example.com/hook"}` {
		t.Errorf("body = %s", got)
	}
}

func documentReply(status string) reply {
	if status == "" {
		return reply{status: 200, body: `{"document":{"id":"d1"},"review_queue":[]}`}
	}
	return reply{status: 200, body: fmt.Sprintf(`{"document":{"id":"d1"},"extraction":{"id":"x1","status":%q,"error_message":"unreadable"},"review_queue":[]}`, status)}
}

func TestWaitForExtraction(t *testing.T) {
	t.Run("done", func(t *testing.T) {
		ft := &fakeTransport{replies: []reply{documentReply(""), documentReply("pending"), documentReply("done")}}
		c := newTestClient(t, ft)
		x, err := c.WaitForExtraction(context.Background(), "d1", nil)
		if err != nil {
			t.Fatal(err)
		}
		if x.Status != ExtractionStatusDone || len(ft.seen()) != 3 {
			t.Errorf("status %s after %d polls", x.Status, len(ft.seen()))
		}
		if len(c.waits) != 2 || c.waits[0] != DefaultPollInterval {
			t.Errorf("waits = %v", c.waits)
		}
	})
	t.Run("failed is an answer", func(t *testing.T) {
		ft := &fakeTransport{replies: []reply{documentReply("failed")}}
		c := newTestClient(t, ft)
		x, err := c.WaitForExtraction(context.Background(), "d1", &WaitParams{PollInterval: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		if x.Status != ExtractionStatusFailed || *x.ErrorMessage != "unreadable" {
			t.Errorf("extraction = %+v", x)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		ft := &fakeTransport{replies: []reply{documentReply("pending")}}
		c := newTestClient(t, ft)
		_, err := c.WaitForExtraction(context.Background(), "d1", &WaitParams{Timeout: 5 * time.Second, PollInterval: 2 * time.Second})
		e := asInvoistError(t, err)
		if e.Status != 0 || e.Code != CodeExtractionTimeout {
			t.Errorf("err = %#v", e)
		}
		// Polls at 0, 2, 4 and 5s (the last wait is shortened to the deadline).
		if got := len(ft.seen()); got != 4 {
			t.Errorf("%d polls, waits %v", got, c.waits)
		}
		if c.waits[len(c.waits)-1] != time.Second {
			t.Errorf("waits = %v", c.waits)
		}
	})
	t.Run("default timeout", func(t *testing.T) {
		ft := &fakeTransport{replies: []reply{documentReply("pending")}}
		c := newTestClient(t, ft)
		_, err := c.WaitForExtraction(context.Background(), "d1", nil)
		if e := asInvoistError(t, err); e.Code != CodeExtractionTimeout {
			t.Fatal(err)
		}
		var total time.Duration
		for _, w := range c.waits {
			total += w
		}
		if total != DefaultWaitTimeout || len(ft.seen()) != 61 {
			t.Errorf("waited %s over %d polls", total, len(ft.seen()))
		}
	})
	t.Run("an error stops polling", func(t *testing.T) {
		ft := &fakeTransport{replies: []reply{{status: 404, body: `{"error":{"code":"not_found","message":"document not found"}}`}}}
		c := newTestClient(t, ft)
		_, err := c.WaitForExtraction(context.Background(), "d1", nil)
		if e := asInvoistError(t, err); !e.IsNotFound() {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		ft := &fakeTransport{replies: []reply{documentReply("pending")}}
		c := newTestClient(t, ft)
		c.sleep = func(ctx context.Context, d time.Duration) error { cancel(); return sleepContext(ctx, d) }
		if _, err := c.WaitForExtraction(ctx, "d1", nil); !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestExtractAndWait(t *testing.T) {
	ft := &fakeTransport{replies: []reply{
		{status: 201, body: `{"document":{"id":"d9"},"extraction":{"id":"x9","status":"pending"},"duplicate":false}`},
		{status: 200, body: `{"document":{"id":"d9"},"extraction":{"id":"x9","status":"done","total_cents":48000},"review_queue":[]}`},
	}}
	c := newTestClient(t, ft)
	result, err := c.ExtractAndWait(context.Background(), FileFromBytes("a.pdf", []byte("%PDF")), &ExtractAndWaitParams{IdempotencyKey: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Document.ID != "d9" || result.Extraction.Status != ExtractionStatusDone || *result.Extraction.TotalCents != 48000 {
		t.Errorf("result = %+v", result)
	}
	reqs := ft.seen()
	if len(reqs) != 2 || reqs[0].header.Get("Idempotency-Key") != "k1" || reqs[1].path != "/v1/documents/d9" {
		t.Errorf("requests = %+v", reqs)
	}
}

func TestExtractAndWaitWithAFinishedExtractionDoesNotPoll(t *testing.T) {
	// A test-mode key answers "done" straight away.
	ft := &fakeTransport{replies: []reply{{status: 201, body: `{"document":{"id":"d9"},"extraction":{"id":"x9","status":"done"},"duplicate":false}`}}}
	c := newTestClient(t, ft)
	result, err := c.ExtractAndWait(context.Background(), FileFromBytes("a.pdf", []byte("%PDF")), nil)
	if err != nil || result.Extraction.Status != ExtractionStatusDone || len(ft.seen()) != 1 {
		t.Errorf("result = %+v, err = %v, %d requests", result, err, len(ft.seen()))
	}
}

func TestExtractAndWaitPropagatesErrors(t *testing.T) {
	ft := &fakeTransport{replies: []reply{{status: 415, body: `{"error":{"code":"unsupported_type","message":"Use PDF, PNG, or JPEG."}}`}}}
	c := newTestClient(t, ft)
	_, err := c.ExtractAndWait(context.Background(), FileFromBytes("a.txt", []byte("hi")), nil)
	if e := asInvoistError(t, err); e.Code != "unsupported_type" {
		t.Errorf("err = %v", err)
	}
	ft = &fakeTransport{replies: []reply{{status: 201, body: `{"document":{"id":"d9"},"extraction":{"status":"pending"},"duplicate":false}`}, {status: 500}}}
	c = newTestClient(t, ft, WithMaxRetries(0))
	if _, err := c.ExtractAndWait(context.Background(), FileFromBytes("a.pdf", []byte("%PDF")), nil); err == nil {
		t.Error("poll failure swallowed")
	}
}

func TestWebhookEventData(t *testing.T) {
	var event WebhookEvent
	if err := json.Unmarshal([]byte(`{"id":"e1","type":"extraction.completed","created_at":"2026-11-01T10:40:00Z","data":{"document_id":"d1","status":"done"}}`), &event); err != nil {
		t.Fatal(err)
	}
	var data struct {
		DocumentID string `json:"document_id"`
	}
	if err := event.DecodeData(&data); err != nil || data.DocumentID != "d1" {
		t.Errorf("data = %+v, %v", data, err)
	}
	if event.Type != EventExtractionCompleted || !event.CreatedAt.Equal(time.Date(2026, time.November, 1, 10, 40, 0, 0, time.UTC)) {
		t.Errorf("event = %+v", event)
	}
	if err := (&WebhookEvent{ID: "e2"}).DecodeData(&data); err == nil {
		t.Error("empty data decoded")
	}
}

func TestMonthAndDateHelpers(t *testing.T) {
	d := time.Date(2026, time.January, 5, 23, 0, 0, 0, time.Local)
	if MonthOf(d) != "2026-01" || DateOf(d) != "2026-01-05" {
		t.Errorf("%s %s", MonthOf(d), DateOf(d))
	}
	// Local time decides: 23:30 UTC on 31 January is already 1 February at UTC+2.
	defer func(l *time.Location) { time.Local = l }(time.Local)
	time.Local = time.FixedZone("UTC+2", 2*60*60)
	utc := time.Date(2026, time.January, 31, 23, 30, 0, 0, time.UTC)
	if MonthOf(utc) != "2026-02" || DateOf(utc) != "2026-02-01" {
		t.Errorf("local: %s %s", MonthOf(utc), DateOf(utc))
	}
}

var _ http.RoundTripper = (*fakeTransport)(nil)
