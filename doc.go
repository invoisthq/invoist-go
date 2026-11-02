// Package invoist is the official Go client for the Invoist API, which
// reads invoices, receipts and bills and returns their contents as
// structured data.
//
// # Getting started
//
// Create a client with an API key, then call its methods. Every method
// takes a context.Context first; cancel it to stop a request, a retry
// wait, or a WaitForExtraction poll.
//
//	client, err := invoist.New(os.Getenv("INVOIST_API_KEY"))
//	if err != nil {
//		log.Fatal(err)
//	}
//	file, err := invoist.FileFromPath("invoice.pdf")
//	if err != nil {
//		log.Fatal(err)
//	}
//	result, err := client.ExtractAndWait(ctx, file, nil)
//
// A business workspace's methods (Extract, ListDocuments, ListReviewQueue,
// webhook endpoints, ...) are on *Client. A household's bills (Invoist
// Personal) are on Client.Bills and Client.HeldEmails:
//
//	unpaid, err := client.Bills.List(ctx, &invoist.ListBillsParams{Paid: new(false)})
//
// # Types
//
// The request and response types are generated from the API's OpenAPI
// description and re-exported here (Document, Extraction, Bill, ...).
// Field names follow Go casing, with the API's JSON names in the struct
// tags. Money is always an int64 count of minor units, such as cents,
// never a float. Dates are "YYYY-MM-DD" strings and timestamps are
// time.Time. A field the API may leave out or send as null is a pointer.
// Fields a newer API adds are ignored.
//
// # Errors
//
// Every failure the API reports, and every request that got no response,
// is an *InvoistError. Branch on its Code, or use its predicates:
//
//	var apiErr *invoist.InvoistError
//	if errors.As(err, &apiErr) && apiErr.IsNotFound() {
//		// ...
//	}
//
// A cancelled or expired context returns the context's own error instead.
//
// # Retries
//
// A request that gets no response, a 429, or a 5xx is retried up to twice
// (WithMaxRetries changes that), waiting as long as the response's
// Retry-After header says, or otherwise 0.5s, 1s, 2s ... with ±20% jitter,
// at most 8s. A Retry-After above 60s is not waited for: the call returns
// that response's error at once. Uploads are retried too: the API recognises a file it
// already has, so a retried upload never creates a second document.
//
// # Webhooks
//
// VerifyWebhook checks a delivery's X-Invoist-Signature header and returns
// the event. Pass it the raw request body, unparsed.
package invoist

//go:generate bash scripts/generate.sh
