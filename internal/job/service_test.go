package job

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/store"
)

func TestListPendingFiltersByRegion(t *testing.T) {
	svc := NewService(store.NewMemory())
	uid := "u1"
	if _, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"echo", "a"}, RegionHint: "us-east"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"echo", "b"}, RegionHint: "eu-west"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"echo", "c"}}); err != nil {
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
	created, err := svc.Create(uid, CreateInput{
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
			j, err := svc.ClaimNext(uid, "")
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
	if _, err := svc.ClaimNext(uid, ""); err == nil {
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
		j, err := svc.Create(uid, CreateInput{DriveID: "d", Command: []string{"echo", "x"}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.ID)
	}
	first, err := svc.ClaimNext(uid, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != ids[0] {
		t.Fatalf("expected oldest %s, got %s", ids[0], first.ID)
	}
	second, err := svc.ClaimNext(uid, "")
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
	j, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"echo"}})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := svc.Claim(uid, j.ID, "agent-claim")
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
	again, err := svc.Claim(uid, j.ID, "agent-2")
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
	j, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"echo"}, Note: "user-seed"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(j.Note, "user-seed") || !strings.Contains(j.Note, "BYOC only") {
		t.Fatalf("create note %q", j.Note)
	}
	if _, err := svc.Claim(uid, j.ID, ""); err != nil {
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
	j2, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"echo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim(uid, j2.ID, ""); err != nil {
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
	j, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"echo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim(uid, j.ID, ""); err != nil {
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
	succ := svc.List(uid, ListFilter{Status: "succeeded"})
	if len(succ) != 1 || succ[0].ID != j.ID {
		t.Fatalf("list succeeded: %+v", succ)
	}
	run := svc.List(uid, ListFilter{Status: "running"})
	if len(run) != 0 {
		t.Fatalf("list running want 0 got %d", len(run))
	}
	// pending list empty after complete
	pend := svc.ListPending(uid, "")
	if len(pend) != 0 {
		t.Fatalf("pending %d", len(pend))
	}
}

func TestMaxAttemptsOnLeaseExpiry(t *testing.T) {
	mem := store.NewMemory()
	svc := NewService(mem)
	svc.SetLease(50 * time.Millisecond)
	uid := "u-max"
	j, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"x"}, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	if j.MaxAttempts != 1 {
		t.Fatalf("max_attempts %d", j.MaxAttempts)
	}
	claimed, err := svc.Claim(uid, j.ID, "a")
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
	j, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"x"}, TimeoutSec: 1})
	if err != nil {
		t.Fatal(err)
	}
	if j.TimeoutSec != 1 {
		t.Fatalf("timeout_sec %d", j.TimeoutSec)
	}
	claimed, err := svc.Claim(uid, j.ID, "a")
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
	j, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"sleep"}})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := svc.Claim(uid, j.ID, "agent-1")
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
	j2, err := svc.Create(uid, CreateInput{DriveID: "d1", Command: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim(uid, j2.ID, "a"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	// Oldest reclaimed job first, then we can claim again.
	next, err := svc.ClaimNext(uid, "a2")
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

func TestCreateRecordsAgentID(t *testing.T) {
	svc := NewService(store.NewMemory())
	j, err := svc.Create("u", CreateInput{DriveID: "d", Command: []string{"x"}, AgentID: "creator-a"})
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
	if _, err := svc.Create(uid, CreateInput{DriveID: "d", Command: []string{"a"}, AgentID: "a1"}); err != nil {
		t.Fatal(err)
	}
	j2, err := svc.Create(uid, CreateInput{DriveID: "d", Command: []string{"b"}, AgentID: "a2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim(uid, j2.ID, "claimer-x"); err != nil {
		t.Fatal(err)
	}
	onlyA1 := svc.List(uid, ListFilter{AgentID: "a1"})
	if len(onlyA1) != 1 || onlyA1[0].AgentID != "a1" {
		t.Fatalf("%+v", onlyA1)
	}
	byClaimer := svc.List(uid, ListFilter{ClaimedByAgentID: "claimer-x"})
	if len(byClaimer) != 1 || byClaimer[0].ID != j2.ID {
		t.Fatalf("%+v", byClaimer)
	}
	all := svc.List(uid)
	if len(all) != 2 {
		t.Fatalf("all=%d", len(all))
	}
}

func TestClaimNextFilteredSkipsDeniedDrives(t *testing.T) {
	svc := NewService(store.NewMemory())
	uid := "u-filt"
	j1, err := svc.Create(uid, CreateInput{DriveID: "forbidden", Command: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	j2, err := svc.Create(uid, CreateInput{DriveID: "allowed", Command: []string{"b"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.ClaimNextFiltered(uid, "runner-bot", func(driveID string) string {
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
	if _, err := svc.Create(uid, CreateInput{DriveID: "x", Command: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.ClaimNextFiltered(uid, "bot", func(string) string { return "blocked" })
	if err == nil || !strings.Contains(err.Error(), "no claimable") {
		t.Fatalf("err=%v", err)
	}
	// Job must still be pending (never stuck running).
	list := svc.ListPending(uid, "")
	if len(list) != 1 || list[0].Status != StatusPending {
		t.Fatalf("pending after all-deny: %+v", list)
	}
}
