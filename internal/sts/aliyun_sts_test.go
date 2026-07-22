package sts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

func TestLooksLikeAliyunRoleARN(t *testing.T) {
	if !looksLikeAliyunRoleARN("acs:ram::123456:role/ai-cloudhub") {
		t.Fatal("expected aliyun arn")
	}
	if looksLikeAliyunRoleARN("arn:aws:iam::123:role/x") {
		t.Fatal("aws arn should not match")
	}
}

func TestAliyunRPCSignatureStable(t *testing.T) {
	params := map[string]string{
		"Action":           "AssumeRole",
		"Version":          "2015-04-01",
		"Format":           "JSON",
		"AccessKeyId":      "testid",
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureVersion": "1.0",
		"SignatureNonce":   "nonce1",
		"Timestamp":        "2020-01-01T00:00:00Z",
		"RoleArn":          "acs:ram::1:role/r",
		"RoleSessionName":  "ai-cloudhub",
		"DurationSeconds":  "900",
	}
	sig1 := aliyunRPCSignature("GET", params, "testsecret")
	sig2 := aliyunRPCSignature("GET", params, "testsecret")
	if sig1 == "" || sig1 != sig2 {
		t.Fatalf("sig unstable: %q %q", sig1, sig2)
	}
	// Different secret → different signature
	if aliyunRPCSignature("GET", params, "other") == sig1 {
		t.Fatal("expected different signature for different secret")
	}
}

func TestTryAliyunAssumeRoleMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("Action") != "AssumeRole" {
			http.Error(w, "bad action", 400)
			return
		}
		if r.URL.Query().Get("Signature") == "" {
			http.Error(w, "no sig", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"Credentials": map[string]string{
				"AccessKeyId":     "STS.temp",
				"AccessKeySecret": "tempsecret",
				"SecurityToken":   "tok",
				"Expiration":      time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
			},
		})
	}))
	defer srv.Close()

	t.Setenv("AI_CLOUDHUB_ALIYUN_STS_ENDPOINT", srv.URL)
	t.Setenv("AI_CLOUDHUB_OSS_STS_ROLE_ARN", "acs:ram::123:role/demo")
	r := &provider.Resolved{
		Type:      provider.TypeOSS,
		AccessKey: "ak",
		SecretKey: "sk",
		Endpoint:  "oss-cn-hangzhou.aliyuncs.com",
		UseSSL:    true,
	}
	ak, sk, tok, exp, err := TryAliyunAssumeRole(r, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ak != "STS.temp" || sk != "tempsecret" || tok != "tok" {
		t.Fatalf("%s %s %s", ak, sk, tok)
	}
	if exp.IsZero() {
		t.Fatal("exp")
	}
}

func TestTryAliyunAssumeRoleRequiresARN(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_OSS_STS_ROLE_ARN", "")
	t.Setenv("AI_CLOUDHUB_ALIYUN_STS_ROLE_ARN", "")
	t.Setenv("AI_CLOUDHUB_S3_STS_ROLE_ARN", "")
	_, _, _, _, err := TryAliyunAssumeRole(&provider.Resolved{AccessKey: "a", SecretKey: "b"}, time.Hour)
	if err == nil || !strings.Contains(err.Error(), "ROLE_ARN") {
		t.Fatalf("err=%v", err)
	}
}

func TestApplyOptionalOSSSTSNative(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"Credentials": map[string]string{
				"AccessKeyId": "STS.n", "AccessKeySecret": "skn", "SecurityToken": "tn",
				"Expiration": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
			},
		})
	}))
	defer srv.Close()
	t.Setenv("AI_CLOUDHUB_OSS_NATIVE_STS", "1")
	t.Setenv("AI_CLOUDHUB_ALIYUN_STS_ENDPOINT", srv.URL)
	t.Setenv("AI_CLOUDHUB_OSS_STS_ROLE_ARN", "acs:ram::9:role/r")
	// ensure S3 path not required
	t.Setenv("AI_CLOUDHUB_OSS_STS", "0")
	t.Setenv("AI_CLOUDHUB_S3_STS", "0")

	r := &provider.Resolved{Type: provider.TypeOSS, AccessKey: "ak", SecretKey: "sk", Endpoint: "oss.example"}
	out, source, note := applyOptionalOSSSTS(r, time.Hour, SourceEmbedded)
	if source != SourceAliyunSTS || note != "" {
		t.Fatalf("source=%s note=%s", source, note)
	}
	if out.AccessKey != "STS.n" || out.SessionToken != "tn" {
		t.Fatalf("%+v", out)
	}
}

func TestApplyOptionalOSSSTSDisabledNote(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_OSS_NATIVE_STS", "0")
	t.Setenv("AI_CLOUDHUB_ALIYUN_STS", "0")
	t.Setenv("AI_CLOUDHUB_OSS_STS", "0")
	t.Setenv("AI_CLOUDHUB_S3_STS", "0")
	t.Setenv("AI_CLOUDHUB_OSS_STS_ROLE_ARN", "")
	r := &provider.Resolved{Type: provider.TypeOSS, AccessKey: "ak", SecretKey: "sk", Endpoint: "oss.example"}
	out, source, note := applyOptionalOSSSTS(r, time.Hour, SourceEmbedded)
	if source != SourceEmbedded || note == "" {
		t.Fatalf("source=%s note=%q", source, note)
	}
	if out != r {
		t.Fatal("expected same resolved")
	}
}
