package invoist

import (
	"bytes"
	"context"
	"errors"
	"math/rand/v2"
	"testing"
	"time"
)

// The shared vectors in testdata/conformance, run case by case: the cases
// every Invoist SDK must pass.

func TestConformanceErrors(t *testing.T) {
	var vectors struct {
		Cases []struct {
			Name    string            `json:"name"`
			Status  int               `json:"status"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
			Expect  struct {
				Code              string  `json:"code"`
				Message           string  `json:"message"`
				RequestID         *string `json:"request_id"`
				IsAuthError       bool    `json:"is_auth_error"`
				IsPermissionError bool    `json:"is_permission_error"`
				IsNotFound        bool    `json:"is_not_found"`
				IsRateLimited     bool    `json:"is_rate_limited"`
				IsRetryable       bool    `json:"is_retryable"`
			} `json:"expect"`
		} `json:"cases"`
	}
	loadVectors(t, "errors", &vectors)
	if len(vectors.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, tc := range vectors.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			ft := &fakeTransport{replies: []reply{{status: tc.Status, header: tc.Headers, body: tc.Body}}}
			c := newTestClient(t, ft, WithMaxRetries(0))
			_, err := c.GetUsage(context.Background())
			e := asInvoistError(t, err)
			want := tc.Expect
			wantRequestID := ""
			if want.RequestID != nil {
				wantRequestID = *want.RequestID
			}
			if e.Status != tc.Status {
				t.Errorf("Status = %d, want %d", e.Status, tc.Status)
			}
			if e.Code != want.Code {
				t.Errorf("Code = %q, want %q", e.Code, want.Code)
			}
			if e.Message != want.Message {
				t.Errorf("Message = %q, want %q", e.Message, want.Message)
			}
			if e.RequestID != wantRequestID {
				t.Errorf("RequestID = %q, want %q", e.RequestID, wantRequestID)
			}
			if !bytes.Equal(e.Body, []byte(tc.Body)) {
				t.Errorf("Body = %q, want %q", e.Body, tc.Body)
			}
			for _, p := range []struct {
				name      string
				got, want bool
			}{
				{"IsAuthError", e.IsAuthError(), want.IsAuthError},
				{"IsPermissionError", e.IsPermissionError(), want.IsPermissionError},
				{"IsNotFound", e.IsNotFound(), want.IsNotFound},
				{"IsRateLimited", e.IsRateLimited(), want.IsRateLimited},
				{"IsRetryable", e.IsRetryable(), want.IsRetryable},
			} {
				if p.got != p.want {
					t.Errorf("%s() = %v, want %v", p.name, p.got, p.want)
				}
			}
		})
	}
}

func TestConformanceRetries(t *testing.T) {
	var vectors struct {
		Cases []struct {
			Name      string `json:"name"`
			Responses []struct {
				Status     int    `json:"status"`
				RetryAfter string `json:"retry_after"`
			} `json:"responses"`
			Expect struct {
				OK       bool   `json:"ok"`
				Attempts int    `json:"attempts"`
				Status   int    `json:"status"`
				WaitsMS  []*int `json:"waits_ms"`
			} `json:"expect"`
		} `json:"cases"`
	}
	loadVectors(t, "retries", &vectors)
	if len(vectors.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, tc := range vectors.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			ft := &fakeTransport{}
			for _, r := range tc.Responses {
				rep := reply{status: r.Status, body: `{"error":{"code":"failed","message":"failed"}}`}
				if r.Status == 200 {
					rep.body = `{"total_calls":1}`
				}
				if r.RetryAfter != "" {
					rep.header = map[string]string{"Retry-After": r.RetryAfter}
				}
				ft.replies = append(ft.replies, rep)
			}
			// Spec default max_retries, and real jitter.
			c := newTestClient(t, ft, WithMaxRetries(DefaultMaxRetries))
			c.jitter = rand.Float64

			usage, err := c.GetUsage(context.Background())
			if got := len(ft.seen()); got != tc.Expect.Attempts {
				t.Errorf("attempts = %d, want %d", got, tc.Expect.Attempts)
			}
			if tc.Expect.OK {
				if err != nil {
					t.Fatalf("err = %v, want success", err)
				}
				if usage.TotalCalls != 1 {
					t.Errorf("usage = %+v", usage)
				}
			} else {
				e := asInvoistError(t, err)
				if e.Status != tc.Expect.Status {
					t.Errorf("Status = %d, want %d", e.Status, tc.Expect.Status)
				}
			}
			if tc.Expect.WaitsMS != nil && len(c.waits) != len(tc.Expect.WaitsMS) {
				t.Fatalf("waits = %v, want %d of them", c.waits, len(tc.Expect.WaitsMS))
			}
			if want := tc.Expect.Attempts - 1; len(c.waits) != want {
				t.Fatalf("waits = %v, want %d (one before each retry)", c.waits, want)
			}
			for i, got := range c.waits {
				if i < len(tc.Expect.WaitsMS) && tc.Expect.WaitsMS[i] != nil {
					if want := time.Duration(*tc.Expect.WaitsMS[i]) * time.Millisecond; got != want {
						t.Errorf("wait %d = %s, want %s (Retry-After)", i, got, want)
					}
					continue
				}
				base := 500 * time.Millisecond << i
				if lo, hi := base*8/10, base*12/10; got < lo || got > hi {
					t.Errorf("wait %d = %s, want %s ±20%%", i, got, base)
				}
			}
		})
	}
}

func TestConformanceWebhooks(t *testing.T) {
	var vectors struct {
		Secret string `json:"secret"`
		Cases  []struct {
			Name      string `json:"name"`
			Payload   string `json:"payload"`
			Header    string `json:"header"`
			Now       int64  `json:"now"`
			Tolerance int64  `json:"tolerance"`
			Expect    struct {
				OK   bool   `json:"ok"`
				Type string `json:"type"`
				Code string `json:"code"`
			} `json:"expect"`
		} `json:"cases"`
	}
	loadVectors(t, "webhooks", &vectors)
	if len(vectors.Cases) == 0 || vectors.Secret == "" {
		t.Fatal("no cases")
	}
	for _, tc := range vectors.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			event, err := VerifyWebhook([]byte(tc.Payload), tc.Header, vectors.Secret,
				WithTolerance(time.Duration(tc.Tolerance)*time.Second), WithNow(time.Unix(tc.Now, 0)))
			if tc.Expect.OK {
				if err != nil {
					t.Fatalf("err = %v, want the event", err)
				}
				if string(event.Type) != tc.Expect.Type {
					t.Errorf("Type = %q, want %q", event.Type, tc.Expect.Type)
				}
				return
			}
			if event != nil {
				t.Errorf("event = %+v, want nil", event)
			}
			var e *InvoistError
			if !errors.As(err, &e) {
				t.Fatalf("err = %v, want *InvoistError", err)
			}
			if e.Status != 400 || e.Code != tc.Expect.Code {
				t.Errorf("got status %d code %q, want 400 %q", e.Status, e.Code, tc.Expect.Code)
			}
		})
	}
}
