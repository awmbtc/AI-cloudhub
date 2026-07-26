package job

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/store"
)

func TestSQLiteWebhookRetryRedeliver(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	t.Setenv("AI_CLOUDHUB_JOB_WEBHOOK_URL", srv.URL)
	t.Setenv("AI_CLOUDHUB_JOB_WEBHOOK_BACKOFF_SEC", "0")
	t.Setenv("AI_CLOUDHUB_JOB_WEBHOOK_MAX_ATTEMPTS", "5")

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(st)
	uid := "u1"
	j, _, err := svc.Create(uid, CreateInput{DriveID: "d", Command: []string{"t"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim(uid, j.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Complete(uid, j.ID, CompleteInput{OK: true}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = svc.ProcessWebhookOutbox(8)
		if hits.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if hits.Load() < 1 {
		t.Fatalf("first deliver hits=%d", hits.Load())
	}
	list := svc.AdminListWebhooks(AdminWebhookFilter{Status: "delivered", Limit: 10})
	if len(list) != 1 {
		t.Fatalf("delivered %d", len(list))
	}
	got, err := svc.AdminRetryWebhook(list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "pending" {
		t.Fatalf("status %s", got.Status)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = svc.ProcessWebhookOutbox(8)
		v, _ := svc.AdminGetWebhook(list[0].ID)
		if v != nil && v.Status == "delivered" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	v, err := svc.AdminGetWebhook(list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "delivered" {
		t.Fatalf("after retry status=%s attempts=%d last=%q hits=%d", v.Status, v.Attempts, v.LastError, hits.Load())
	}
	if hits.Load() < 2 {
		t.Fatalf("hits %d", hits.Load())
	}
}
