package sts

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

const assumeRoleXMLOK = `<?xml version="1.0" encoding="UTF-8"?>
<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <AssumeRoleResult>
    <Credentials>
      <AccessKeyId>S3STS_AK</AccessKeyId>
      <SecretAccessKey>S3STS_SK</SecretAccessKey>
      <SessionToken>S3STS_TOK</SessionToken>
      <Expiration>2099-06-01T00:00:00Z</Expiration>
    </Credentials>
  </AssumeRoleResult>
</AssumeRoleResponse>`

func clearSTSFlags(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"AI_CLOUDHUB_S3_STS", "AI_CLOUDHUB_MINIO_STS", "AI_CLOUDHUB_AWS_STS",
		"AI_CLOUDHUB_B2_STS", "AI_CLOUDHUB_OSS_STS", "AI_CLOUDHUB_COS_STS",
		"AI_CLOUDHUB_QINIU_STS", "AI_CLOUDHUB_ORACLE_STS", "AI_CLOUDHUB_R2_STS",
		"AI_CLOUDHUB_S3_STS_ROLE_ARN", "AI_CLOUDHUB_MINIO_STS_ROLE_ARN",
		"AI_CLOUDHUB_B2_STS_ROLE_ARN", "AI_CLOUDHUB_OSS_STS_ROLE_ARN",
		"AI_CLOUDHUB_COS_STS_ROLE_ARN", "AI_CLOUDHUB_QINIU_STS_ROLE_ARN",
		"AI_CLOUDHUB_ORACLE_STS_ROLE_ARN", "AI_CLOUDHUB_R2_STS_ROLE_ARN",
		"AI_CLOUDHUB_AWS_STS_ROLE_ARN", "AI_CLOUDHUB_AWS_STS_ENDPOINT",
	} {
		t.Setenv(k, "")
	}
}

func TestS3STSEnabled(t *testing.T) {
	clearSTSFlags(t)
	if s3STSEnabled() {
		t.Fatal("empty disabled")
	}
	t.Setenv("AI_CLOUDHUB_S3_STS", "1")
	if !s3STSEnabled() {
		t.Fatal("1 enables")
	}
	t.Setenv("AI_CLOUDHUB_S3_STS", "yes")
	if !s3STSEnabled() {
		t.Fatal("yes enables")
	}
}

func TestVendorSTSEnabled(t *testing.T) {
	clearSTSFlags(t)
	if vendorSTSEnabled("B2") {
		t.Fatal("off")
	}
	t.Setenv("AI_CLOUDHUB_B2_STS", "true")
	if !vendorSTSEnabled("B2") {
		t.Fatal("B2 true")
	}
	if !vendorSTSEnabled("b2") {
		t.Fatal("case insensitive vendor")
	}
}

func TestS3CompatSTSWanted(t *testing.T) {
	clearSTSFlags(t)
	if s3CompatSTSWanted(provider.TypeB2) {
		t.Fatal("b2 off")
	}
	t.Setenv("AI_CLOUDHUB_B2_STS", "1")
	if !s3CompatSTSWanted(provider.TypeB2) {
		t.Fatal("b2 vendor flag")
	}
	clearSTSFlags(t)
	t.Setenv("AI_CLOUDHUB_S3_STS", "1")
	for _, typ := range []provider.Type{
		provider.TypeMinIO, provider.TypeB2, provider.TypeOSS, provider.TypeCOS,
		provider.TypeQiniu, provider.TypeOracle, provider.TypeR2, provider.TypeS3,
	} {
		if !s3CompatSTSWanted(typ) {
			t.Fatalf("generic S3_STS should enable %s", typ)
		}
	}
	clearSTSFlags(t)
	t.Setenv("AI_CLOUDHUB_MINIO_STS", "1")
	if !s3CompatSTSWanted(provider.TypeMinIO) {
		t.Fatal("minio flag")
	}
	if s3CompatSTSWanted(provider.TypeB2) {
		t.Fatal("minio flag must not enable b2")
	}
}

func TestRoleARNForType(t *testing.T) {
	clearSTSFlags(t)
	if roleARNForType(provider.TypeB2) != "" {
		t.Fatal("empty")
	}
	t.Setenv("AI_CLOUDHUB_S3_STS_ROLE_ARN", "arn:generic")
	if got := roleARNForType(provider.TypeOSS); got != "arn:generic" {
		t.Fatalf("generic fallback %s", got)
	}
	t.Setenv("AI_CLOUDHUB_OSS_STS_ROLE_ARN", "arn:oss")
	if got := roleARNForType(provider.TypeOSS); got != "arn:oss" {
		t.Fatalf("vendor prefer %s", got)
	}
	t.Setenv("AI_CLOUDHUB_MINIO_STS_ROLE_ARN", "arn:minio")
	if got := roleARNForType(provider.TypeMinIO); got != "arn:minio" {
		t.Fatalf("minio %s", got)
	}
}

