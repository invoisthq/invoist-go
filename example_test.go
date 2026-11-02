package invoist_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	invoist "github.com/invoisthq/invoist-go"
)

// A business workspace: upload an invoice and wait for what was read.
func Example() {
	client, err := invoist.New(os.Getenv("INVOIST_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	file, err := invoist.FileFromPath("invoice.pdf")
	if err != nil {
		log.Fatal(err)
	}
	result, err := client.ExtractAndWait(context.Background(), file, nil)
	if err != nil {
		log.Fatal(err)
	}
	x := result.Extraction
	if x.Status == invoist.ExtractionStatusDone && x.TotalCents != nil {
		fmt.Printf("%s: %d.%02d %s\n", deref(x.VendorName), *x.TotalCents/100, *x.TotalCents%100, deref(x.Currency))
	}
}

// A household: list the bills still to pay this month.
func ExampleBillsService_List() {
	client, err := invoist.New(os.Getenv("INVOIST_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	bills, err := client.Bills.List(context.Background(), &invoist.ListBillsParams{
		Paid:  new(false),
		Month: invoist.MonthOf(time.Now()),
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, b := range bills {
		fmt.Println(deref(b.VendorName), deref(b.DueDate), *b.TotalCents, deref(b.Currency))
	}
}

// Mark a bill paid once it is paid.
func ExampleBillsService_MarkPaid() {
	client, err := invoist.New(os.Getenv("INVOIST_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	record, err := client.Bills.MarkPaid(context.Background(), "01a19812-0000-7000-8000-0000000000aa")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("paid at", record.PaidAt)
}

// Branch on what went wrong with errors.As.
func ExampleInvoistError() {
	client, err := invoist.New(os.Getenv("INVOIST_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	_, err = client.GetDocument(context.Background(), "01a19812-0000-7000-8000-0000000000aa")
	var apiErr *invoist.InvoistError
	switch {
	case err == nil:
		fmt.Println("found it")
	case errors.As(err, &apiErr) && apiErr.IsNotFound():
		fmt.Println("no such document")
	case errors.As(err, &apiErr) && apiErr.IsAuthError():
		fmt.Println("check INVOIST_API_KEY")
	case errors.As(err, &apiErr):
		fmt.Println(apiErr.Code, apiErr.Message, apiErr.RequestID)
	default:
		fmt.Println(err) // the context was cancelled
	}
}

// Receive webhooks with net/http. Read the raw body before anything parses it.
func ExampleVerifyWebhook() {
	secret := os.Getenv("INVOIST_WEBHOOK_SECRET")
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
		switch event.Type {
		case invoist.EventExtractionCompleted:
			var data struct {
				DocumentID string `json:"document_id"`
			}
			if err := event.DecodeData(&data); err == nil {
				log.Println("read", data.DocumentID)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// VerifyWebhook returns the event when the signature matches.
func ExampleVerifyWebhook_signature() {
	secret := "whsec_0123456789abcdef0123456789abcdef"
	body := []byte(`{"id":"e1","type":"document.created","created_at":"2026-11-01T08:00:00Z","data":{}}`)
	signedAt := time.Date(2026, time.November, 1, 8, 0, 0, 0, time.UTC)

	// What Invoist sends in X-Invoist-Signature.
	ts := strconv.FormatInt(signedAt.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	header := "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))

	event, err := invoist.VerifyWebhook(body, header, secret, invoist.WithNow(signedAt.Add(time.Minute)))
	fmt.Println(event.Type, err)

	_, err = invoist.VerifyWebhook([]byte(`{"tampered":true}`), header, secret, invoist.WithNow(signedAt))
	var apiErr *invoist.InvoistError
	if errors.As(err, &apiErr) {
		fmt.Println(apiErr.Status, apiErr.Code)
	}
	// Output:
	// document.created <nil>
	// 400 invalid_signature
}

// Point the client elsewhere and tune its retries.
func ExampleNew() {
	client, err := invoist.New("inv_sk_...",
		invoist.WithBaseURL("http://localhost:8080"),
		invoist.WithTimeout(30*time.Second),
		invoist.WithMaxRetries(4),
		invoist.WithHTTPClient(&http.Client{Transport: http.DefaultTransport}),
	)
	fmt.Println(client != nil, err)

	_, err = invoist.New("")
	fmt.Println(errors.Is(err, invoist.ErrMissingAPIKey))
	// Output:
	// true <nil>
	// true
}

// MonthOf formats a time for the month filters.
func ExampleMonthOf() {
	fmt.Println(invoist.MonthOf(time.Date(2026, time.October, 31, 0, 0, 0, 0, time.UTC)))
	// Output: 2026-10
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
