package job

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

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
			j, err := svc.ClaimNext(uid)
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
	if _, err := svc.ClaimNext(uid); err == nil {
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
	first, err := svc.ClaimNext(uid)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != ids[0] {
		t.Fatalf("expected oldest %s, got %s", ids[0], first.ID)
	}
	second, err := svc.ClaimNext(uid)
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
	claimed, err := svc.Claim(uid, j.ID)
	if err != nil || claimed.Status != StatusRunning {
		t.Fatalf("claim: %v %+v", err, claimed)
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
	again, err := svc.Claim(uid, j.ID)
	if err != nil || again.Status != StatusRunning {
		t.Fatalf("reclaim: %v %+v", err, again)
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
	got, err := svc.ClaimNextFiltered(uid, func(driveID string) string {
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
	_, err := svc.ClaimNextFiltered(uid, func(string) string { return "blocked" })
	if err == nil || !strings.Contains(err.Error(), "no claimable") {
		t.Fatalf("err=%v", err)
	}
	// Job must still be pending (never stuck running).
	list := svc.ListPending(uid, "")
	if len(list) != 1 || list[0].Status != StatusPending {
		t.Fatalf("pending after all-deny: %+v", list)
	}
}
