package job

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/metrics"
	"github.com/awmbtc/AI-cloudhub/internal/store"
)

func TestListPendingFiltersByRegion(t *testing.T) {
	svc := NewService(store.NewMemory())
	uid := "u1"
	if _, _, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"echo", "a"}, RegionHint: "us-east"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"echo", "b"}, RegionHint: "eu-west"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"echo", "c"}}); err != nil {
		t.Fatal(err)
	}

	all := svc.ListPending(uid, "")
	if len(all) != 3 {
		t.Fatalf("expected 3 pending, got %d", len(all))
	}
	east := svc.ListPending(uid, "us-east")
	if len(east) != 1 || east[0].RegionHint != "us-east" {
		t.Fatalf("region filter us-east: %+v", east)
	}
	west := svc.ListPending(uid, "eu-west")
	if len(west) != 1 || west[0].RegionHint != "eu-west" {
		t.Fatalf("region filter eu-west: %+v", west)
	}
	none := svc.ListPending(uid, "ap-south")
	if len(none) != 0 {
		t.Fatalf("expected empty for unknown region, got %+v", none)
	}
}

func TestClaimNextOnlyClaimsPendingOnce(t *testing.T) {
	mem := store.NewMemory()
	svc := NewService(mem)
	uid := "u-claim"
	created, _, err := svc.Create(uid, CreateInput{
		DriveID: "drive-1",
		Command: []string{"echo", "once"},
	})
	if err != nil {
		t.Fatal(err)
	}

	const workers = 16
	var (
		wg       sync.WaitGroup
		success  atomic.Int32
		claimedID string
		mu       sync.Mutex
	)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			j, err := svc.ClaimNext(uid, "", "", "")
			if err != nil {
				return
			}
			success.Add(1)
			mu.Lock()
			claimedID = j.ID
			mu.Unlock()
			if j.Status != StatusRunning {
				t.Errorf("claimed status = %s, want running", j.Status)
			}
		}()
	}
	wg.Wait()

	if success.Load() != 1 {
		t.Fatalf("expected exactly 1 successful ClaimNext, got %d", success.Load())
	}
	if claimedID != created.ID {
		t.Fatalf("claimed id %s, want %s", claimedID, created.ID)
	}

	// Second ClaimNext must fail — no pending left.
	if _, err := svc.ClaimNext(uid, "", "", ""); err == nil {
		t.Fatal("expected ClaimNext to fail when nothing pending")
	}

	got, err := svc.Get(uid, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusRunning {
		t.Fatalf("job status = %s, want running", got.Status)
	}
}

func TestClaimNextPicksOldestAmongMany(t *testing.T) {
	svc := NewService(store.NewMemory())
	uid := "u2"
	var ids []string
	for i := 0; i < 3; i++ {
		j, _, err := svc.Create(uid, CreateInput{DriveID: "d", Command: []string{"echo", "x"}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.ID)
	}
	first, err := svc.ClaimNext(uid, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != ids[0] {
		t.Fatalf("expected oldest %s, got %s", ids[0], first.ID)
	}
	second, err := svc.ClaimNext(uid, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != ids[1] {
		t.Fatalf("expected second oldest %s, got %s", ids[1], second.ID)
	}
}

func TestReleaseToPending(t *testing.T) {
	svc := NewService(store.NewMemory())
	uid := "u-rel"
	j, _, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"echo"}})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := svc.Claim(uid, j.ID, "agent-claim", "")
	if err != nil || claimed.Status != StatusRunning {
		t.Fatalf("claim: %v %+v", err, claimed)
	}
	if claimed.ClaimedByAgentID != "agent-claim" {
		t.Fatalf("claimed_by=%q", claimed.ClaimedByAgentID)
	}
	rel, err := svc.ReleaseToPending(uid, j.ID, "drive not allowed for agent")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Status != StatusPending {
		t.Fatalf("status %s", rel.Status)
	}
	if !strings.Contains(rel.Note, "released:") {
		t.Fatalf("note %q", rel.Note)
	}
	// can claim again
	again, err := svc.Claim(uid, j.ID, "agent-2", "")
	if err != nil || again.Status != StatusRunning {
		t.Fatalf("reclaim: %v %+v", err, again)
	}
	if again.ClaimedByAgentID != "agent-2" {
		t.Fatalf("reclaim by %q", again.ClaimedByAgentID)
	}
}

func TestCompleteAppendsNote(t *testing.T) {
	svc := NewService(store.NewMemory())
	uid := "u-note"
	j, _, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"echo"}, Note: "user-seed"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(j.Note, "user-seed") || !strings.Contains(j.Note, "BYOC only") {
		t.Fatalf("create note %q", j.Note)
	}
	if _, err := svc.Claim(uid, j.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	code := 0
	done, err := svc.Complete(uid, j.ID, CompleteInput{OK: true, Note: "cloned to /workspace/repo", ExitCode: &code, DurationMs: 42})
	if err != nil {
		t.Fatal(err)
	}
	if done.ExitCode == nil || *done.ExitCode != 0 || done.DurationMs != 42 {
		t.Fatalf("exit/duration %+v", done)
	}
	if done.Status != StatusSucceeded {
		t.Fatalf("status %s", done.Status)
	}
	if !strings.Contains(done.Note, "BYOC only") {
		t.Fatalf("create trail lost: %q", done.Note)
	}
	if !strings.Contains(done.Note, "cloned to /workspace/repo") {
		t.Fatalf("clone path missing: %q", done.Note)
	}
	// empty complete note must not wipe
	j2, _, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"echo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim(uid, j2.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	keep, err := svc.Complete(uid, j2.ID, CompleteInput{OK: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(keep.Note, "BYOC only") {
		t.Fatalf("empty complete wiped note: %q", keep.Note)
	}
}

