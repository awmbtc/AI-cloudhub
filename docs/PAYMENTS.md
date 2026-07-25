# Marketplace payments (skeleton)

## Flow

1. Publish item with `price_cents` / `currency` (or free = 0).  
2. `POST /v1/marketplace/{id}/checkout` → purchase + `stripe_metadata` + **`checkout_url`**.  
3. Redirect buyer to `checkout_url` (Stripe-hosted page — no card data on control plane).  
4. Complete payment:
   - **Dev:** `POST /v1/purchases/{purchase_id}/pay` (human token), or  
   - **Stripe:** webhook `POST /v1/webhooks/stripe` with `Stripe-Signature`.

## Checkout response (paid item)

```json
{
  "id": "<purchase_id>",
  "status": "pending",
  "provider": "stripe",
  "amount_cents": 500,
  "currency": "usd",
  "stripe_metadata": {
    "purchase_id": "…",
    "user_id": "…",
    "item_id": "…"
  },
  "checkout_url": "https://checkout.stripe.com/c/pay/cs_…",
  "session_id": "cs_…",
  "note": "…"
}
```

| Mode | When | `checkout_url` |
|------|------|----------------|
| **Mock** | no `AI_CLOUDHUB_STRIPE_SECRET_KEY` | `https://checkout.stripe.com/c/pay/cs_test_mock_<purchase_id>` |
| **Live** | secret key set | real Session from Stripe API (`mode=payment`) |

## Env

| Variable | Meaning |
|----------|---------|
| `AI_CLOUDHUB_STRIPE_WEBHOOK_SECRET` | `whsec_…` — required in prod for `/v1/webhooks/stripe` |
| `AI_CLOUDHUB_STRIPE_ALLOW_INSECURE=1` | Dev only: accept unsigned webhooks |
| `AI_CLOUDHUB_STRIPE_TOLERANCE_SEC` | Timestamp window (default 300) |
| `AI_CLOUDHUB_STRIPE_SECRET_KEY` | optional `sk_…` — creates real Checkout Session (alias: `STRIPE_SECRET_KEY`) |
| `AI_CLOUDHUB_STRIPE_SUCCESS_URL` | success redirect (default example.com; use `{CHECKOUT_SESSION_ID}`) |
| `AI_CLOUDHUB_STRIPE_CANCEL_URL` | cancel redirect |

## Webhook payload (minimal)

```json
{
  "type": "checkout.session.completed",
  "data": {
    "object": {
      "id": "cs_test_…",
      "metadata": {
        "purchase_id": "<from checkout>",
        "user_id": "<buyer user id>",
        "item_id": "<marketplace item id>"
      }
    }
  }
}
```

Also accepts `type: "ai_cloudhub.purchase.paid"` for local tests.

## Local signed test

```bash
export AI_CLOUDHUB_STRIPE_WEBHOOK_SECRET=whsec_test
# sign with marketplace.SignStripeTest (or stripe CLI)
curl -sS -X POST $API/v1/webhooks/stripe \
  -H "Stripe-Signature: t=…,v1=…" \
  -H "Content-Type: application/json" \
  -d @event.json
```

## Paid install gate

`POST /v1/marketplace/{id}/install` for **paid** items requires a purchase with `status=paid` for the same user and item. Free items (`price_cents=0` or system catalog) install without checkout.

## Limits

- No card data / Elements on control plane (PCI out of scope)  
- No full product catalog / subscriptions  
- Live Session create is optional best-effort; mock URL always works for smoke  
