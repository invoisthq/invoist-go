# Invoist Go library

[![Go Reference](https://pkg.go.dev/badge/github.com/invoisthq/invoist-go.svg)](https://pkg.go.dev/github.com/invoisthq/invoist-go)

The official Go client for the [Invoist API](https://invoi.st/docs). Invoist
reads invoices, receipts and bills and gives you back what is on them as
structured data: the sender, the amounts, the due date, every line, and
anything that looks worth a second look.

It needs Go 1.26 or newer and has no dependencies outside the standard
library.

## Install

```sh
go get github.com/invoisthq/invoist-go
```

```go
import invoist "github.com/invoisthq/invoist-go"
```

## Get a key

A business workspace creates API keys in the dashboard under Integrations.
A household creates them under Settings, AI assistants. A key is shown only
once, so keep it in your secret store. It is a server-side secret: never put
it in a mobile app or a web page.

## A business: read an invoice

```go
client, err := invoist.New(os.Getenv("INVOIST_API_KEY"))
if err != nil {
	log.Fatal(err)
}
file, err := invoist.FileFromPath("invoice.pdf")
if err != nil {
	log.Fatal(err)
}
result, err := client.ExtractAndWait(ctx, file, nil)
if err != nil {
	log.Fatal(err)
}
fmt.Println(*result.Extraction.VendorName, *result.Extraction.TotalCents, *result.Extraction.Currency)
```

`Extract` uploads a PDF, PNG or JPEG and returns at once, while the reading
happens in the background. `ExtractAndWait` also waits for the result, and
`WaitForExtraction` waits for a document you uploaded earlier. A reading
that failed is returned, not raised: check `Extraction.Status` and
`Extraction.ErrorMessage`.

Uploading a file the workspace already has returns the existing document
with `Duplicate` set, and costs nothing. For a file that is the same
document but not the same bytes, pass an idempotency key:
`client.Extract(ctx, file, &invoist.ExtractParams{IdempotencyKey: "order-1042"})`.

The other business methods are `ExtractBatch`, `ListDocuments`,
`GetDocument`, `GetDocumentFile`, `ListReviewQueue`, `ResolveReviewItem`,
`ListCategories`, `GetUsage`, `GetStats`, and `ListWebhookEndpoints`,
`CreateWebhookEndpoint` and `DeleteWebhookEndpoint`.

## A household: what is left to pay

```go
client, err := invoist.New(os.Getenv("INVOIST_API_KEY"))
if err != nil {
	log.Fatal(err)
}
bills, err := client.Bills.List(ctx, &invoist.ListBillsParams{Paid: new(false)})
if err != nil {
	log.Fatal(err)
}
for _, b := range bills {
	fmt.Println(*b.VendorName, *b.DueDate, *b.TotalCents, *b.Currency)
}
```

A household's methods are grouped on `client.Bills`: `List`, `Get`,
`GetFile`, `Summary`, `Stats`, `MarkPaid` and `MarkUnpaid`. Emails that wait
for someone to approve their sender are on `client.HeldEmails.List`.
Marking a bill paid needs a key the household allowed to do that (the
`bills:write` scope).

Months are `"YYYY-MM"` strings; `invoist.MonthOf(time.Now())` makes one,
in your local time zone (`invoist.DateOf` does the same for dates).

## What you get back

The types are generated from the API's OpenAPI description, so they match
what the server sends. Fields use Go names, with the API's JSON names in
their tags (`TotalCents` is `total_cents`).

- Money is an `int64` count of minor units: `TotalCents: 48000` with
  `Currency: "EUR"` is €480.00. There are no floats, so nothing is rounded.
- Dates are `"YYYY-MM-DD"` strings. Timestamps are `time.Time`.
- A field the API can leave out or send as `null` is a pointer.
- Fields a newer API adds are ignored, so an API update never breaks your
  build or your decoding.

Every method takes a `context.Context` first. Cancel it to stop a request,
a wait between retries, or a `WaitForExtraction` loop.

## Errors

Every failed call returns an `*invoist.InvoistError`:

```go
_, err := client.GetDocument(ctx, id)
var apiErr *invoist.InvoistError
if errors.As(err, &apiErr) {
	switch {
	case apiErr.IsNotFound():
		// there is no such document
	case apiErr.IsAuthError():
		// the key is wrong or revoked
	case apiErr.IsPermissionError():
		// the key may not do this
	default:
		log.Printf("%s: %s (request %s)", apiErr.Code, apiErr.Message, apiErr.RequestID)
	}
}
```

| Field | Meaning |
|---|---|
| `Status` | The HTTP status, or 0 when no response arrived |
| `Code` | A stable code such as `not_found` or `rate_limited`. Branch on this |
| `Message` | Text for people. It can change, so don't branch on it |
| `RequestID` | The `X-Request-Id` header. Quote it when you contact support |
| `Body` | The raw response body |

The library sets its own codes when the API didn't send one:
`unexpected_response` (the body wasn't an API error, such as a proxy's
error page), `network_error` and `timeout` (no response arrived), and
`extraction_timeout` (`WaitForExtraction` gave up).

A cancelled or expired context returns the context's own error
(`context.Canceled` or `context.DeadlineExceeded`), not an `InvoistError`.

## Retries and timeouts

A request that gets no response, a 429 or a 5xx is tried again, up to twice.
The client waits as long as the response's `Retry-After` header says, or
otherwise 0.5s, then 1s, then 2s and so on, varied by up to 20% and never
more than 8s. If `Retry-After` asks for more than 60 seconds, the client
doesn't wait: the call returns that error straight away. Other errors are
not retried. Uploads are retried too: the API
recognises a file it already has, so a retried upload never makes a second
document.

```go
client, err := invoist.New(key,
	invoist.WithMaxRetries(4),              // default 2; 0 turns retries off
	invoist.WithTimeout(30*time.Second),    // per attempt; default 60s
	invoist.WithBaseURL("http://localhost:8080"),
	invoist.WithHTTPClient(&http.Client{Transport: myTransport}),
)
```

## Webhooks

Invoist can call your server when something happens, such as a document
being read (`invoist.EventExtractionCompleted`). Register a URL with
`CreateWebhookEndpoint` and keep the `Secret` it returns: it is shown only
once.

Check every delivery with `VerifyWebhook`. Pass it the **raw request body**,
exactly as received. Decoding the JSON and encoding it again changes the
bytes, and the signature will no longer match.

```go
http.HandleFunc("/webhooks/invoist", func(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	event, err := invoist.VerifyWebhook(payload, r.Header.Get(invoist.SignatureHeader), secret)
	if err != nil {
		http.Error(w, "bad signature", http.StatusBadRequest)
		return
	}
	if event.Type == invoist.EventExtractionCompleted {
		var data struct {
			DocumentID string `json:"document_id"`
		}
		_ = event.DecodeData(&data)
		// fetch it with client.GetDocument(r.Context(), data.DocumentID)
	}
	w.WriteHeader(http.StatusNoContent)
})
```

`VerifyWebhook` rejects a delivery whose timestamp is more than five minutes
from your clock, either way (`invoist.WithTolerance` changes that). It
accepts any of several signatures, which is what you receive while a secret
is being replaced. A delivery that fails returns an `InvoistError` with
status 400 and the code `invalid_signature` or `signature_too_old`; answer
it with a 400.

Each delivery also carries `X-Invoist-Event` (the event type) and
`X-Invoist-Delivery` (an id that stays the same when a delivery is retried,
so you can skip repeats).

## Development

```sh
bash scripts/generate.sh   # regenerate the types from openapi.yaml
go vet ./... && go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...
go test ./...
```

`scripts/generate.sh` reads `testdata/openapi.yaml`, the API's spec, and rewrites `internal/generated/types.go` and
`types_gen.go`. Both are committed; running it again must change nothing.
The tests run the shared conformance vectors in `testdata/conformance`.
Live tests run only when `INVOIST_API_KEY` is set (and `INVOIST_BASE_URL`,
to use another server); they only read.

## Links

- [API documentation](https://invoi.st/docs)
- [Package reference](https://pkg.go.dev/github.com/invoisthq/invoist-go)
- [Changelog](CHANGELOG.md)
- [Security policy](SECURITY.md)

MIT licensed. See [LICENSE](LICENSE).