func TestTryS3AssumeRoleValidation(t *testing.T) {
	_, _, _, _, err := TryS3AssumeRole(nil, time.Hour, "")
	if err == nil {
		t.Fatal("nil")
	}
	_, _, _, _, err = TryS3AssumeRole(&provider.Resolved{AccessKey: "a", SecretKey: "b"}, time.Hour, "")
	if err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("endpoint: %v", err)
	}
}

func TestTryS3AssumeRoleHTTPSuccess(t *testing.T) {
	var sawRole string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		sawRole = r.Form.Get("RoleArn")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(assumeRoleXMLOK))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	r := &provider.Resolved{
		Type:      provider.TypeB2,
		AccessKey: "ak",
		SecretKey: "sk",
		Endpoint:  host,
		Region:    "us-west-000",
		UseSSL:    false,
	}
	ak, sk, tok, exp, err := TryS3AssumeRole(r, time.Hour, "arn:role/hub")
	if err != nil {
		t.Fatal(err)
	}
	if sawRole != "arn:role/hub" {
		t.Fatalf("RoleArn %q", sawRole)
	}
	if ak != "S3STS_AK" || sk != "S3STS_SK" || tok != "S3STS_TOK" {
		t.Fatalf("creds %s %s %s", ak, sk, tok)
	}
	if exp.Year() != 2099 {
		t.Fatalf("exp %v", exp)
	}
}

func TestApplyOptionalS3CompatSTSFlagsOff(t *testing.T) {
	clearSTSFlags(t)
	r := &provider.Resolved{
		Type:      provider.TypeB2,
		AccessKey: "ak",
		SecretKey: "sk",
		Endpoint:  "s3.us-west-000.backblazeb2.com",
	}
	out, source, note := applyOptionalS3CompatSTS(r, time.Hour, SourceEmbedded, SourceS3STS, "B2", noteB2)
	if source != SourceEmbedded || out != r {
		t.Fatalf("want embedded unchanged, source=%s", source)
	}
	if !strings.Contains(note, "B2") {
		t.Fatalf("note %q", note)
	}
	if out.SessionToken != "" {
		t.Fatal("no invented token")
	}
}

func TestApplyOptionalS3CompatSTSSuccessPerVendor(t *testing.T) {
	clearSTSFlags(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(assumeRoleXMLOK))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	vendors := []struct {
		typ  provider.Type
		flag string
	}{
		{provider.TypeB2, "AI_CLOUDHUB_B2_STS"},
		{provider.TypeOSS, "AI_CLOUDHUB_OSS_STS"},
		{provider.TypeCOS, "AI_CLOUDHUB_COS_STS"},
		{provider.TypeQiniu, "AI_CLOUDHUB_QINIU_STS"},
		{provider.TypeOracle, "AI_CLOUDHUB_ORACLE_STS"},
		{provider.TypeR2, "AI_CLOUDHUB_R2_STS"},
	}
	for _, v := range vendors {
		t.Run(string(v.typ), func(t *testing.T) {
			clearSTSFlags(t)
			t.Setenv(v.flag, "1")
			orig := &provider.Resolved{
				Type:      v.typ,
				AccessKey: "ak",
				SecretKey: "sk",
				Endpoint:  host,
				UseSSL:    false,
			}
			out, source, note := applyOptionalSTS(orig, time.Hour, SourceEmbedded)
			if source != SourceS3STS {
				t.Fatalf("source %s", source)
			}
			if note != "" {
				t.Fatalf("note %q", note)
			}
			if out.AccessKey != "S3STS_AK" || out.SessionToken != "S3STS_TOK" {
				t.Fatalf("out %+v", out)
			}
			if orig.AccessKey != "ak" {
				t.Fatal("orig mutated")
			}
		})
	}
}

func TestApplyOptionalS3CompatSTSGenericFlag(t *testing.T) {
	clearSTSFlags(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(assumeRoleXMLOK))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	t.Setenv("AI_CLOUDHUB_S3_STS", "1")
	orig := &provider.Resolved{
		Type:      provider.TypeOSS,
		AccessKey: "ak",
		SecretKey: "sk",
		Endpoint:  host,
		UseSSL:    false,
	}
	_, source, note := applyOptionalSTS(orig, time.Hour, SourceEmbedded)
	if source != SourceS3STS || note != "" {
		t.Fatalf("source=%s note=%q", source, note)
	}
}

