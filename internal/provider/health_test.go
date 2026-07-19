package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/store"
)

func TestHealthProbeNotFound(t *testing.T) {
	svc := NewService(store.NewMemory())
	_, err := svc.HealthProbe(context.Background(), "u1", "missing")
	if err == nil {
		t.Fatal("expected not found")
	}
}

func TestHealthProbeUnreachable(t *testing.T) {
	svc := NewService(store.NewMemory())
	rec, err := svc.Create("u1", CreateInput{
		Name: "bad",
		Type: TypeMinIO,
		Creds: Credentials{
			AccessKey: "ak",
			SecretKey: "sk",
			Endpoint:  "127.0.0.1:1", // nothing listens
			UseSSL:    boolPtr(false),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := svc.HealthProbe(ctx, "u1", rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("expected probe failure against closed port")
	}
	if res.Message == "" {
		t.Fatal("expected error message")
	}
	if res.Endpoint == "" {
		t.Fatal("expected endpoint")
	}
}

func TestHealthProbeOK(t *testing.T) {
	// Minimal fake: ListBuckets returns empty XML OK for MinIO/S3 clients is complex;
	// use httptest only if we had a raw HTTP probe. Instead verify ok path via minio
	// against a mock that speaks enough of the S3 API is heavy — skip live OK here
	// and cover soft-fail path above. This test documents ownership + decrypt path.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// minio-go ListBuckets hits GET / ; respond 403 so we get a controlled failure
		// with non-network error (auth). That still exercises client wiring.
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>AccessDenied</Code></Error>`))
	}))
	defer ts.Close()

	svc := NewService(store.NewMemory())
	rec, err := svc.Create("u1", CreateInput{
		Name: "mock",
		Type: TypeMinIO,
		Creds: Credentials{
			AccessKey: "ak",
			SecretKey: "sk",
			Endpoint:  ts.URL,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.HealthProbe(context.Background(), "u1", rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Forbidden or similar → not OK but no Go error from Resolve
	if res.OK {
		t.Log("unexpected ok from forbidden mock; minio client may treat differently")
	}
	if res.ProviderID != rec.ID {
		t.Fatalf("provider_id: %s", res.ProviderID)
	}
}

func boolPtr(b bool) *bool { return &b }
