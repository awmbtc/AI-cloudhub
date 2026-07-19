package policy

import "testing"

func TestCheckBindings(t *testing.T) {
	q := Quota{MaxConcurrentBindings: 2}
	if err := q.CheckBindings(0); err != nil {
		t.Fatalf("0: %v", err)
	}
	if err := q.CheckBindings(1); err != nil {
		t.Fatalf("1: %v", err)
	}
	if err := q.CheckBindings(2); err == nil {
		t.Fatal("expected quota error at 2")
	}
}

func TestCheckDrives(t *testing.T) {
	q := Quota{MaxDrives: 2}
	if err := q.CheckDrives(1); err != nil {
		t.Fatal(err)
	}
	if err := q.CheckDrives(2); err == nil {
		t.Fatal("expected drive quota error")
	}
}

func TestCheckProviders(t *testing.T) {
	q := Quota{MaxProviders: 1}
	if err := q.CheckProviders(0); err != nil {
		t.Fatal(err)
	}
	if err := q.CheckProviders(1); err == nil {
		t.Fatal("expected provider quota error")
	}
}

func TestDefaultQuota(t *testing.T) {
	q := Quota{} // zero → default 10 bindings, 20 drives/providers
	if err := q.CheckBindings(9); err != nil {
		t.Fatal(err)
	}
	if err := q.CheckBindings(10); err == nil {
		t.Fatal("expected default max 10")
	}
	if err := DefaultQuota.CheckBindings(10); err == nil {
		t.Fatal("DefaultQuota should cap at 10")
	}
	if err := q.CheckDrives(19); err != nil {
		t.Fatal(err)
	}
	if err := q.CheckDrives(20); err == nil {
		t.Fatal("expected default max drives 20")
	}
	if err := q.CheckProviders(20); err == nil {
		t.Fatal("expected default max providers 20")
	}
}
