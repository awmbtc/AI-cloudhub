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

func TestEngineProviderWriteImpliesRead(t *testing.T) {
	d := NewEngine().Evaluate(Request{
		AgentID: "a1",
		Scopes:  []string{"provider.write"},
		Action:  ActionProviderRead,
	})
	if !d.Allow {
		t.Fatal(d.Reason)
	}
	d = NewEngine().Evaluate(Request{
		AgentID: "a1",
		Scopes:  []string{"provider.read"},
		Action:  ActionProviderWrite,
	})
	if d.Allow {
		t.Fatal("provider.read must not imply write")
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

// TestEngineJobRunScopeAndDrive: agents need job.run; optional drive allowlist applies to job.run.
func TestEngineJobRunScopeAndDrive(t *testing.T) {
	e := NewEngine()
	// Missing scope
	d := e.Evaluate(Request{
		AgentID: "a1",
		Scopes:  []string{"drive.read"},
		Action:  ActionJobRun,
		DriveID: "d1",
	})
	if d.Allow {
		t.Fatal("job.run without scope should deny")
	}
	// Scope ok, all drives
	d = e.Evaluate(Request{
		AgentID: "a1",
		Scopes:  []string{"job.run"},
		Action:  ActionJobRun,
		DriveID: "d1",
	})
	if !d.Allow {
		t.Fatal(d.Reason)
	}
	// Scope ok, drive not in allowlist
	d = e.Evaluate(Request{
		AgentID:         "a1",
		Scopes:          []string{"job.run"},
		Action:          ActionJobRun,
		DriveID:         "d-other",
		AllowedDriveIDs: []string{"d1"},
	})
	if d.Allow {
		t.Fatal("drive not allowed should deny")
	}
	// Scope + allowed drive
	d = e.Evaluate(Request{
		AgentID:         "a1",
		Scopes:          []string{"job.run"},
		Action:          ActionJobRun,
		DriveID:         "d1",
		AllowedDriveIDs: []string{"d1"},
	})
	if !d.Allow {
		t.Fatal(d.Reason)
	}
	// Humans skip agent scope checks
	d = e.Evaluate(Request{Action: ActionJobRun, DriveID: "d1"})
	if !d.Allow {
		t.Fatal(d.Reason)
	}
}
