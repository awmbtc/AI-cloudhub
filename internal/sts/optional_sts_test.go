package sts

import (
	"strings"
	"testing"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

func TestApplyOptionalSTSVendorNotes(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_MINIO_STS", "0")
	t.Setenv("AI_CLOUDHUB_AWS_STS", "0")

	cases := []struct {
		typ  provider.Type
		want string
	}{
		{provider.TypeR2, "R2"},
		{provider.TypeB2, "B2"},
		{provider.TypeOSS, "OSS"},
		{provider.TypeCOS, "COS"},
		{provider.TypeQiniu, "Qiniu"},
		{provider.TypeOracle, "Oracle"},
	}
	for _, tc := range cases {
		t.Run(string(tc.typ), func(t *testing.T) {
			r := &provider.Resolved{
				Type:      tc.typ,
				AccessKey: "ak",
				SecretKey: "sk",
				Endpoint:  "example.invalid",
			}
			out, source, note := applyOptionalSTS(r, time.Hour, SourceEmbedded)
			if source != SourceEmbedded {
				t.Fatalf("source %s", source)
			}
			if out != r {
				t.Fatal("resolved should be unchanged")
			}
			if !strings.Contains(note, tc.want) {
				t.Fatalf("note %q should mention %q", note, tc.want)
			}
			if out.SessionToken != "" {
				t.Fatal("must not invent session token")
			}
		})
	}
}

func TestApplyOptionalSTSRefreshFallbackSource(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_MINIO_STS", "0")
	t.Setenv("AI_CLOUDHUB_AWS_STS", "0")
	r := &provider.Resolved{
		Type:      provider.TypeR2,
		AccessKey: "ak",
		SecretKey: "sk",
		Endpoint:  "abc.r2.cloudflarestorage.com",
	}
	_, source, note := applyOptionalSTS(r, time.Hour, SourceRefresh)
	if source != SourceRefresh {
		t.Fatalf("source %s", source)
	}
	if note == "" {
		t.Fatal("expected r2 note")
	}
}

func TestIssueVendorNotesOnSession(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_MINIO_STS", "0")
	t.Setenv("AI_CLOUDHUB_AWS_STS", "0")
	s := New(time.Minute, "")
	sess, err := s.Issue(IssueInput{
		UserID: "u", DriveID: "d", MountPoint: "/m",
		Bucket: "b",
		Resolved: &provider.Resolved{
			Type:      provider.TypeB2,
			AccessKey: "ak",
			SecretKey: "sk",
			Endpoint:  "s3.us-west-000.backblazeb2.com",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sess.Source != SourceEmbedded {
		t.Fatalf("source %s", sess.Source)
	}
	if !strings.Contains(sess.Note, "B2") {
		t.Fatalf("note %q", sess.Note)
	}
}

func TestClampSTSDurationSeconds(t *testing.T) {
	if got := clampSTSDurationSeconds(time.Second); got != 900 {
		t.Fatalf("min %d", got)
	}
	if got := clampSTSDurationSeconds(time.Hour); got != 3600 {
		t.Fatalf("hour %d", got)
	}
	if got := clampSTSDurationSeconds(48 * time.Hour); got != 12*3600 {
		t.Fatalf("max %d", got)
	}
}
