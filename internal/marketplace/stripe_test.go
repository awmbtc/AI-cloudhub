package marketplace

import (
	"testing"
	"time"
)

func TestStripeSignatureRoundTrip(t *testing.T) {
	secret := "whsec_test"
	payload := []byte(`{"type":"checkout.session.completed","data":{"object":{"id":"cs_1","metadata":{"purchase_id":"p1","user_id":"u1"}}}}`)
	ts := time.Now()
	sig := SignStripeTest(payload, secret, ts)
	if err := VerifyStripeSignature(payload, sig, secret, ts); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStripeSignature(payload, sig, secret, ts.Add(10*time.Minute)); err == nil {
		t.Fatal("expected tolerance fail")
	}
	pid, uid, sid, err := ParseStripeCheckoutCompleted(payload)
	if err != nil || pid != "p1" || uid != "u1" || sid != "cs_1" {
		t.Fatalf("%v %s %s %s", err, pid, uid, sid)
	}
}
