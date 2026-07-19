package sts

import (
	"strings"
	"testing"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

func testResolved() *provider.Resolved {
	return &provider.Resolved{
		Type:           provider.TypeMinIO,
		AccessKey:      "ak",
		SecretKey:      "sk",
		Endpoint:       "127.0.0.1:9000",
		Region:         "us-east-1",
		ForcePathStyle: true,
		UseSSL:         false,
		ProviderLabel:  "Minio",
	}
}

func TestIssueAndRefresh(t *testing.T) {
	// Ensure optional native STS paths are off so unit tests never hit the network.
	t.Setenv("AI_CLOUDHUB_MINIO_STS", "0")
	t.Setenv("AI_CLOUDHUB_AWS_STS", "0")

	s := New(time.Minute, "http://localhost:8080")
	resolved := testResolved()
	sess, err := s.Issue(IssueInput{
		UserID: "u1", DriveID: "d1", MountPoint: "/workspace", Mode: "mount",
		Bucket: "b", Resolved: resolved,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sess.Token == "" || sess.Spec.RemotePath == "" {
		t.Fatalf("bad session %+v", sess)
	}
	if sess.Source != SourceEmbedded {
		t.Fatalf("source want %q got %q", SourceEmbedded, sess.Source)
	}
	if !strings.Contains(sess.Spec.RcloneConf, "access_key_id = ak") {
		t.Fatalf("conf missing access key: %s", sess.Spec.RcloneConf)
	}
	if strings.Contains(sess.Spec.RcloneConf, "session_token") {
		t.Fatal("embedded session should not set session_token")
	}

	oldTok := sess.Token
	ref, err := s.Refresh(oldTok, resolved, "b", "")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Token == oldTok {
		t.Fatal("token should rotate")
	}
	if ref.Source != SourceRefresh {
		t.Fatalf("refresh source want %q got %q", SourceRefresh, ref.Source)
	}
	if _, err := s.GetByToken(oldTok); err == nil {
		t.Fatal("old token should be invalid")
	}
	if _, err := s.GetByToken(ref.Token); err != nil {
		t.Fatal(err)
	}
	// same session id after refresh
	if ref.ID != sess.ID {
		t.Fatalf("id changed: %s -> %s", sess.ID, ref.ID)
	}
}

func TestRefreshInvalidToken(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_MINIO_STS", "0")
	t.Setenv("AI_CLOUDHUB_AWS_STS", "0")
	s := New(time.Minute, "http://localhost:8080")
	_, err := s.Refresh("no-such-token", testResolved(), "b", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRefreshBeyondGrace(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_MINIO_STS", "0")
	t.Setenv("AI_CLOUDHUB_AWS_STS", "0")
	s := New(time.Millisecond, "http://localhost:8080")
	resolved := testResolved()
	sess, err := s.Issue(IssueInput{
		UserID: "u1", DriveID: "d1", MountPoint: "/m",
		Bucket: "b", Resolved: resolved,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Force expiry far past grace window.
	s.mu.Lock()
	sess.ExpiresAt = time.Now().UTC().Add(-10 * time.Minute)
	s.mu.Unlock()

	_, err = s.Refresh(sess.Token, resolved, "b", "")
	if err == nil {
		t.Fatal("expected beyond-grace error")
	}
	if !strings.Contains(err.Error(), "grace") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestIssueRequiresResolvedAndMount(t *testing.T) {
	s := New(time.Minute, "")
	if _, err := s.Issue(IssueInput{MountPoint: "/m"}); err == nil {
		t.Fatal("expected resolved required")
	}
	if _, err := s.Issue(IssueInput{Resolved: testResolved()}); err == nil {
		t.Fatal("expected mount_point required")
	}
}

func TestRevoke(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_MINIO_STS", "0")
	t.Setenv("AI_CLOUDHUB_AWS_STS", "0")
	s := New(time.Minute, "")
	sess, err := s.Issue(IssueInput{
		UserID: "u", DriveID: "d", MountPoint: "/m",
		Bucket: "b", Resolved: testResolved(),
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Revoke(sess.ID)
	if _, err := s.GetByToken(sess.Token); err == nil {
		t.Fatal("revoked token should fail")
	}
}

func TestSessionTokenInConf(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_MINIO_STS", "0")
	t.Setenv("AI_CLOUDHUB_AWS_STS", "0")
	s := New(time.Minute, "")
	r := testResolved()
	r.SessionToken = "temp-session-tok"
	sess, err := s.Issue(IssueInput{
		UserID: "u", DriveID: "d", MountPoint: "/m",
		Bucket: "b", Resolved: r,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sess.Spec.RcloneConf, "session_token = temp-session-tok") {
		t.Fatalf("expected session_token in conf:\n%s", sess.Spec.RcloneConf)
	}
}
