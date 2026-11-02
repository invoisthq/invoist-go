package invoist

import (
	"net/url"
	"reflect"
	"strconv"
	"time"
)

// Parameter structs. Every field is optional: a zero value is left out of
// the request, so the API's default applies. A nil *Params means "no
// filters". The `query` tags name the API's query parameters, and a test
// checks them against openapi.yaml's.

// ListDocumentsParams filters ListDocuments.
type ListDocumentsParams struct {
	// Limit is the most documents to return. The API defaults to 50 and
	// caps it at 200.
	Limit int `query:"limit"`
	// Offset skips this many documents, for paging.
	Offset int `query:"offset"`
	// Status keeps only documents in this state.
	Status DocumentStatus `query:"status"`
	// Flagged keeps only documents with review items still pending. It is
	// sent only when true; false lists every document.
	Flagged bool `query:"flagged"`
	// Q matches the filename, vendor, invoice number or reference,
	// ignoring case.
	Q string `query:"q"`
	// Category is a category id, or "none" for uncategorized documents.
	Category string `query:"category"`
}

// ListReviewQueueParams filters ListReviewQueue.
type ListReviewQueueParams struct {
	// Status keeps only items in this state. Empty lists every item.
	Status ReviewStatus `query:"status"`
}

// ExtractParams are the options of Extract.
type ExtractParams struct {
	// IdempotencyKey names one logical upload. Sending the same key again
	// replays the first response instead of creating a second document,
	// which covers re-sending byte-different copies of the same file
	// (identical bytes are recognised without a key). Reusing a key with a
	// different file fails with status 422. Keys expire after 24 hours.
	IdempotencyKey string
}

// WaitParams are the options of WaitForExtraction.
type WaitParams struct {
	// Timeout is how long to keep polling before giving up with code
	// extraction_timeout. Zero means DefaultWaitTimeout (120s).
	Timeout time.Duration
	// PollInterval is the pause between polls. Zero means
	// DefaultPollInterval (2s).
	PollInterval time.Duration
}

// ExtractAndWaitParams are the options of ExtractAndWait.
type ExtractAndWaitParams struct {
	// IdempotencyKey is passed to Extract; see ExtractParams.
	IdempotencyKey string
	// Timeout is how long to poll for the result; see WaitParams.
	Timeout time.Duration
	// PollInterval is the pause between polls; see WaitParams.
	PollInterval time.Duration
}

// CreateWebhookEndpointParams describes a webhook endpoint to register.
type CreateWebhookEndpointParams struct {
	// URL is the HTTP or HTTPS address that receives the events. Required.
	URL string `json:"url"`
	// Description is a note for people reading the endpoint list.
	Description string `json:"description,omitempty"`
	// Events are the event types to send, such as EventExtractionCompleted.
	// Empty, or []EventType{"*"}, subscribes to every event.
	Events []EventType `json:"events,omitempty"`
}

// ListBillsParams filters Bills.List.
type ListBillsParams struct {
	// Paid, when set, keeps only bills marked paid (true) or still to pay
	// (false). Nil lists both: Paid: new(false) lists what is left to pay.
	Paid *bool `query:"paid"`
	// Month keeps only the bills of one month, "YYYY-MM", by due date (or
	// the bill's own date when it has none). MonthOf formats a time.Time.
	Month string `query:"month"`
	// Sender keeps only one sender's bills, ignoring case and spacing.
	Sender string `query:"sender"`
	// Q matches the filename, sender, bill number or reference, ignoring
	// case, across every month.
	Q string `query:"q"`
	// Limit is the most bills to return. The API defaults to 50 and caps
	// it at 200.
	Limit int `query:"limit"`
	// Offset skips this many bills, for paging.
	Offset int `query:"offset"`
}

// BillsSummaryParams are the options of Bills.Summary.
type BillsSummaryParams struct {
	// Today is the household's own date, "YYYY-MM-DD", which must be within
	// two days of the server's. Empty uses the server's. DateOf formats a
	// time.Time.
	Today string `query:"today"`
	// Days is how far ahead "due soon" reaches. The API defaults to 30.
	Days int `query:"days"`
}

// BillsStatsParams are the options of Bills.Stats.
type BillsStatsParams struct {
	// Month is the month to report, "YYYY-MM". Empty is the current month.
	// MonthOf formats a time.Time.
	Month string `query:"month"`
	// Sender narrows the totals and the trend to one sender.
	Sender string `query:"sender"`
}

// MonthOf formats t's year and month in local time as the API's
// "YYYY-MM", for ListBillsParams.Month and BillsStatsParams.Month.
func MonthOf(t time.Time) string { return t.Local().Format("2006-01") }

// DateOf formats t's date in local time as the API's "YYYY-MM-DD", for
// BillsSummaryParams.Today.
func DateOf(t time.Time) string { return t.Local().Format(time.DateOnly) }

// encodeQuery turns a parameter struct (or a nil pointer to one) into
// query values, leaving out zero values. Booleans are "true"/"false".
func encodeQuery(params any) url.Values {
	q := url.Values{}
	v := reflect.ValueOf(params)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return q
		}
		v = v.Elem()
	}
	t := v.Type()
	for i := range t.NumField() {
		name := t.Field(i).Tag.Get("query")
		if name == "" {
			continue
		}
		f := v.Field(i)
		if f.Kind() == reflect.Pointer {
			if f.IsNil() {
				continue
			}
			f = f.Elem()
		} else if f.IsZero() {
			continue
		}
		switch f.Kind() {
		case reflect.String:
			q.Set(name, f.String())
		case reflect.Bool:
			q.Set(name, strconv.FormatBool(f.Bool()))
		case reflect.Int, reflect.Int64:
			q.Set(name, strconv.FormatInt(f.Int(), 10))
		default:
			panic("invoist: unsupported query field kind " + f.Kind().String())
		}
	}
	return q
}
