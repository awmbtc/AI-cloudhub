package sts

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

func TestSTSEndpointURL(t *testing.T) {
	r := &provider.Resolved{Endpoint: "127.0.0.1:9000", UseSSL: false}
	if got := stsEndpointURL(r); got != "http://127.0.0.1:9000" {
		t.Fatalf("got %s", got)
	}
	r.UseSSL = true
	if got := stsEndpointURL(r); got != "https://127.0.0.1:9000" {
		t.Fatalf("got %s", got)
	}
	r.Endpoint = "https://minio.example.com"
	if got := stsEndpointURL(r); got != "https://minio.example.com" {
		t.Fatalf("got %s", got)
	}
}

func TestMinioSTSEnabled(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_MINIO_STS", "")
	if minioSTSEnabled() {
		t.Fatal("empty should be disabled")
	}
	t.Setenv("AI_CLOUDHUB_MINIO_STS", "1")
	if !minioSTSEnabled() {
		t.Fatal("1 should enable")
	}
	t.Setenv("AI_CLOUDHUB_MINIO_STS", "true")
	if !minioSTSEnabled() {
		t.Fatal("true should enable")
	}
}

func TestTryMinioAssumeRoleValidation(t *testing.T) {
	_, _, _, _, err := TryMinioAssumeRole(nil, time.Hour)
	if err == nil {
		t.Fatal("nil resolved")
	}
	_, _, _, _, err = TryMinioAssumeRole(&provider.Resolved{AccessKey: "a", SecretKey: "b"}, time.Hour)
	if err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("want endpoint error, got %v", err)
	}
}

func TestTryMinioAssumeRoleHTTPSuccess(t *testing.T) {
	// Minimal STS AssumeRole XML response shape accepted by minio-go.
	const body = `<?xml version="1.0" encoding="UTF-8"?>
<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <AssumeRoleResult>
    <Credentials>
      <AccessKeyId>ASIA_TEMP_AK</AccessKeyId>
      <SecretAccessKey>temp-secret</SecretAccessKey>
      <SessionToken>temp-token</SessionToken>
      <Expiration>2099-01-01T00:00:00Z</Expiration>
    </Credentials>
  </AssumeRoleResult>
</AssumeRoleResponse>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	// host without scheme; UseSSL=false → http://
	host := strings.TrimPrefix(srv.URL, "http://")
	r := &provider.Resolved{
		Type:      provider.TypeMinIO,
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
		Endpoint:  host,
		Region:    "us-east-1",
		UseSSL:    false,
	}
	ak, sk, tok, exp, err := TryMinioAssumeRole(r, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ak != "ASIA_TEMP_AK" || sk != "temp-secret" || tok != "temp-token" {
		t.Fatalf("creds %s %s %s", ak, sk, tok)
	}
	if exp.Year() != 2099 {
		t.Fatalf("exp %v", exp)
	}
}

func TestTryMinioAssumeRoleHTTPErrorFallsThroughApply(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no sts", http.StatusForbidden)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	t.Setenv("AI_CLOUDHUB_MINIO_STS", "1")
	orig := &provider.Resolved{
		Type:      provider.TypeMinIO,
		AccessKey: "ak",
		SecretKey: "sk",
		Endpoint:  host,
		UseSSL:    false,
	}
	out, source, note := applyOptionalMinioSTS(orig, time.Hour, SourceEmbedded)
	if source != SourceEmbedded {
		t.Fatalf("want fallback embedded, got %s", source)
	}
	if out.AccessKey != "ak" || out.SessionToken != "" {
		t.Fatalf("should keep embedded keys: %+v", out)
	}
	if !strings.Contains(note, "failed") {
		t.Fatalf("want failure note, got %q", note)
	}
}

func TestApplyOptionalMinioSTSSuccess(t *testing.T) {
	const body = `<?xml version="1.0" encoding="UTF-8"?>
<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <AssumeRoleResult>
    <Credentials>
      <AccessKeyId>TEMP_AK</AccessKeyId>
      <SecretAccessKey>TEMP_SK</SecretAccessKey>
      <SessionToken>TEMP_TOK</SessionToken>
      <Expiration>2099-06-01T00:00:00Z</Expiration>
    </Credentials>
  </AssumeRoleResult>
</AssumeRoleResponse>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	t.Setenv("AI_CLOUDHUB_MINIO_STS", "1")
	orig := &provider.Resolved{
		Type:      provider.TypeMinIO,
		AccessKey: "ak",
		SecretKey: "sk",
		Endpoint:  host,
		Region:    "us-east-1",
		UseSSL:    false,
	}
	out, source, note := applyOptionalMinioSTS(orig, time.Hour, SourceEmbedded)
	if source != SourceMinioSTS {
		t.Fatalf("source %s", source)
	}
	if note != "" {
		t.Fatalf("success should have empty note, got %q", note)
	}
	if out.AccessKey != "TEMP_AK" || out.SecretKey != "TEMP_SK" || out.SessionToken != "TEMP_TOK" {
		t.Fatalf("out %+v", out)
	}
	// original must not be mutated
	if orig.AccessKey != "ak" || orig.SessionToken != "" {
		t.Fatalf("orig mutated: %+v", orig)
	}
}

func TestApplyOptionalMinioSTSSkippedForNonMinio(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_MINIO_STS", "1")
	r := &provider.Resolved{Type: provider.TypeS3, AccessKey: "a", SecretKey: "b", Endpoint: "s3.amazonaws.com"}
	out, source, note := applyOptionalMinioSTS(r, time.Hour, SourceEmbedded)
	if source != SourceEmbedded || out != r || note != "" {
		t.Fatalf("s3 should skip MinIO STS attempt")
	}
}

func TestIssueWithMinioSTS(t *testing.T) {
	const body = `<?xml version="1.0" encoding="UTF-8"?>
<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <AssumeRoleResult>
    <Credentials>
      <AccessKeyId>ISSUE_AK</AccessKeyId>
      <SecretAccessKey>ISSUE_SK</SecretAccessKey>
      <SessionToken>ISSUE_TOK</SessionToken>
      <Expiration>2099-06-01T00:00:00Z</Expiration>
    </Credentials>
  </AssumeRoleResult>
</AssumeRoleResponse>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	t.Setenv("AI_CLOUDHUB_MINIO_STS", "1")
	t.Setenv("AI_CLOUDHUB_AWS_STS", "0")
	s := New(time.Minute, "http://api")
	sess, err := s.Issue(IssueInput{
		UserID: "u", DriveID: "d", MountPoint: "/m",
		Bucket: "bucket",
		Resolved: &provider.Resolved{
			Type:           provider.TypeMinIO,
			AccessKey:      "ak",
			SecretKey:      "sk",
			Endpoint:       host,
			Region:         "us-east-1",
			ForcePathStyle: true,
			UseSSL:         false,
			ProviderLabel:  "Minio",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sess.Source != SourceMinioSTS {
		t.Fatalf("source %s", sess.Source)
	}
	if !strings.Contains(sess.Spec.RcloneConf, "access_key_id = ISSUE_AK") {
		t.Fatalf("conf: %s", sess.Spec.RcloneConf)
	}
	if !strings.Contains(sess.Spec.RcloneConf, "session_token = ISSUE_TOK") {
		t.Fatalf("conf missing token: %s", sess.Spec.RcloneConf)
	}
}
