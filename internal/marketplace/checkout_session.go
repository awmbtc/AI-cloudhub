package marketplace

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// CreateCheckoutSessionURL returns a Stripe Checkout Session URL (mode=payment).
// When AI_CLOUDHUB_STRIPE_SECRET_KEY (or STRIPE_SECRET_KEY) is unset, returns a mock URL
// so local/smoke clients can assert the field without PCI or live Stripe.
// Never accepts card data — amount + metadata only.
func CreateCheckoutSessionURL(amountCents int64, currency, productName string, metadata map[string]string) (checkoutURL, sessionID string, err error) {
	secret := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_STRIPE_SECRET_KEY"))
	if secret == "" {
		secret = strings.TrimSpace(os.Getenv("STRIPE_SECRET_KEY"))
	}
	if currency == "" {
		currency = "usd"
	}
	if productName == "" {
		productName = "AI-cloudhub marketplace item"
	}
	purchaseID := metadata["purchase_id"]
	if secret == "" {
		// Mock: deterministic, no network.
		if purchaseID == "" {
			purchaseID = "unknown"
		}
		sessionID = "cs_test_mock_" + purchaseID
		checkoutURL = "https://checkout.stripe.com/c/pay/" + sessionID
		return checkoutURL, sessionID, nil
	}

	successURL := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_STRIPE_SUCCESS_URL"))
	if successURL == "" {
		successURL = "https://example.com/marketplace/paid?session_id={CHECKOUT_SESSION_ID}"
	}
	cancelURL := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_STRIPE_CANCEL_URL"))
	if cancelURL == "" {
		cancelURL = "https://example.com/marketplace/cancel"
	}

	form := url.Values{}
	form.Set("mode", "payment")
	form.Set("success_url", successURL)
	form.Set("cancel_url", cancelURL)
	form.Set("line_items[0][quantity]", "1")
	form.Set("line_items[0][price_data][currency]", currency)
	form.Set("line_items[0][price_data][unit_amount]", fmt.Sprintf("%d", amountCents))
	form.Set("line_items[0][price_data][product_data][name]", productName)
	for k, v := range metadata {
		if k == "" || v == "" {
			continue
		}
		form.Set("metadata["+k+"]", v)
	}

	req, err := http.NewRequest(http.MethodPost, "https://api.stripe.com/v1/checkout/sessions", strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	req.SetBasicAuth(secret, "")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 20 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("stripe checkout session: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return "", "", fmt.Errorf("stripe checkout session HTTP %d: %s", res.StatusCode, truncateBody(body, 400))
	}
	var out struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", "", fmt.Errorf("stripe checkout parse: %w", err)
	}
	if out.URL == "" || out.ID == "" {
		return "", "", fmt.Errorf("stripe checkout missing url/id")
	}
	return out.URL, out.ID, nil
}

func truncateBody(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
