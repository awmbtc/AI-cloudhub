package sts

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

func TestLooksLikeTencentRoleARN(t *testing.T) {
	if !looksLikeTencentRoleARN("qcs::cam::uin/1000:roleName/demo") {
		t.Fatal("expected tencent arn")
	}
	if !looksLikeTencentRoleARN("qcs::cam::uin/1000:role/demo") {
		t.Fatal("expected tencent arn form2")
	}
	if looksLikeTencentRoleARN("acs:ram::1:role/x") {
		t.Fatal("aliyun should not match")
	}
}

func TestTencentTC3AuthNonEmpty(t *testing.T) {
	auth, err := tencentTC3Auth("id", "key", "sts", "sts.tencentcloudapi.com", "AssumeRole", "2018-08-13", "ap-guangzhou", 1600000000, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(auth, "TC3-HMAC-SHA256 Credential=") {
		t.Fatal(auth)
	}
	if !strings.Contains(auth, "Signature=") {
		t.Fatal(auth)
	}
}

func TestTryTencentAssumeRoleMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-TC-Action") != "AssumeRole" {
			http.Error(w, "bad action", 400)
			return
		}
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "no auth", 400)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), "RoleArn") {
			http.Error(w, "no role", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"Response": map[string]interface{}{
				"Credentials": map[string]string{
					"TmpSecretId":  "AKIDtmp",
					"TmpSecretKey": "tmpsecret",
					"Token":        "ttok",
				},
				"ExpiredTime": time.Now().Add(time.Hour).Unix(),
				"RequestId":   "req-1",
			},
		})
	}))
	defer srv.Close()

	t.Setenv("AI_CLOUDHUB_TENCENT_STS_ENDPOINT", srv.URL)
	t.Setenv("AI_CLOUDHUB_COS_STS_ROLE_ARN", "qcs::cam::uin/1:roleName/demo")

	r := &provider.Resolved{
		Type:      provider.TypeCOS,
		AccessKey: "ak",
		SecretKey: "sk",
		Endpoint:  "cos.ap-guangzhou.myqcloud.com",
		Region:    "ap-guangzhou",
		UseSSL:    true,
	}
	ak, sk, tok, exp, err := TryTencentAssumeRole(r, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ak != "AKIDtmp" || sk != "tmpsecret" || tok != "ttok" {
		t.Fatalf("%s %s %s", ak, sk, tok)
	}
	if exp.IsZero() {
		t.Fatal("exp")
	}
}

func TestApplyOptionalCOSSTSNative(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"Response": map[string]interface{}{
				"Credentials": map[string]string{
					"TmpSecretId": "AKIDn", "TmpSecretKey": "skn", "Token": "tn",
				},
				"ExpiredTime": time.Now().Add(time.Hour).Unix(),
			},
		})
	}))
	defer srv.Close()
	t.Setenv("AI_CLOUDHUB_COS_NATIVE_STS", "1")
	t.Setenv("AI_CLOUDHUB_TENCENT_STS_ENDPOINT", srv.URL)
	t.Setenv("AI_CLOUDHUB_COS_STS_ROLE_ARN", "qcs::cam::uin/1:roleName/demo")
	t.Setenv("AI_CLOUDHUB_COS_STS", "0")
	t.Setenv("AI_CLOUDHUB_S3_STS", "0")

	r := &provider.Resolved{Type: provider.TypeCOS, AccessKey: "ak", SecretKey: "sk", Endpoint: "cos.example", Region: "ap-guangzhou"}
	out, source, note := applyOptionalCOSSTS(r, time.Hour, SourceEmbedded)
	if source != SourceTencentSTS || note != "" {
		t.Fatalf("source=%s note=%s", source, note)
	}
	if out.AccessKey != "AKIDn" || out.SessionToken != "tn" {
		t.Fatalf("%+v", out)
	}
}

func TestTryTencentAssumeRoleRequiresARN(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_COS_STS_ROLE_ARN", "")
	t.Setenv("AI_CLOUDHUB_TENCENT_STS_ROLE_ARN", "")
	t.Setenv("AI_CLOUDHUB_S3_STS_ROLE_ARN", "")
	_, _, _, _, err := TryTencentAssumeRole(&provider.Resolved{AccessKey: "a", SecretKey: "b"}, time.Hour)
	if err == nil || !strings.Contains(err.Error(), "ROLE_ARN") {
		t.Fatalf("err=%v", err)
	}
}

func TestApplyOptionalCOSSTSDisabledNote(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_COS_NATIVE_STS", "0")
	t.Setenv("AI_CLOUDHUB_TENCENT_STS", "0")
	t.Setenv("AI_CLOUDHUB_COS_STS", "0")
	t.Setenv("AI_CLOUDHUB_S3_STS", "0")
	t.Setenv("AI_CLOUDHUB_COS_STS_ROLE_ARN", "")
	r := &provider.Resolved{Type: provider.TypeCOS, AccessKey: "ak", SecretKey: "sk", Endpoint: "cos.example"}
	out, source, note := applyOptionalCOSSTS(r, time.Hour, SourceEmbedded)
	if source != SourceEmbedded || note == "" {
		t.Fatalf("source=%s note=%q", source, note)
	}
	if out != r {
		t.Fatal("expected same")
	}
}
