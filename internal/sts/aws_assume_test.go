package sts

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

func TestAWSSTSEnabled(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_AWS_STS", "")
	if awsSTSEnabled() {
		t.Fatal("empty disabled")
	}
	t.Setenv("AI_CLOUDHUB_AWS_STS", "1")
	if !awsSTSEnabled() {
		t.Fatal("1 enables")
	}
	t.Setenv("AI_CLOUDHUB_AWS_STS", "yes")
	if !awsSTSEnabled() {
		t.Fatal("yes enables")
	}
}

func TestLooksLikeAWS(t *testing.T) {
	cases := []struct {
		name string
		r    *provider.Resolved
		want bool
	}{
		{"nil", nil, false},
		{"label AWS", &provider.Resolved{ProviderLabel: "AWS", Endpoint: "custom"}, true},
		{"default host", &provider.Resolved{Endpoint: "s3.amazonaws.com"}, true},
		{"empty ep", &provider.Resolved{Endpoint: ""}, true},
		{"regional", &provider.Resolved{Endpoint: "s3.us-west-2.amazonaws.com"}, true},
		{"hyphen region", &provider.Resolved{Endpoint: "s3-eu-west-1.amazonaws.com"}, true},
		{"virtual hosted", &provider.Resolved{Endpoint: "mybucket.s3.amazonaws.com"}, true},
		{"china", &provider.Resolved{Endpoint: "s3.cn-north-1.amazonaws.com.cn"}, true},
		{"minio custom", &provider.Resolved{Endpoint: "127.0.0.1:9000"}, false},
		{"r2", &provider.Resolved{Endpoint: "abc.r2.cloudflarestorage.com"}, false},
		{"b2", &provider.Resolved{Endpoint: "s3.us-west-000.backblazeb2.com"}, false},
		{"lambda not s3-ish", &provider.Resolved{Endpoint: "lambda.us-east-1.amazonaws.com"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeAWS(tc.r); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestAWSSTSEndpointURL(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_AWS_STS_ENDPOINT", "")
	if got := awsSTSEndpointURL("us-east-1"); got != "https://sts.amazonaws.com" {
		t.Fatalf("us-east-1: %s", got)
	}
	if got := awsSTSEndpointURL(""); got != "https://sts.amazonaws.com" {
		t.Fatalf("empty: %s", got)
	}
	if got := awsSTSEndpointURL("eu-west-1"); got != "https://sts.eu-west-1.amazonaws.com" {
		t.Fatalf("eu-west-1: %s", got)
	}
	if got := awsSTSEndpointURL("cn-north-1"); got != "https://sts.cn-north-1.amazonaws.com.cn" {
		t.Fatalf("cn: %s", got)
	}
	t.Setenv("AI_CLOUDHUB_AWS_STS_ENDPOINT", "http://127.0.0.1:9999/")
	if got := awsSTSEndpointURL("us-east-1"); got != "http://127.0.0.1:9999" {
		t.Fatalf("override: %s", got)
	}
}

func TestTryAWSAssumeRoleValidation(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_AWS_STS_ROLE_ARN", "")
	_, _, _, _, err := TryAWSAssumeRole(nil, time.Hour)
	if err == nil {
		t.Fatal("nil resolved")
	}
	_, _, _, _, err = TryAWSAssumeRole(&provider.Resolved{AccessKey: "a", SecretKey: "b"}, time.Hour)
	if err == nil || !strings.Contains(err.Error(), "AI_CLOUDHUB_AWS_STS_ROLE_ARN") {
		t.Fatalf("want role arn error, got %v", err)
	}
}

func TestTryAWSAssumeRoleHTTPSuccess(t *testing.T) {
	const body = `<?xml version="1.0" encoding="UTF-8"?>
<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <AssumeRoleResult>
    <Credentials>
      <AccessKeyId>ASIA_AWS_AK</AccessKeyId>
      <SecretAccessKey>aws-temp-secret</SecretAccessKey>
      <SessionToken>aws-temp-token</SessionToken>
      <Expiration>2099-03-01T00:00:00Z</Expiration>
    </Credentials>
  </AssumeRoleResult>
</AssumeRoleResponse>`
	var sawRole bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		_ = r.ParseForm()
		if r.Form.Get("RoleArn") == "arn:aws:iam::123456789012:role/HubRole" {
			sawRole = true
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	t.Setenv("AI_CLOUDHUB_AWS_STS_ENDPOINT", srv.URL)
	t.Setenv("AI_CLOUDHUB_AWS_STS_ROLE_ARN", "arn:aws:iam::123456789012:role/HubRole")
	r := &provider.Resolved{
		Type:          provider.TypeS3,
		AccessKey:     "AKIApermanent",
		SecretKey:     "permanent-secret",
		Endpoint:      "s3.amazonaws.com",
		Region:        "us-east-1",
		UseSSL:        true,
		ProviderLabel: "AWS",
	}
	ak, sk, tok, exp, err := TryAWSAssumeRole(r, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !sawRole {
		t.Fatal("expected RoleArn in form POST")
	}
	if ak != "ASIA_AWS_AK" || sk != "aws-temp-secret" || tok != "aws-temp-token" {
		t.Fatalf("creds %s %s %s", ak, sk, tok)
	}
	if exp.Year() != 2099 {
		t.Fatalf("exp %v", exp)
	}
}

func TestApplyOptionalAWSSTSSuccess(t *testing.T) {
	const body = `<?xml version="1.0" encoding="UTF-8"?>
<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <AssumeRoleResult>
    <Credentials>
      <AccessKeyId>TEMP_AWS_AK</AccessKeyId>
      <SecretAccessKey>TEMP_AWS_SK</SecretAccessKey>
      <SessionToken>TEMP_AWS_TOK</SessionToken>
      <Expiration>2099-06-01T00:00:00Z</Expiration>
    </Credentials>
  </AssumeRoleResult>
</AssumeRoleResponse>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	t.Setenv("AI_CLOUDHUB_AWS_STS", "1")
	t.Setenv("AI_CLOUDHUB_AWS_STS_ENDPOINT", srv.URL)
	t.Setenv("AI_CLOUDHUB_AWS_STS_ROLE_ARN", "arn:aws:iam::1:role/r")
	orig := &provider.Resolved{
		Type:          provider.TypeS3,
		AccessKey:     "ak",
		SecretKey:     "sk",
		Endpoint:      "s3.amazonaws.com",
		Region:        "us-east-1",
		ProviderLabel: "AWS",
	}
	out, source, note := applyOptionalAWSSTS(orig, time.Hour, SourceEmbedded)
	if source != SourceAWSSTS {
		t.Fatalf("source %s", source)
	}
	if note != "" {
		t.Fatalf("note %q", note)
	}
	if out.AccessKey != "TEMP_AWS_AK" || out.SessionToken != "TEMP_AWS_TOK" {
		t.Fatalf("out %+v", out)
	}
	if orig.AccessKey != "ak" {
		t.Fatal("orig mutated")
	}
}

func TestApplyOptionalAWSSTSFailFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	defer srv.Close()

	t.Setenv("AI_CLOUDHUB_AWS_STS", "1")
	t.Setenv("AI_CLOUDHUB_AWS_STS_ENDPOINT", srv.URL)
	t.Setenv("AI_CLOUDHUB_AWS_STS_ROLE_ARN", "arn:aws:iam::1:role/r")
	orig := &provider.Resolved{
		Type:          provider.TypeS3,
		AccessKey:     "ak",
		SecretKey:     "sk",
		Endpoint:      "s3.amazonaws.com",
		ProviderLabel: "AWS",
	}
	out, source, note := applyOptionalAWSSTS(orig, time.Hour, SourceEmbedded)
	if source != SourceEmbedded || out.AccessKey != "ak" {
		t.Fatalf("want embedded fallback, got source=%s out=%+v", source, out)
	}
	if !strings.Contains(note, "failed") {
		t.Fatalf("note %q", note)
	}
}

func TestApplyOptionalAWSSTSSkipNonAWSEndpoint(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_AWS_STS", "1")
	t.Setenv("AI_CLOUDHUB_AWS_STS_ROLE_ARN", "arn:aws:iam::1:role/r")
	r := &provider.Resolved{
		Type:      provider.TypeS3,
		AccessKey: "a",
		SecretKey: "b",
		Endpoint:  "minio.local:9000",
	}
	out, source, note := applyOptionalAWSSTS(r, time.Hour, SourceEmbedded)
	if source != SourceEmbedded || out != r {
		t.Fatal("custom S3 must not call AWS STS")
	}
	if !strings.Contains(note, "not AWS") {
		t.Fatalf("note %q", note)
	}
}

func TestApplyOptionalAWSSTSDisabled(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_AWS_STS", "0")
	r := &provider.Resolved{
		Type:          provider.TypeS3,
		AccessKey:     "a",
		SecretKey:     "b",
		Endpoint:      "s3.amazonaws.com",
		ProviderLabel: "AWS",
	}
	out, source, note := applyOptionalAWSSTS(r, time.Hour, SourceEmbedded)
	if source != SourceEmbedded || out != r || note != "" {
		t.Fatalf("disabled should be silent embedded")
	}
}

func TestIssueWithAWSSTS(t *testing.T) {
	const body = `<?xml version="1.0" encoding="UTF-8"?>
<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <AssumeRoleResult>
    <Credentials>
      <AccessKeyId>ISSUE_AWS_AK</AccessKeyId>
      <SecretAccessKey>ISSUE_AWS_SK</SecretAccessKey>
      <SessionToken>ISSUE_AWS_TOK</SessionToken>
      <Expiration>2099-06-01T00:00:00Z</Expiration>
    </Credentials>
  </AssumeRoleResult>
</AssumeRoleResponse>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	t.Setenv("AI_CLOUDHUB_MINIO_STS", "0")
	t.Setenv("AI_CLOUDHUB_AWS_STS", "1")
	t.Setenv("AI_CLOUDHUB_AWS_STS_ENDPOINT", srv.URL)
	t.Setenv("AI_CLOUDHUB_AWS_STS_ROLE_ARN", "arn:aws:iam::1:role/r")

	s := New(time.Minute, "http://api")
	sess, err := s.Issue(IssueInput{
		UserID: "u", DriveID: "d", MountPoint: "/m",
		Bucket: "bucket",
		Resolved: &provider.Resolved{
			Type:          provider.TypeS3,
			AccessKey:     "ak",
			SecretKey:     "sk",
			Endpoint:      "s3.amazonaws.com",
			Region:        "us-east-1",
			UseSSL:        true,
			ProviderLabel: "AWS",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sess.Source != SourceAWSSTS {
		t.Fatalf("source %s", sess.Source)
	}
	if !strings.Contains(sess.Spec.RcloneConf, "access_key_id = ISSUE_AWS_AK") {
		t.Fatalf("conf: %s", sess.Spec.RcloneConf)
	}
	if !strings.Contains(sess.Spec.RcloneConf, "session_token = ISSUE_AWS_TOK") {
		t.Fatalf("conf missing token: %s", sess.Spec.RcloneConf)
	}
}