func TestApplyOptionalS3CompatSTSFailFallsBack(t *testing.T) {
	clearSTSFlags(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusForbidden)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	t.Setenv("AI_CLOUDHUB_B2_STS", "1")
	orig := &provider.Resolved{
		Type:      provider.TypeB2,
		AccessKey: "ak",
		SecretKey: "sk",
		Endpoint:  host,
		UseSSL:    false,
	}
	out, source, note := applyOptionalSTS(orig, time.Hour, SourceEmbedded)
	if source != SourceEmbedded || out.AccessKey != "ak" {
		t.Fatalf("fallback source=%s out=%+v", source, out)
	}
	if !strings.Contains(note, "failed") {
		t.Fatalf("note %q", note)
	}
}

func TestCustomS3EndpointS3STS(t *testing.T) {
	clearSTSFlags(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(assumeRoleXMLOK))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	// flags off → silent embedded
	r := &provider.Resolved{
		Type:      provider.TypeS3,
		AccessKey: "ak",
		SecretKey: "sk",
		Endpoint:  host,
		UseSSL:    false,
	}
	out, source, note := applyOptionalSTS(r, time.Hour, SourceEmbedded)
	if source != SourceEmbedded || out != r || note != "" {
		t.Fatalf("custom s3 off: source=%s note=%q", source, note)
	}

	t.Setenv("AI_CLOUDHUB_S3_STS", "1")
	out, source, note = applyOptionalSTS(r, time.Hour, SourceEmbedded)
	if source != SourceS3STS {
		t.Fatalf("want s3_sts, got %s", source)
	}
	if note != "" || out.AccessKey != "S3STS_AK" {
		t.Fatalf("out source=%s note=%q ak=%s", source, note, out.AccessKey)
	}
}

func TestLooksLikeAWSStillRoutesToAWSSTS(t *testing.T) {
	clearSTSFlags(t)
	const body = `<?xml version="1.0" encoding="UTF-8"?>
<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <AssumeRoleResult>
    <Credentials>
      <AccessKeyId>AWS_ONLY_AK</AccessKeyId>
      <SecretAccessKey>AWS_ONLY_SK</SecretAccessKey>
      <SessionToken>AWS_ONLY_TOK</SessionToken>
      <Expiration>2099-06-01T00:00:00Z</Expiration>
    </Credentials>
  </AssumeRoleResult>
</AssumeRoleResponse>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	// Both S3_STS and AWS_STS on: AWS-looking endpoint must use aws_sts, not s3_sts.
	t.Setenv("AI_CLOUDHUB_S3_STS", "1")
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
	out, source, note := applyOptionalSTS(orig, time.Hour, SourceEmbedded)
	if source != SourceAWSSTS {
		t.Fatalf("want aws_sts, got %s", source)
	}
	if note != "" {
		t.Fatalf("note %q", note)
	}
	if out.AccessKey != "AWS_ONLY_AK" {
		t.Fatalf("out %+v", out)
	}
}

func TestMinioViaGenericS3STSFlag(t *testing.T) {
	clearSTSFlags(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(assumeRoleXMLOK))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	t.Setenv("AI_CLOUDHUB_S3_STS", "1")
	// MINIO_STS intentionally off
	orig := &provider.Resolved{
		Type:      provider.TypeMinIO,
		AccessKey: "ak",
		SecretKey: "sk",
		Endpoint:  host,
		UseSSL:    false,
	}
	out, source, note := applyOptionalSTS(orig, time.Hour, SourceEmbedded)
	if source != SourceMinioSTS {
		t.Fatalf("minio should still label minio_sts, got %s", source)
	}
	if note != "" || out.AccessKey != "S3STS_AK" {
		t.Fatalf("out source=%s note=%q ak=%s", source, note, out.AccessKey)
	}
}

func TestIssueVendorFlagsOffEmbedded(t *testing.T) {
	clearSTSFlags(t)
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

func TestIssueWithS3STS(t *testing.T) {
	clearSTSFlags(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(assumeRoleXMLOK))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	t.Setenv("AI_CLOUDHUB_OSS_STS", "1")

	s := New(time.Minute, "http://api")
	sess, err := s.Issue(IssueInput{
		UserID: "u", DriveID: "d", MountPoint: "/m",
		Bucket: "bucket",
		Resolved: &provider.Resolved{
			Type:      provider.TypeOSS,
			AccessKey: "ak",
			SecretKey: "sk",
			Endpoint:  host,
			UseSSL:    false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sess.Source != SourceS3STS {
		t.Fatalf("source %s", sess.Source)
	}
	if !strings.Contains(sess.Spec.RcloneConf, "access_key_id = S3STS_AK") {
		t.Fatalf("conf: %s", sess.Spec.RcloneConf)
	}
	if !strings.Contains(sess.Spec.RcloneConf, "session_token = S3STS_TOK") {
		t.Fatalf("conf missing token: %s", sess.Spec.RcloneConf)
	}
}
