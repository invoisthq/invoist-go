package invoist_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	invoist "github.com/invoisthq/invoist-go"
)

// Live tests call a real API, read-only. They are skipped unless
// INVOIST_API_KEY is set; INVOIST_BASE_URL optionally points them at
// another deployment. The key may be a business workspace's or a
// household's: each test checks what that kind of key can read.

const missingID = "00000000-0000-7000-8000-000000000000"

func liveClient(t *testing.T, key string) *invoist.Client {
	t.Helper()
	if os.Getenv("INVOIST_API_KEY") == "" {
		t.Skip("set INVOIST_API_KEY to run live tests")
	}
	opts := []invoist.Option{invoist.WithTimeout(30 * time.Second)}
	if u := os.Getenv("INVOIST_BASE_URL"); u != "" {
		opts = append(opts, invoist.WithBaseURL(u))
	}
	c, err := invoist.New(key, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func liveContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// isHousehold reports whether the key belongs to a household, by asking
// a business route.
func isHousehold(t *testing.T, c *invoist.Client) bool {
	_, err := c.ListDocuments(liveContext(t), &invoist.ListDocumentsParams{Limit: 1})
	var apiErr *invoist.InvoistError
	if errors.As(err, &apiErr) && apiErr.Code == "business_feature" {
		return true
	}
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	return false
}

// skipIfForbidden skips when the key lacks the scope for one call.
func skipIfForbidden(t *testing.T, err error) {
	var apiErr *invoist.InvoistError
	if errors.As(err, &apiErr) && apiErr.IsPermissionError() {
		t.Skipf("key lacks the scope: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestLiveReads(t *testing.T) {
	c := liveClient(t, os.Getenv("INVOIST_API_KEY"))
	ctx := liveContext(t)
	if isHousehold(t, c) {
		bills, err := c.Bills.List(ctx, &invoist.ListBillsParams{Paid: new(false), Limit: 5})
		skipIfForbidden(t, err)
		t.Logf("%d unpaid bills", len(bills))
		summary, err := c.Bills.Summary(ctx, nil)
		skipIfForbidden(t, err)
		if summary.Today == "" {
			t.Errorf("summary = %+v", summary)
		}
		if _, err := c.Bills.Stats(ctx, &invoist.BillsStatsParams{Month: invoist.MonthOf(time.Now())}); err != nil {
			t.Error(err)
		}
		if _, err := c.HeldEmails.List(ctx); err != nil {
			t.Error(err)
		}
		return
	}
	docs, err := c.ListDocuments(ctx, &invoist.ListDocumentsParams{Limit: 5})
	skipIfForbidden(t, err)
	t.Logf("%d documents", len(docs))
	if _, err := c.ListReviewQueue(ctx, &invoist.ListReviewQueueParams{Status: invoist.ReviewStatusPending}); err != nil {
		var apiErr *invoist.InvoistError
		if !errors.As(err, &apiErr) || !apiErr.IsPermissionError() {
			t.Error(err)
		}
	}
}

func TestLiveMissingResourceIsNotFound(t *testing.T) {
	c := liveClient(t, os.Getenv("INVOIST_API_KEY"))
	ctx := liveContext(t)
	var err error
	if isHousehold(t, c) {
		_, err = c.Bills.Get(ctx, missingID)
	} else {
		_, err = c.GetDocument(ctx, missingID)
	}
	var apiErr *invoist.InvoistError
	if errors.As(err, &apiErr) && apiErr.IsPermissionError() {
		t.Skipf("key lacks the scope: %v", err)
	}
	if !errors.As(err, &apiErr) || !apiErr.IsNotFound() {
		t.Fatalf("err = %v, want a 404", err)
	}
}

func TestLiveBadKeyIsAnAuthError(t *testing.T) {
	c := liveClient(t, "inv_sk_not_a_real_key")
	_, err := c.ListCategories(liveContext(t))
	var apiErr *invoist.InvoistError
	if !errors.As(err, &apiErr) || !apiErr.IsAuthError() {
		t.Fatalf("err = %v, want a 401", err)
	}
}
