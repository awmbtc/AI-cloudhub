package marketplace

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// VerifyStripeSignature validates Stripe-Signature header (t=…,v1=…).
// Secret from AI_CLOUDHUB_STRIPE_WEBHOOK_SECRET. Tolerance default 300s.
func VerifyStripeSignature(payload []byte, sigHeader, secret string, now time.Time) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return fmt.Errorf("webhook secret not configured")
	}
	if sigHeader == "" {
		return fmt.Errorf("missing Stripe-Signature")
	}
	var ts int64
	var v1 string
	for _, part := range strings.Split(sigHeader, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "t=") {
			n, err := strconv.ParseInt(strings.TrimPrefix(part, "t="), 10, 64)
			if err != nil {
				return fmt.Errorf("bad t in signature")
			}
			ts = n
		}
		if strings.HasPrefix(part, "v1=") {
			v1 = strings.TrimPrefix(part, "v1=")
		}
	}
	if ts == 0 || v1 == "" {
		return fmt.Errorf("incomplete Stripe-Signature")
	}
	tol := 300
	if v := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_STRIPE_TOLERANCE_SEC")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tol = n
		}
	}
	if now.IsZero() {
		now = time.Now()
	}
	if d := now.Unix() - ts; d > int64(tol) || d < -int64(tol) {
		return fmt.Errorf("timestamp outside tolerance")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(fmt.Sprintf("%d.", ts)))
	_, _ = mac.Write(payload)
	expect := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expect), []byte(v1)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

// StripeCheckoutCompleted is a minimal Stripe event payload we accept.
type StripeCheckoutCompleted struct {
	Type string `json:"type"`
	Data struct {
		Object struct {
			ID       string            `json:"id"`
			Metadata map[string]string `json:"metadata"`
		} `json:"object"`
	} `json:"data"`
}

// ParseStripeCheckoutCompleted extracts purchase_id and user_id from a checkout.session.completed event.
func ParseStripeCheckoutCompleted(payload []byte) (purchaseID, userID, sessionID string, err error) {
	var ev StripeCheckoutCompleted
	if err := json.Unmarshal(payload, &ev); err != nil {
		return "", "", "", fmt.Errorf("json: %w", err)
	}
	if ev.Type != "" && ev.Type != "checkout.session.completed" && ev.Type != "payment_intent.succeeded" {
		// allow empty type for stub tests
		if ev.Type != "ai_cloudhub.purchase.paid" {
			return "", "", "", fmt.Errorf("unsupported event type %q", ev.Type)
		}
	}
	md := ev.Data.Object.Metadata
	if md == nil {
		return "", "", "", fmt.Errorf("missing metadata")
	}
	purchaseID = strings.TrimSpace(md["purchase_id"])
	userID = strings.TrimSpace(md["user_id"])
	if purchaseID == "" || userID == "" {
		return "", "", "", fmt.Errorf("metadata.purchase_id and metadata.user_id required")
	}
	return purchaseID, userID, ev.Data.Object.ID, nil
}

// SignStripeTest produces a valid Stripe-Signature for tests (and local curl demos).
func SignStripeTest(payload []byte, secret string, ts time.Time) string {
	if ts.IsZero() {
		ts = time.Now()
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(fmt.Sprintf("%d.", ts.Unix())))
	_, _ = mac.Write(payload)
	return fmt.Sprintf("t=%d,v1=%s", ts.Unix(), hex.EncodeToString(mac.Sum(nil)))
}