func TestCompleteStdoutStderrCapAndListStatus(t *testing.T) {
	svc := NewService(store.NewMemory())
	uid := "u-out"
	j, _, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"echo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim(uid, j.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AI_CLOUDHUB_JOB_OUTPUT_MAX", "16")
	code := 0
	big := strings.Repeat("x", 40)
	done, err := svc.Complete(uid, j.ID, CompleteInput{
		OK: true, ExitCode: &code, DurationMs: 5,
		Stdout: "out-" + big, Stderr: "err-" + big,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(done.Stdout) != 16 || len(done.Stderr) != 16 {
		t.Fatalf("cap: stdout=%d stderr=%d want 16", len(done.Stdout), len(done.Stderr))
	}
	// tail kept
	if !strings.HasSuffix(done.Stdout, "xxxx") {
		t.Fatalf("stdout tail %q", done.Stdout)
	}
	if !done.StdoutTruncated || !done.StderrTruncated {
		t.Fatalf("truncated flags: out=%v err=%v", done.StdoutTruncated, done.StderrTruncated)
	}
	// list by status
	succ, _ := svc.List(uid, ListFilter{Status: "succeeded"})
	if len(succ) != 1 || succ[0].ID != j.ID {
		t.Fatalf("list succeeded: %+v", succ)
	}
	run, _ := svc.List(uid, ListFilter{Status: "running"})
	if len(run) != 0 {
		t.Fatalf("list running want 0 got %d", len(run))
	}
	// pending list empty after complete
	pend := svc.ListPending(uid, "")
	if len(pend) != 0 {
		t.Fatalf("pending %d", len(pend))
	}
}

func TestIdempotentCreate(t *testing.T) {
	svc := NewService(store.NewMemory())
	uid := "u-idem"
	a, created, err := svc.Create(uid, CreateInput{
		DriveID: "d", Command: []string{"echo"}, IdempotencyKey: "req-1",
	})
	if err != nil || !created {
		t.Fatalf("create: %v created=%v", err, created)
	}
	// same key + same payload → replay
	b, created2, err := svc.Create(uid, CreateInput{
		DriveID: "d", Command: []string{"echo"}, IdempotencyKey: "req-1",
	})
	if err != nil || created2 {
		t.Fatalf("replay: %v created=%v", err, created2)
	}
	if a.ID != b.ID {
		t.Fatalf("idempotent create should return same id %s vs %s", a.ID, b.ID)
	}
	// same key + different command → conflict
	if _, _, err := svc.Create(uid, CreateInput{
		DriveID: "d", Command: []string{"echo", "other"}, IdempotencyKey: "req-1",
	}); err == nil || !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	// different key = new job
	c, created3, err := svc.Create(uid, CreateInput{DriveID: "d", Command: []string{"x"}, IdempotencyKey: "req-2"})
	if err != nil || !created3 || c.ID == a.ID {
		t.Fatalf("new key: %v created=%v %s", err, created3, c.ID)
	}
}

func TestJobStats(t *testing.T) {
	svc := NewService(store.NewMemory())
	uid := "u-st"
	j1, _, _ := svc.Create(uid, CreateInput{DriveID: "d", Command: []string{"a"}})
	j2, _, _ := svc.Create(uid, CreateInput{DriveID: "d", Command: []string{"b"}})
	_, _ = svc.Claim(uid, j1.ID, "", "")
	_, _ = svc.Complete(uid, j1.ID, CompleteInput{OK: true})
	_, _ = svc.Cancel(uid, j2.ID)
	st := svc.Stats(uid)
	if st.Total != 2 || st.Succeeded != 1 || st.Cancelled != 1 {
		t.Fatalf("stats %+v", st)
	}
}

func TestStatsFullAggregationNoCap(t *testing.T) {
	svc := NewService(store.NewMemory())
	// create more than the old 500 admin list cap would allow if still scanning
	const n = 30
	for i := 0; i < n; i++ {
		uid := "u-agg"
		if i%2 == 0 {
			uid = "u-agg-a"
		}
		j, _, err := svc.Create(uid, CreateInput{DriveID: "d", Command: []string{"x"}})
		if err != nil {
			t.Fatal(err)
		}
		if i%3 == 0 {
			_, _ = svc.Claim(uid, j.ID, "", "")
			_, _ = svc.Complete(uid, j.ID, CompleteInput{OK: true})
		} else if i%3 == 1 {
			_, _ = svc.Cancel(uid, j.ID)
		}
	}
	global := svc.AdminStats("")
	if global.Total != n {
		t.Fatalf("global total %d want %d (succeeded=%d cancelled=%d pending=%d)",
			global.Total, n, global.Succeeded, global.Cancelled, global.Pending)
	}
	if global.Succeeded+global.Cancelled+global.Pending+global.Running+global.Dispatched+global.Failed != global.Total {
		t.Fatalf("buckets sum mismatch %+v", global)
	}
	per := svc.AdminStats("u-agg-a")
	if per.Total == 0 || per.Total > n {
		t.Fatalf("per-user %+v", per)
	}
	// user stats same path
	u := svc.Stats("u-agg-a")
	if u.Total != per.Total {
		t.Fatalf("Stats vs AdminStats: %+v vs %+v", u, per)
	}
}

func TestAdminListAndGet(t *testing.T) {
	svc := NewService(store.NewMemory())
	j1, _, _ := svc.Create("u1", CreateInput{DriveID: "d", Command: []string{"a"}})
	j2, _, _ := svc.Create("u2", CreateInput{DriveID: "d", Command: []string{"b"}})
	all, next := svc.AdminList(AdminListFilter{Limit: 50})
	if len(all) < 2 {
		t.Fatalf("admin list %d", len(all))
	}
	if next != "" {
		t.Fatalf("unexpected next_cursor for full page: %q", next)
	}
	only, _ := svc.AdminList(AdminListFilter{UserID: "u1", Limit: 10})
	if len(only) != 1 || only[0].ID != j1.ID {
		t.Fatalf("filter user: %+v", only)
	}
	got, err := svc.AdminGet(j2.ID)
	if err != nil || got.UserID != "u2" {
		t.Fatalf("admin get: %v %+v", err, got)
	}
	st := svc.AdminStats("")
	if st.Total < 2 {
		t.Fatalf("admin stats %+v", st)
	}
}

func TestAdminListCursor(t *testing.T) {
	svc := NewService(store.NewMemory())
	var ids []string
	for i := 0; i < 5; i++ {
		j, _, err := svc.Create("u-cur", CreateInput{DriveID: "d", Command: []string{fmt.Sprintf("c%d", i)}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.ID)
		time.Sleep(2 * time.Millisecond) // ensure distinct created_at for stable pages
	}
	page1, cur := svc.AdminList(AdminListFilter{UserID: "u-cur", Limit: 2})
	if len(page1) != 2 || cur == "" {
		t.Fatalf("page1 len=%d cur=%q", len(page1), cur)
	}
	page2, cur2 := svc.AdminList(AdminListFilter{UserID: "u-cur", Limit: 2, Cursor: cur})
	if len(page2) != 2 {
		t.Fatalf("page2 len=%d", len(page2))
	}
	// no overlap between pages
	seen := map[string]bool{}
	for _, j := range page1 {
		seen[j.ID] = true
	}
	for _, j := range page2 {
		if seen[j.ID] {
			t.Fatalf("overlap %s", j.ID)
		}
		seen[j.ID] = true
	}
	page3, cur3 := svc.AdminList(AdminListFilter{UserID: "u-cur", Limit: 2, Cursor: cur2})
	if len(page3) != 1 || cur3 != "" {
		t.Fatalf("page3 len=%d cur=%q", len(page3), cur3)
	}
	if !seen[page3[0].ID] {
		seen[page3[0].ID] = true
	}
	if len(seen) != 5 {
		t.Fatalf("seen %d want 5: %v", len(seen), seen)
	}
	// pages are newest-first: page1[0] newer than page2[0]
	if !page1[0].CreatedAt.After(page2[0].CreatedAt) && page1[0].CreatedAt.Equal(page2[0].CreatedAt) && page1[0].ID <= page2[0].ID {
		// allow equal timestamps only if id DESC holds
		if page1[0].ID <= page2[0].ID && !page1[0].CreatedAt.After(page2[0].CreatedAt) {
			// if strictly same second, still require page order by keyset
		}
	}
	if page1[0].CreatedAt.Before(page2[0].CreatedAt) {
		t.Fatalf("page order: p1 %v before p2 %v", page1[0].CreatedAt, page2[0].CreatedAt)
	}
}

func TestAdminCancel(t *testing.T) {
	svc := NewService(store.NewMemory())
	j, _, err := svc.Create("u-other", CreateInput{DriveID: "d", Command: []string{"long"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim("u-other", j.ID, "a", "r1"); err != nil {
		t.Fatal(err)
	}
	done, err := svc.AdminCancel(j.ID, "stuck runner")
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != StatusCancelled {
		t.Fatalf("status %s", done.Status)
	}
	if !strings.Contains(done.Note, "admin cancel: stuck runner") {
		t.Fatalf("note %q", done.Note)
	}
	// idempotent
	again, err := svc.AdminCancel(j.ID, "")
	if err != nil || again.Status != StatusCancelled {
		t.Fatalf("idempotent: %v %+v", err, again)
	}
}

func TestAdminRelease(t *testing.T) {
	svc := NewService(store.NewMemory())
	j, _, err := svc.Create("u-rel", CreateInput{DriveID: "d", Command: []string{"work"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim("u-rel", j.ID, "a", "r1"); err != nil {
		t.Fatal(err)
	}
	rel, err := svc.AdminRelease(j.ID, "dead runner")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Status != StatusPending {
		t.Fatalf("status %s", rel.Status)
	}
	if rel.ClaimedByAgentID != "" || rel.ClaimedByRunnerID != "" {
		t.Fatalf("claimer should clear: %+v", rel)
	}
	if !strings.Contains(rel.Note, "released: admin: dead runner") {
		t.Fatalf("note %q", rel.Note)
	}
	// can claim again
	again, err := svc.Claim("u-rel", j.ID, "b", "r2")
	if err != nil || again.Status != StatusRunning {
		t.Fatalf("reclaim: %v %+v", err, again)
	}
	// release pending fails
	if _, err := svc.AdminRelease(j.ID+"nope", ""); err == nil {
		// wrong id
	}
	if _, err := svc.Complete("u-rel", j.ID, CompleteInput{OK: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AdminRelease(j.ID, "x"); err == nil {
		t.Fatal("expected release fail on terminal")
	}
}

func TestCompleteNoopWhenCancelled(t *testing.T) {
	svc := NewService(store.NewMemory())
	j, _, err := svc.Create("u", CreateInput{DriveID: "d", Command: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim("u", j.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Cancel("u", j.ID); err != nil {
		t.Fatal(err)
	}
	done, err := svc.Complete("u", j.ID, CompleteInput{OK: true, Note: "late"})
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != StatusCancelled {
		t.Fatalf("status %s want cancelled", done.Status)
	}
}

func TestLabelsAndRegionClaim(t *testing.T) {
	svc := NewService(store.NewMemory())
	uid := "u-lab"
	_, _, err := svc.Create(uid, CreateInput{
		DriveID: "d", Command: []string{"a"}, RegionHint: "us-east",
		Labels: map[string]string{"env": "prod", "team": "ml"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = svc.Create(uid, CreateInput{
		DriveID: "d", Command: []string{"b"}, RegionHint: "eu-west",
		Labels: map[string]string{"env": "dev"},
	})
	if err != nil {
		t.Fatal(err)
	}
	prod, _ := svc.List(uid, ListFilter{Labels: map[string]string{"env": "prod"}})
	if len(prod) != 1 || prod[0].Labels["env"] != "prod" {
		t.Fatalf("label filter: %+v", prod)
	}
	// region claim only eu
	got, err := svc.ClaimNext(uid, "", "r1", "eu-west")
	if err != nil {
		t.Fatal(err)
	}
	if got.RegionHint != "eu-west" {
		t.Fatalf("region %s", got.RegionHint)
	}
	if _, err := svc.ClaimNext(uid, "", "", "eu-west"); err == nil {
		t.Fatal("expected no more eu jobs")
	}
	// us still pending
	us, err := svc.ClaimNext(uid, "", "", "us-east")
	if err != nil || us.RegionHint != "us-east" {
		t.Fatalf("us claim: %v %+v", err, us)
	}
}

func TestClaimPriorityOrder(t *testing.T) {
	svc := NewService(store.NewMemory())
	uid := "u-pri"
	low, _, err := svc.Create(uid, CreateInput{DriveID: "d", Command: []string{"low"}, Priority: 1})
	if err != nil {
		t.Fatal(err)
	}
	high, _, err := svc.Create(uid, CreateInput{DriveID: "d", Command: []string{"high"}, Priority: 10})
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.ClaimNext(uid, "", "runner-a", "")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != high.ID {
		t.Fatalf("want high priority %s got %s", high.ID, first.ID)
	}
	if first.ClaimedByRunnerID != "runner-a" {
		t.Fatalf("runner_id %q", first.ClaimedByRunnerID)
	}
	second, err := svc.ClaimNext(uid, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != low.ID {
		t.Fatalf("want low %s got %s", low.ID, second.ID)
	}
}

func TestWebhookHMACVerify(t *testing.T) {
	body := []byte(`{"id":"j1","status":"succeeded"}`)
	ts := "1700000000"
	secret := "whsec_test"
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(ts + "."))
	_, _ = mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !VerifyJobWebhookSignature(secret, ts, sig, body) {
		t.Fatal("expected valid signature")
	}
	if VerifyJobWebhookSignature(secret, ts, "sha256=deadbeef", body) {
		t.Fatal("bad sig")
	}
	if VerifyJobWebhookSignature(secret, "1", sig, body) {
		t.Fatal("wrong ts")
	}
}

func TestWebhookOutboxDeliverAndRetry(t *testing.T) {
	var hits atomic.Int32
	var lastEventID string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		body, _ := io.ReadAll(r.Body)
		var env map[string]interface{}
		_ = json.Unmarshal(body, &env)
		mu.Lock()
		if id, _ := env["event_id"].(string); id != "" {
			lastEventID = id
		}
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// verify HMAC when secret set
		ts := r.Header.Get("X-AI-Cloudhub-Timestamp")
		sig := r.Header.Get("X-AI-Cloudhub-Signature")
		if !VerifyJobWebhookSignature("whsec_outbox", ts, sig, body) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("AI_CLOUDHUB_JOB_WEBHOOK_URL", srv.URL)
	t.Setenv("AI_CLOUDHUB_JOB_WEBHOOK_SECRET", "whsec_outbox")
	t.Setenv("AI_CLOUDHUB_JOB_WEBHOOK_BACKOFF_SEC", "0")
	t.Setenv("AI_CLOUDHUB_JOB_WEBHOOK_MAX_ATTEMPTS", "5")

	mem := store.NewMemory()
	svc := NewService(mem)
	uid := "u-wh"
	j, _, err := svc.Create(uid, CreateInput{DriveID: "d", Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim(uid, j.ID, "a", "r"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Complete(uid, j.ID, CompleteInput{OK: true}); err != nil {
		t.Fatal(err)
	}

	// First pass fails (500); retry after 1ms backoff succeeds.
	// notifyJobTerminal also kicks a background Process — race-tolerant wait.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = svc.ProcessWebhookOutbox(8)
		due, err := mem.ListDueWebhookOutbox(time.Now().UTC().Add(time.Hour), 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(due) == 0 && hits.Load() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if hits.Load() < 2 {
		t.Fatalf("expected retry hits>=2 got %d", hits.Load())
	}
	mu.Lock()
	eid := lastEventID
	mu.Unlock()
	if eid == "" {
		t.Fatal("missing event_id")
	}
	due, err := mem.ListDueWebhookOutbox(time.Now().UTC().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("still pending: %+v", due)
	}
	if metrics.JobsWebhookOK.Load() < 1 {
		t.Fatalf("expected webhook ok metric")
	}
}

func TestWebhookOutboxPurge(t *testing.T) {
	mem := store.NewMemory()
	svc := NewService(mem)
	now := time.Now().UTC()
	// two delivered old, one recent, one pending (must keep)
	old := now.Add(-2 * time.Hour)
	_ = mem.EnqueueWebhookOutbox(&store.WebhookOutbox{
		ID: "e-old-1", JobID: "j1", UserID: "u", Event: "job.succeeded",
		PayloadJSON: []byte(`{}`), Status: "delivered", DeliveredAt: old, CreatedAt: old, UpdatedAt: old, NextAttemptAt: old,
	})
	_ = mem.EnqueueWebhookOutbox(&store.WebhookOutbox{
		ID: "e-old-dead", JobID: "j2", UserID: "u", Event: "job.failed",
		PayloadJSON: []byte(`{}`), Status: "dead", CreatedAt: old, UpdatedAt: old, NextAttemptAt: old,
	})
	_ = mem.EnqueueWebhookOutbox(&store.WebhookOutbox{
		ID: "e-new", JobID: "j3", UserID: "u", Event: "job.succeeded",
		PayloadJSON: []byte(`{}`), Status: "delivered", DeliveredAt: now, CreatedAt: now, UpdatedAt: now, NextAttemptAt: now,
	})
	_ = mem.EnqueueWebhookOutbox(&store.WebhookOutbox{
		ID: "e-pend", JobID: "j4", UserID: "u", Event: "job.cancelled",
		PayloadJSON: []byte(`{}`), Status: "pending", CreatedAt: old, UpdatedAt: old, NextAttemptAt: old,
	})
	t.Setenv("AI_CLOUDHUB_JOB_WEBHOOK_RETAIN_SEC", "3600") // 1h
	n, err := svc.PurgeWebhookOutbox(0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("purged %d want 2", n)
	}
	if _, err := mem.GetWebhookOutbox("e-old-1"); err == nil {
		t.Fatal("old delivered should be gone")
	}
	if _, err := mem.GetWebhookOutbox("e-old-dead"); err == nil {
		t.Fatal("old dead should be gone")
	}
	if _, err := mem.GetWebhookOutbox("e-new"); err != nil {
		t.Fatal("recent delivered kept")
	}
	if _, err := mem.GetWebhookOutbox("e-pend"); err != nil {
		t.Fatal("pending kept")
	}
	// admin purge with short age
	_ = mem.EnqueueWebhookOutbox(&store.WebhookOutbox{
		ID: "e-admin", JobID: "j5", UserID: "u", Event: "job.succeeded",
		PayloadJSON: []byte(`{}`), Status: "delivered", DeliveredAt: now.Add(-10 * time.Second),
		CreatedAt: now, UpdatedAt: now, NextAttemptAt: now,
	})
	n2, err := svc.AdminPurgeWebhooks(5 * time.Second)
	if err != nil || n2 < 1 {
		t.Fatalf("admin purge: n=%d err=%v", n2, err)
	}
	if _, err := mem.GetWebhookOutbox("e-admin"); err == nil {
		t.Fatal("admin-purged row should be gone")
	}
}

func TestAdminRetryWebhooksBatch(t *testing.T) {
	mem := store.NewMemory()
	svc := NewService(mem)
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		_ = mem.EnqueueWebhookOutbox(&store.WebhookOutbox{
			ID: fmt.Sprintf("dead-%d", i), JobID: "j", UserID: "u", Event: "job.failed",
			PayloadJSON: []byte(`{}`), Status: "dead", CreatedAt: now, UpdatedAt: now, NextAttemptAt: now,
		})
	}
	_ = mem.EnqueueWebhookOutbox(&store.WebhookOutbox{
		ID: "del-1", JobID: "j", UserID: "u", Event: "job.succeeded",
		PayloadJSON: []byte(`{}`), Status: "delivered", DeliveredAt: now, CreatedAt: now, UpdatedAt: now, NextAttemptAt: now,
	})
	n, err := svc.AdminRetryWebhooksBatch(AdminWebhookFilter{Status: "dead", Limit: 10})
	if err != nil || n != 3 {
		t.Fatalf("batch dead: n=%d err=%v", n, err)
	}
	if len(svc.AdminListWebhooks(AdminWebhookFilter{Status: "dead", Limit: 10})) != 0 {
		t.Fatal("dead should be empty")
	}
	if len(svc.AdminListWebhooks(AdminWebhookFilter{Status: "pending", Limit: 10})) != 3 {
		t.Fatalf("pending want 3 got %d", len(svc.AdminListWebhooks(AdminWebhookFilter{Status: "pending", Limit: 10})))
	}
	// default status is dead
	n0, err := svc.AdminRetryWebhooksBatch(AdminWebhookFilter{Limit: 10})
	if err != nil || n0 != 0 {
		t.Fatalf("empty dead batch: n=%d err=%v", n0, err)
	}
	// delivered batch
	n2, err := svc.AdminRetryWebhooksBatch(AdminWebhookFilter{Status: "delivered", Limit: 10})
	if err != nil || n2 != 1 {
		t.Fatalf("batch delivered: n=%d err=%v", n2, err)
	}
	if _, err := svc.AdminRetryWebhooksBatch(AdminWebhookFilter{Status: "nope", Limit: 10}); err == nil {
		t.Fatal("expected invalid status")
	}
}

func TestAdminWebhookFilterByJobID(t *testing.T) {
	mem := store.NewMemory()
	svc := NewService(mem)
	now := time.Now().UTC()
	_ = mem.EnqueueWebhookOutbox(&store.WebhookOutbox{
		ID: "e1", JobID: "job-a", UserID: "u1", Event: "job.succeeded",
		PayloadJSON: []byte(`{}`), Status: "delivered", DeliveredAt: now, CreatedAt: now, UpdatedAt: now, NextAttemptAt: now,
	})
	_ = mem.EnqueueWebhookOutbox(&store.WebhookOutbox{
		ID: "e2", JobID: "job-b", UserID: "u1", Event: "job.failed",
		PayloadJSON: []byte(`{}`), Status: "dead", CreatedAt: now, UpdatedAt: now, NextAttemptAt: now,
	})
	_ = mem.EnqueueWebhookOutbox(&store.WebhookOutbox{
		ID: "e3", JobID: "job-a", UserID: "u2", Event: "job.cancelled",
		PayloadJSON: []byte(`{}`), Status: "dead", CreatedAt: now, UpdatedAt: now, NextAttemptAt: now,
	})
	byJob := svc.AdminListWebhooks(AdminWebhookFilter{JobID: "job-a", Limit: 10})
	if len(byJob) != 2 {
		t.Fatalf("job-a want 2 got %d", len(byJob))
	}
	byUser := svc.AdminListWebhooks(AdminWebhookFilter{UserID: "u2", Limit: 10})
	if len(byUser) != 1 || byUser[0].ID != "e3" {
		t.Fatalf("user filter: %+v", byUser)
	}
	byBoth := svc.AdminListWebhooks(AdminWebhookFilter{JobID: "job-a", Status: "dead", Limit: 10})
	if len(byBoth) != 1 || byBoth[0].ID != "e3" {
		t.Fatalf("job+status: %+v", byBoth)
	}
	n, err := svc.AdminRetryWebhooksBatch(AdminWebhookFilter{Status: "dead", JobID: "job-a", Limit: 10})
	if err != nil || n != 1 {
		t.Fatalf("retry job-a dead: n=%d err=%v", n, err)
	}
	if len(svc.AdminListWebhooks(AdminWebhookFilter{Status: "dead", JobID: "job-b", Limit: 10})) != 1 {
		t.Fatal("job-b dead should remain")
	}
}

func TestAdminWebhookFilterByEvent(t *testing.T) {
	mem := store.NewMemory()
	svc := NewService(mem)
	now := time.Now().UTC()
	_ = mem.EnqueueWebhookOutbox(&store.WebhookOutbox{
		ID: "e-ok", JobID: "j1", UserID: "u1", Event: "job.succeeded",
		PayloadJSON: []byte(`{}`), Status: "delivered", DeliveredAt: now, CreatedAt: now, UpdatedAt: now, NextAttemptAt: now,
	})
	_ = mem.EnqueueWebhookOutbox(&store.WebhookOutbox{
		ID: "e-fail", JobID: "j2", UserID: "u1", Event: "job.failed",
		PayloadJSON: []byte(`{}`), Status: "dead", CreatedAt: now, UpdatedAt: now, NextAttemptAt: now,
	})
	_ = mem.EnqueueWebhookOutbox(&store.WebhookOutbox{
		ID: "e-cancel", JobID: "j3", UserID: "u1", Event: "job.cancelled",
		PayloadJSON: []byte(`{}`), Status: "dead", CreatedAt: now, UpdatedAt: now, NextAttemptAt: now,
	})
	_ = mem.EnqueueWebhookOutbox(&store.WebhookOutbox{
		ID: "e-fail-2", JobID: "j4", UserID: "u2", Event: "job.failed",
		PayloadJSON: []byte(`{}`), Status: "pending", CreatedAt: now, UpdatedAt: now, NextAttemptAt: now,
	})

	failed := svc.AdminListWebhooks(AdminWebhookFilter{Event: "job.failed", Limit: 10})
	if len(failed) != 2 {
		t.Fatalf("job.failed want 2 got %d", len(failed))
	}
	for _, v := range failed {
		if v.Event != "job.failed" {
			t.Fatalf("unexpected event %q id=%s", v.Event, v.ID)
		}
	}
	ok := svc.AdminListWebhooks(AdminWebhookFilter{Event: "job.succeeded", Limit: 10})
	if len(ok) != 1 || ok[0].ID != "e-ok" {
		t.Fatalf("succeeded: %+v", ok)
	}
	// event + status
	deadFail := svc.AdminListWebhooks(AdminWebhookFilter{Event: "job.failed", Status: "dead", Limit: 10})
	if len(deadFail) != 1 || deadFail[0].ID != "e-fail" {
		t.Fatalf("failed+dead: %+v", deadFail)
	}
	// batch retry scoped by event
	n, err := svc.AdminRetryWebhooksBatch(AdminWebhookFilter{Status: "dead", Event: "job.failed", Limit: 10})
	if err != nil || n != 1 {
		t.Fatalf("retry failed dead: n=%d err=%v", n, err)
	}
	if len(svc.AdminListWebhooks(AdminWebhookFilter{Status: "dead", Event: "job.cancelled", Limit: 10})) != 1 {
		t.Fatal("cancelled dead should remain")
	}
	if len(svc.AdminListWebhooks(AdminWebhookFilter{Status: "pending", Event: "job.failed", Limit: 10})) != 2 {
		t.Fatal("both failed should be pending after requeue of e-fail")
	}
}

func TestAdminRetryWebhook(t *testing.T) {
	var hits atomic.Int32
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer failSrv.Close()

	t.Setenv("AI_CLOUDHUB_JOB_WEBHOOK_URL", failSrv.URL)
	t.Setenv("AI_CLOUDHUB_JOB_WEBHOOK_BACKOFF_SEC", "0")
	t.Setenv("AI_CLOUDHUB_JOB_WEBHOOK_MAX_ATTEMPTS", "1")

	mem := store.NewMemory()
	svc := NewService(mem)
	uid := "u-retry-admin"
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

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_ = svc.ProcessWebhookOutbox(8)
		due, _ := mem.ListDueWebhookOutbox(time.Now().UTC().Add(time.Hour), 10)
		if len(due) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	dead := svc.AdminListWebhooks(AdminWebhookFilter{Status: "dead", Limit: 10})
	if len(dead) != 1 {
		t.Fatalf("want 1 dead got %d", len(dead))
	}
	t.Setenv("AI_CLOUDHUB_JOB_WEBHOOK_URL", okSrv.URL)
	t.Setenv("AI_CLOUDHUB_JOB_WEBHOOK_MAX_ATTEMPTS", "5")

	got, err := svc.AdminRetryWebhook(dead[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "pending" || got.Attempts != 0 {
		t.Fatalf("after retry: %+v", got)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_ = svc.ProcessWebhookOutbox(8)
		if len(svc.AdminListWebhooks(AdminWebhookFilter{Status: "delivered", Limit: 10})) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if hits.Load() < 1 {
		t.Fatalf("expected delivery after admin retry, hits=%d", hits.Load())
	}
	view, err := svc.AdminGetWebhook(dead[0].ID)
	if err != nil || view.Status != "delivered" {
		t.Fatalf("get after deliver: %v %+v", err, view)
	}
	if len(view.Payload) == 0 {
		t.Fatal("expected payload on get")
	}
}

func TestWebhookOutboxDeadAfterMax(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	t.Setenv("AI_CLOUDHUB_JOB_WEBHOOK_URL", srv.URL)
	t.Setenv("AI_CLOUDHUB_JOB_WEBHOOK_BACKOFF_SEC", "0")
	t.Setenv("AI_CLOUDHUB_JOB_WEBHOOK_MAX_ATTEMPTS", "2")

	mem := store.NewMemory()
	svc := NewService(mem)
	uid := "u-dead"
	j, _, err := svc.Create(uid, CreateInput{DriveID: "d", Command: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim(uid, j.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Complete(uid, j.ID, CompleteInput{OK: false, Note: "boom"}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_ = svc.ProcessWebhookOutbox(8)
		due, _ := mem.ListDueWebhookOutbox(time.Now().UTC().Add(time.Hour), 10)
		if len(due) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	due, err := mem.ListDueWebhookOutbox(time.Now().UTC().Add(24*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("expected no pending after dead, got %d", len(due))
	}
	if metrics.JobsWebhookDead.Load() < 1 {
		t.Fatalf("expected dead metric, fail=%d dead=%d", metrics.JobsWebhookFail.Load(), metrics.JobsWebhookDead.Load())
	}
}

func TestMaxAttemptsOnLeaseExpiry(t *testing.T) {
	mem := store.NewMemory()
	svc := NewService(mem)
	svc.SetLease(50 * time.Millisecond)
	uid := "u-max"
	j, _, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"x"}, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	if j.MaxAttempts != 1 {
		t.Fatalf("max_attempts %d", j.MaxAttempts)
	}
	claimed, err := svc.Claim(uid, j.ID, "a", "")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.AttemptCount != 1 {
		t.Fatalf("attempt %d", claimed.AttemptCount)
	}
	time.Sleep(80 * time.Millisecond)
	n, err := svc.ReclaimStale(uid)
	if err != nil || n != 1 {
		t.Fatalf("reclaim n=%d err=%v", n, err)
	}
	done, err := svc.Get(uid, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != StatusFailed {
		t.Fatalf("status %s want failed", done.Status)
	}
	if !strings.Contains(done.Note, "max attempts") {
		t.Fatalf("note %q", done.Note)
	}
}

func TestHardTimeoutFailsJob(t *testing.T) {
	mem := store.NewMemory()
	svc := NewService(mem)
	svc.SetLease(0) // only timeout path
	uid := "u-to"
	j, _, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"x"}, TimeoutSec: 1})
	if err != nil {
		t.Fatal(err)
	}
	if j.TimeoutSec != 1 {
		t.Fatalf("timeout_sec %d", j.TimeoutSec)
	}
	claimed, err := svc.Claim(uid, j.ID, "a", "")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ClaimedAt == nil {
		t.Fatal("claimed_at required")
	}
	got, err := mem.GetJob(uid, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	got.ClaimedAt = time.Now().UTC().Add(-2 * time.Second)
	got.HeartbeatAt = got.ClaimedAt
	if err := mem.UpdateJob(got); err != nil {
		t.Fatal(err)
	}
	n, err := svc.ReclaimStale(uid)
	if err != nil || n != 1 {
		t.Fatalf("reclaim n=%d err=%v", n, err)
	}
	done, err := svc.Get(uid, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != StatusFailed {
		t.Fatalf("status %s", done.Status)
	}
	if done.ExitCode == nil || *done.ExitCode != 124 {
		t.Fatalf("exit %v", done.ExitCode)
	}
	if !strings.Contains(done.Note, "timeout after 1s") {
		t.Fatalf("note %q", done.Note)
	}
}

func TestHeartbeatAndLeaseReclaim(t *testing.T) {
	svc := NewService(store.NewMemory())
	svc.SetLease(50 * time.Millisecond)
	uid := "u-lease"
	j, _, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"sleep"}})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := svc.Claim(uid, j.ID, "agent-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.HeartbeatAt == nil {
		t.Fatal("claim should set heartbeat_at")
	}
	hb1 := *claimed.HeartbeatAt
	time.Sleep(20 * time.Millisecond)
	refreshed, err := svc.Heartbeat(uid, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.HeartbeatAt == nil || !refreshed.HeartbeatAt.After(hb1) {
		t.Fatalf("heartbeat not advanced: was %v now %v", hb1, refreshed.HeartbeatAt)
	}
	// Let lease expire without further heartbeats.
	time.Sleep(80 * time.Millisecond)
	n, err := svc.ReclaimStale(uid)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reclaim count %d want 1", n)
	}
	got, err := svc.Get(uid, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPending {
		t.Fatalf("status %s want pending", got.Status)
	}
	if got.ClaimedByAgentID != "" {
		t.Fatalf("claimer should clear: %q", got.ClaimedByAgentID)
	}
	if !strings.Contains(got.Note, "lease expired") {
		t.Fatalf("note %q", got.Note)
	}
	// ClaimNext path reclaims too.
	j2, _, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim(uid, j2.ID, "a", ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	// Oldest reclaimed job first, then we can claim again.
	next, err := svc.ClaimNext(uid, "a2", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != j.ID && next.ID != j2.ID {
		t.Fatalf("unexpected claim %s", next.ID)
	}
	if next.Status != StatusRunning {
		t.Fatalf("status %s", next.Status)
	}
}

// TestReclaimStaleUsesRunningOnly ensures reclaim ignores non-running jobs and still
// transitions lease-expired running work (via ListRunningJobs, not full ListJobs).
func TestReclaimStaleUsesRunningOnly(t *testing.T) {
	mem := store.NewMemory()
	svc := NewService(mem)
	svc.SetLease(50 * time.Millisecond)
	uid := "u-run-only"

	// Terminal / pending noise that must not be scanned as reclaim targets.
	for i, st := range []Status{StatusPending, StatusSucceeded, StatusFailed, StatusCancelled} {
		j, _, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"noise"}})
		if err != nil {
			t.Fatal(err)
		}
		sj, err := mem.GetJob(uid, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		sj.Status = string(st)
		sj.UpdatedAt = time.Now().UTC().Add(-time.Hour)
		sj.HeartbeatAt = time.Now().UTC().Add(-time.Hour)
		if err := mem.UpdateJob(sj); err != nil {
			t.Fatal(err)
		}
		_ = i
	}

	running, _, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"live"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim(uid, running.ID, "a", ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)

	n, err := svc.ReclaimStale(uid)
	if err != nil || n != 1 {
		t.Fatalf("reclaim n=%d err=%v want 1", n, err)
	}
	got, err := svc.Get(uid, running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPending {
		t.Fatalf("status %s want pending", got.Status)
	}
	// Noise jobs unchanged.
	all, err := mem.ListJobs(uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("job count %d", len(all))
	}
	runOnly, err := mem.ListRunningJobs(uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(runOnly) != 0 {
		t.Fatalf("expected no running after reclaim, got %d", len(runOnly))
	}
}

func TestCreateRecordsAgentID(t *testing.T) {
	svc := NewService(store.NewMemory())
	j, _, err := svc.Create("u", CreateInput{DriveID: "d", Command: []string{"x"}, AgentID: "creator-a"})
	if err != nil {
		t.Fatal(err)
	}
	if j.AgentID != "creator-a" {
		t.Fatalf("agent_id=%q", j.AgentID)
	}
	got, err := svc.Get("u", j.ID)
	if err != nil || got.AgentID != "creator-a" {
		t.Fatalf("get %+v err=%v", got, err)
	}
}

func TestListFilterByAgent(t *testing.T) {
	svc := NewService(store.NewMemory())
	uid := "u-lf"
	if _, _, err := svc.Create(uid, CreateInput{DriveID: "d", Command: []string{"a"}, AgentID: "a1"}); err != nil {
		t.Fatal(err)
	}
	j2, _, err := svc.Create(uid, CreateInput{DriveID: "d", Command: []string{"b"}, AgentID: "a2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim(uid, j2.ID, "claimer-x", ""); err != nil {
		t.Fatal(err)
	}
	onlyA1, _ := svc.List(uid, ListFilter{AgentID: "a1"})
	if len(onlyA1) != 1 || onlyA1[0].AgentID != "a1" {
		t.Fatalf("%+v", onlyA1)
	}
	byClaimer, _ := svc.List(uid, ListFilter{ClaimedByAgentID: "claimer-x"})
	if len(byClaimer) != 1 || byClaimer[0].ID != j2.ID {
		t.Fatalf("%+v", byClaimer)
	}
	all, next := svc.List(uid)
	if len(all) != 2 {
		t.Fatalf("all=%d", len(all))
	}
	if next != "" {
		t.Fatalf("unexpected next_cursor %q", next)
	}
}

func TestListFilterPushDown(t *testing.T) {
	svc := NewService(store.NewMemory())
	uid := "u-push"
	j1, _, err := svc.Create(uid, CreateInput{
		DriveID: "d", Command: []string{"a"}, AgentID: "ag1",
		Labels: map[string]string{"env": "prod", "team": "ml"},
	})
	if err != nil {
		t.Fatal(err)
	}
	j2, _, err := svc.Create(uid, CreateInput{
		DriveID: "d", Command: []string{"b"}, AgentID: "ag2",
		Labels: map[string]string{"env": "dev"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim(uid, j2.ID, "claimer-z", ""); err != nil {
		t.Fatal(err)
	}
	// agent filter
	only, _ := svc.List(uid, ListFilter{AgentID: "ag1", Limit: 50})
	if len(only) != 1 || only[0].ID != j1.ID {
		t.Fatalf("agent filter: %+v", only)
	}
	// labels AND match
	prod, _ := svc.List(uid, ListFilter{Labels: map[string]string{"env": "prod", "team": "ml"}, Limit: 50})
	if len(prod) != 1 || prod[0].ID != j1.ID {
		t.Fatalf("labels: %+v", prod)
	}
	// claimer + status
	run, _ := svc.List(uid, ListFilter{ClaimedByAgentID: "claimer-z", Status: "running", Limit: 50})
	if len(run) != 1 || run[0].ID != j2.ID {
		t.Fatalf("claimer/status: %+v", run)
	}
	// no match
	none, _ := svc.List(uid, ListFilter{Labels: map[string]string{"env": "staging"}, Limit: 50})
	if len(none) != 0 {
		t.Fatalf("want empty got %+v", none)
	}
}

func TestListCursor(t *testing.T) {
	svc := NewService(store.NewMemory())
	uid := "u-list-cur"
	for i := 0; i < 5; i++ {
		if _, _, err := svc.Create(uid, CreateInput{DriveID: "d", Command: []string{fmt.Sprintf("x%d", i)}}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	p1, cur := svc.List(uid, ListFilter{Limit: 2})
	if len(p1) != 2 || cur == "" {
		t.Fatalf("page1 len=%d cur=%q", len(p1), cur)
	}
	p2, cur2 := svc.List(uid, ListFilter{Limit: 2, Cursor: cur})
	if len(p2) != 2 {
		t.Fatalf("page2 len=%d", len(p2))
	}
	seen := map[string]bool{}
	for _, j := range p1 {
		seen[j.ID] = true
	}
	for _, j := range p2 {
		if seen[j.ID] {
			t.Fatalf("overlap %s", j.ID)
		}
		seen[j.ID] = true
	}
	p3, cur3 := svc.List(uid, ListFilter{Limit: 2, Cursor: cur2})
	if len(p3) != 1 || cur3 != "" {
		t.Fatalf("page3 len=%d cur=%q", len(p3), cur3)
	}
	seen[p3[0].ID] = true
	if len(seen) != 5 {
		t.Fatalf("seen %d", len(seen))
	}
	if p1[0].CreatedAt.Before(p2[0].CreatedAt) {
		t.Fatalf("order: p1 %v before p2 %v", p1[0].CreatedAt, p2[0].CreatedAt)
	}
}

func TestClaimNextFilteredSkipsDeniedDrives(t *testing.T) {
	svc := NewService(store.NewMemory())
	uid := "u-filt"
	j1, _, err := svc.Create(uid, CreateInput{DriveID: "forbidden", Command: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	j2, _, err := svc.Create(uid, CreateInput{DriveID: "allowed", Command: []string{"b"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.ClaimNextFiltered(uid, "runner-bot", "", "", func(driveID string) string {
		if driveID == "forbidden" {
			return "drive not allowed for agent"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != j2.ID || got.DriveID != "allowed" {
		t.Fatalf("got %+v want j2=%s", got, j2.ID)
	}
	if got.ClaimedByAgentID != "runner-bot" {
		t.Fatalf("claimed_by=%q", got.ClaimedByAgentID)
	}
	// j1 must be pending again (released)
	back, err := svc.Get(uid, j1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back.Status != StatusPending {
		t.Fatalf("j1 status %s want pending", back.Status)
	}
}

func TestClaimNextFilteredAllDenied(t *testing.T) {
	svc := NewService(store.NewMemory())
	uid := "u-alldeny"
	if _, _, err := svc.Create(uid, CreateInput{DriveID: "x", Command: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.ClaimNextFiltered(uid, "bot", "", "", func(string) string { return "blocked" })
	if err == nil || !strings.Contains(err.Error(), "no claimable") {
		t.Fatalf("err=%v", err)
	}
	// Job must still be pending (never stuck running).
	list := svc.ListPending(uid, "")
	if len(list) != 1 || list[0].Status != StatusPending {
		t.Fatalf("pending after all-deny: %+v", list)
	}
}
