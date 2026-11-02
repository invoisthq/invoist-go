package invoist

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"
	"time"
)

// Cases beyond the shared vectors in testdata/conformance/webhooks.json.

func sign(t int64, body, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(t, 10) + "." + body))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyWebhookEdgeCases(t *testing.T) {
	const secret = "whsec_test"
	now := time.Unix(1_793_520_000, 0)
	body := `{"id":"e1","type":"review_item.created","created_at":"2026-11-01T08:00:00Z","data":{"id":"r1"}}`
	good := sign(now.Unix(), body, secret)
	ts := strconv.FormatInt(now.Unix(), 10)

	event, err := VerifyWebhook([]byte(body), "t="+ts+",v1="+good, secret, WithNow(now))
	if err != nil || event.Type != EventReviewItemCreated || event.ID != "e1" {
		t.Fatalf("event = %+v, err = %v", event, err)
	}
	// The default tolerance is five minutes, and uses the real clock.
	if _, err := VerifyWebhook([]byte(body), "t="+ts+",v1="+good, secret, WithNow(now.Add(DefaultWebhookTolerance+time.Second))); err == nil {
		t.Error("default tolerance not applied")
	}
	if _, err := VerifyWebhook([]byte(body), "t="+ts+",v1="+good, secret); err == nil {
		t.Error("a 2026 signature verified against the real clock without a tolerance window")
	}

	for name, header := range map[string]string{
		"two timestamps":   "t=" + ts + ",t=" + ts + ",v1=" + good,
		"negative t":       "t=-" + ts + ",v1=" + good,
		"empty v1":         "t=" + ts + ",v1=",
		"absurd t":         "t=99999999999999999999,v1=" + good,
		"v1 without value": "t=" + ts + ",v1",
	} {
		_, err := VerifyWebhook([]byte(body), header, secret, WithNow(now))
		var e *InvoistError
		if !errors.As(err, &e) || e.Code != CodeInvalidSignature || e.Status != 400 {
			t.Errorf("%s: err = %v", name, err)
		}
	}

	// A correctly signed body that is not JSON.
	notJSON := "not json"
	_, err = VerifyWebhook([]byte(notJSON), "t="+ts+",v1="+sign(now.Unix(), notJSON, secret), secret, WithNow(now))
	var e *InvoistError
	if !errors.As(err, &e) || e.Code != CodeInvalidPayload || e.Status != 400 || e.Err == nil {
		t.Errorf("not JSON: err = %v", err)
	}

	// A wrong signature is reported as such even when also stale.
	_, err = VerifyWebhook([]byte(body), "t="+ts+",v1="+sign(now.Unix(), body, "other"), secret, WithNow(now.Add(time.Hour)))
	if !errors.As(err, &e) || e.Code != CodeInvalidSignature {
		t.Errorf("forged and stale: err = %v", err)
	}
}
