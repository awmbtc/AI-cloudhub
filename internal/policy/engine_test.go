package policy

import "testing"

func TestEngineHumanAlways(t *testing.T) {
	d := NewEngine().Evaluate(Request{Action: ActionDriveWrite, DriveID: "d1"})
	if !d.Allow {
		t.Fatal(d.Reason)
	}
}

func TestEngineDriveAllowlist(t *testing.T) {
	d := NewEngine().Evaluate(Request{
		AgentID:         "a1",
		Scopes:          []string{"drive.read"},
		Action:          ActionDriveSession,
		DriveID:         "d2",
		AllowedDriveIDs: []string{"d1"},
	})
	if d.Allow {
		t.Fatal("should deny other drive")
	}
	d = NewEngine().Evaluate(Request{
		AgentID:         "a1",
		Scopes:          []string{"drive.read"},
		Action:          ActionDriveSession,
		DriveID:         "d1",
		AllowedDriveIDs: []string{"d1"},
	})
	if !d.Allow {
		t.Fatal(d.Reason)
	}
}

func TestEngineScope(t *testing.T) {
	d := NewEngine().Evaluate(Request{
		AgentID: "a1",
		Scopes:  []string{"drive.read"},
		Action:  ActionDriveWrite,
		DriveID: "d1",
	})
	if d.Allow {
		t.Fatal("missing write scope")
	}
}

func TestCanAccessDrive(t *testing.T) {
	if err := CanAccessDrive("a1", []string{"d1"}, "d2"); err == nil {
		t.Fatal("expected deny")
	}
	if err := CanAccessDrive("a1", nil, "d2"); err != nil {
		t.Fatal("empty allowlist = all")
	}
	if err := CanAccessDrive("", []string{"d1"}, "d2"); err != nil {
		t.Fatal("human")
	}
}
