package drive

import (
	"testing"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/sts"
)

func TestIssueSessionForBindingOptsPropagatesAgent(t *testing.T) {
	// Embedded STS only — no network/S3.
	t.Setenv("AI_CLOUDHUB_MINIO_STS", "0")
	t.Setenv("AI_CLOUDHUB_AWS_STS", "0")

	ds, uid, driveID := testDriveSvc(t)
	ds.SetSTS(sts.New(time.Minute, "http://localhost:8080"))

	b, err := ds.CreateBinding(uid, BindingCreate{
		DriveID:    driveID,
		DeviceID:   "dev1",
		MountPoint: "/workspace",
		Mode:       "mount",
	})
	if err != nil {
		t.Fatal(err)
	}

	opts := SessionOpts{
		AgentID:       "agent-42",
		ReadPrefixes:  []string{"in"},
		WritePrefixes: []string{"out"},
	}
	bundle, err := ds.IssueSessionForBindingOpts(uid, b.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Binding == nil || bundle.Binding.ID != b.ID {
		t.Fatalf("binding: %+v", bundle.Binding)
	}
	if bundle.Manifest.Version != 2 {
		t.Fatalf("manifest version = %d, want 2", bundle.Manifest.Version)
	}
	if bundle.Manifest.Agent.AgentID != "agent-42" {
		t.Fatalf("agent_id = %q", bundle.Manifest.Agent.AgentID)
	}
	if bundle.Manifest.Permissions == nil {
		t.Fatal("permissions nil")
	}
	if len(bundle.Manifest.Permissions.Read) != 1 || bundle.Manifest.Permissions.Read[0] != "/workspace/in" {
		t.Fatalf("read perms = %+v", bundle.Manifest.Permissions.Read)
	}
	if len(bundle.Manifest.Permissions.Write) != 1 || bundle.Manifest.Permissions.Write[0] != "/workspace/out" {
		t.Fatalf("write perms = %+v", bundle.Manifest.Permissions.Write)
	}

	// Backward-compatible path (no opts) stays Manifest v1 full workspace.
	plain, err := ds.IssueSessionForBinding(uid, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Manifest.Version != 1 {
		t.Fatalf("plain version = %d, want 1", plain.Manifest.Version)
	}
	if plain.Manifest.Agent.AgentID != "" {
		t.Fatalf("plain agent_id should be empty, got %q", plain.Manifest.Agent.AgentID)
	}
}

func TestIssueSessionForBindingMissing(t *testing.T) {
	ds, uid, _ := testDriveSvc(t)
	ds.SetSTS(sts.New(time.Minute, "http://localhost:8080"))
	if _, err := ds.IssueSessionForBinding(uid, "no-such-binding"); err == nil {
		t.Fatal("expected not found")
	}
}
