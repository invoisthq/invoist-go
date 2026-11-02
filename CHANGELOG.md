# Changelog

All notable changes to this library are listed here. It follows
[Semantic Versioning](https://semver.org/).

## 1.0.1 - 2026-11-02

- No change to the API or to how the library behaves.
- The repository now holds its own copy of the API's OpenAPI spec in
  `testdata/openapi.yaml`. The models are generated from it, and the tests
  that compare the models with the spec run in this repository too.
- Test data uses made-up company names.

## v1.0.0

The first stable release.

- The module is `github.com/invoisthq/invoist-go`.
- Business workspaces: `Extract`, `ExtractBatch`, `ExtractAndWait`,
  `WaitForExtraction`, `ListDocuments`, `GetDocument`, `GetDocumentFile`,
  `ListReviewQueue`, `ResolveReviewItem`, `ListCategories`, `GetUsage`,
  `GetStats`, and webhook endpoints (`ListWebhookEndpoints`,
  `CreateWebhookEndpoint`, `DeleteWebhookEndpoint`).
- Households: `client.Bills` (`List`, `Get`, `GetFile`, `Summary`, `Stats`,
  `MarkPaid`, `MarkUnpaid`) and `client.HeldEmails.List`.
- One error type, `*InvoistError`, with `Status`, `Code`, `Message`,
  `RequestID` and `Body`, and the predicates `IsAuthError`,
  `IsPermissionError`, `IsNotFound`, `IsRateLimited` and `IsRetryable`.
- Retries of failed connections, 429s and 5xx responses, honouring
  `Retry-After` up to 60 seconds (a longer one fails the call at once),
  with jittered backoff capped at 8 seconds.
- `VerifyWebhook` checks `X-Invoist-Signature`, accepts any of several
  signatures during a secret rotation, and rejects timestamps more than five
  minutes off in either direction. Event types are constants
  (`EventExtractionCompleted`, ...). A signed body that is not JSON fails
  with `invalid_payload`.
- Options `WithBaseURL`, `WithHTTPClient`, `WithTimeout` and `WithMaxRetries`.
- Types generated from the API's OpenAPI description with oapi-codegen.
  Money is `int64` minor units. No dependencies outside the standard library.
- Requires Go 1.26 or newer.
