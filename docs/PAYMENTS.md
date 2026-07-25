# Marketplace payments (skeleton)

## Flow

1. Publish item with `price_cents` / `currency` (or free = 0).  
2. `POST /v1/marketplace/{id}/checkout` → purchase + `stripe_metadata` for Checkout Session.  
3. Complete payment:
   - **Dev:** `POST /v1/purchases/{purchase_id}/pay` (human token), or  
   - **Stripe:** webhook `POST /v1/webhooks/stripe` with `Stripe-Signature`.

## Env

| Variable | Meaning |
|----------|---------|
| `AI_CLOUDHUB_STRIPE_WEBHOOK_SECRET` | `whsec_…` — required in prod for `/v1/webhooks/stripe` |
| `AI_CLOUDHUB_STRIPE_ALLOW_INSECURE=1` | Dev only: accept unsigned webhooks |
| `AI_CLOUDHUB_STRIPE_TOLERANCE_SEC` | Timestamp window (default 300) |

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

## Limits

- No hosted Checkout UI in control plane  
- No PCI card data handling  
- Install of paid templates is not auto-gated on `paid` status yet (hook point for later)  
