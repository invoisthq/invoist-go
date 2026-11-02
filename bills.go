package invoist

import (
	"context"
	"net/http"

	"github.com/invoisthq/invoist-go/internal/generated"
)

// BillsService holds a household's bills (Invoist Personal). Reach it as
// Client.Bills. Every method needs the bills:read scope, and MarkPaid and
// MarkUnpaid also bills:write; a business workspace's key gets an
// *InvoistError with status 403 and code household_feature.
type BillsService struct {
	client *Client
}

// List lists the household's bills; unpaid bills come soonest-due first
// (GET /v1/bills). A reminder or collection letter about a bill is folded
// into that bill (see Bill.Letter). params may be nil.
func (s *BillsService) List(ctx context.Context, params *ListBillsParams) ([]Bill, error) {
	var out generated.ListBills200JSONResponseBody
	if err := s.client.call(ctx, &request{method: http.MethodGet, path: "/v1/bills", query: encodeQuery(params)}, &out); err != nil {
		return nil, err
	}
	return out.Bills, nil
}

// Get fetches one bill with everything read from it, its open warnings,
// the household's record and its letters (GET /v1/bills/{id}).
func (s *BillsService) Get(ctx context.Context, id string) (*BillDetail, error) {
	if err := requireID("bill", id); err != nil {
		return nil, err
	}
	var out BillDetail
	if err := s.client.call(ctx, &request{method: http.MethodGet, path: pathFor("/v1/bills", id)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetFile downloads a bill's original file, byte for byte
// (GET /v1/bills/{id}/file).
func (s *BillsService) GetFile(ctx context.Context, id string) (*Download, error) {
	if err := requireID("bill", id); err != nil {
		return nil, err
	}
	resp, err := s.client.send(ctx, &request{method: http.MethodGet, path: pathFor("/v1/bills", id, "file")})
	if err != nil {
		return nil, err
	}
	return downloadFrom(resp), nil
}

// Summary says what there is to pay: how many bills are unpaid and
// overdue, and what falls due soon, per currency (GET /v1/bills/summary).
// params may be nil.
func (s *BillsService) Summary(ctx context.Context, params *BillsSummaryParams) (*BillsSummary, error) {
	var out BillsSummary
	if err := s.client.call(ctx, &request{method: http.MethodGet, path: "/v1/bills/summary", query: encodeQuery(params)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Stats returns a month at a glance: totals billed and paid per currency,
// the twelve months up to it, and the biggest senders
// (GET /v1/bills/stats). params may be nil.
func (s *BillsService) Stats(ctx context.Context, params *BillsStatsParams) (*BillsStats, error) {
	var out BillsStats
	if err := s.client.call(ctx, &request{method: http.MethodGet, path: "/v1/bills/stats", query: encodeQuery(params)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MarkPaid records a bill and its letters as paid by the key's creator,
// and returns the household's record (POST /v1/bills/{id}/paid, scope
// bills:write).
func (s *BillsService) MarkPaid(ctx context.Context, id string) (*BillRecord, error) {
	return s.setPaid(ctx, http.MethodPost, id)
}

// MarkUnpaid records a bill as not paid, and returns the household's
// record (DELETE /v1/bills/{id}/paid, scope bills:write).
func (s *BillsService) MarkUnpaid(ctx context.Context, id string) (*BillRecord, error) {
	return s.setPaid(ctx, http.MethodDelete, id)
}

func (s *BillsService) setPaid(ctx context.Context, method, id string) (*BillRecord, error) {
	if err := requireID("bill", id); err != nil {
		return nil, err
	}
	var out generated.MarkBillPaid200JSONResponseBody
	if err := s.client.call(ctx, &request{method: method, path: pathFor("/v1/bills", id, "paid")}, &out); err != nil {
		return nil, err
	}
	return &out.Bill, nil
}

// HeldEmailsService holds the emails forwarded to a household that wait
// for a person to approve their sender, or for a plan. Reach it as
// Client.HeldEmails. It needs the bills:read scope.
type HeldEmailsService struct {
	client *Client
}

// List lists the waiting emails, oldest first (GET /v1/held-emails). It is
// read-only: approving a sender is done in the app.
func (s *HeldEmailsService) List(ctx context.Context) ([]HeldEmail, error) {
	var out generated.ListHeldEmails200JSONResponseBody
	if err := s.client.call(ctx, &request{method: http.MethodGet, path: "/v1/held-emails"}, &out); err != nil {
		return nil, err
	}
	return out.HeldEmails, nil
}
