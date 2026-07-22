package sts

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

// SourceAliyunSTS labels temporary credentials from Aliyun RAM STS AssumeRole.
const SourceAliyunSTS = "aliyun_sts"

// aliyunNativeSTSEnabled reports whether Aliyun RAM STS should be preferred for OSS.
// Set AI_CLOUDHUB_OSS_NATIVE_STS=1 (or AI_CLOUDHUB_ALIYUN_STS=1).
func aliyunNativeSTSEnabled() bool {
	return envFlagTrue("AI_CLOUDHUB_OSS_NATIVE_STS") || envFlagTrue("AI_CLOUDHUB_ALIYUN_STS")
}

// aliyunRoleARN returns RoleArn for Aliyun STS.
// Prefers AI_CLOUDHUB_OSS_STS_ROLE_ARN / AI_CLOUDHUB_ALIYUN_STS_ROLE_ARN, then S3 generic.
func aliyunRoleARN() string {
	for _, k := range []string{
		"AI_CLOUDHUB_ALIYUN_STS_ROLE_ARN",
		"AI_CLOUDHUB_OSS_STS_ROLE_ARN",
		"AI_CLOUDHUB_S3_STS_ROLE_ARN",
	} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// aliyunSTSEndpoint returns the RAM STS API host URL.
// Override with AI_CLOUDHUB_ALIYUN_STS_ENDPOINT (tests / regional).
func aliyunSTSEndpoint() string {
	if ep := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_ALIYUN_STS_ENDPOINT")); ep != "" {
		return strings.TrimRight(ep, "/")
	}
	return "https://sts.aliyuncs.com"
}

// looksLikeAliyunRoleARN reports acs:ram::... style ARNs.
func looksLikeAliyunRoleARN(arn string) bool {
	arn = strings.TrimSpace(arn)
	return strings.HasPrefix(arn, "acs:ram::")
}

// TryAliyunAssumeRole calls Aliyun RAM STS AssumeRole (RPC JSON API, HMAC-SHA1).
// Requires RoleArn (acs:ram::...). Best-effort: callers fall back on error.
//
// Spec: https://www.alibabacloud.com/help/en/ram/developer-reference/api-sts-2015-04-01-assumerole
func TryAliyunAssumeRole(r *provider.Resolved, duration time.Duration) (access, secret, token string, exp time.Time, err error) {
	if r == nil {
		return "", "", "", time.Time{}, fmt.Errorf("resolved provider required")
	}
	if r.AccessKey == "" || r.SecretKey == "" {
		return "", "", "", time.Time{}, fmt.Errorf("access_key and secret_key required for Aliyun STS")
	}
	roleARN := aliyunRoleARN()
	if roleARN == "" {
		return "", "", "", time.Time{}, fmt.Errorf("AI_CLOUDHUB_OSS_STS_ROLE_ARN (or ALIYUN) required for Aliyun AssumeRole")
	}

	secs := clampSTSDurationSeconds(duration)
	// Aliyun STS DurationSeconds: typically 900–3600 (max depends on role).
	if secs > 3600 {
		secs = 3600
	}

	params := map[string]string{
		"Action":           "AssumeRole",
		"Version":          "2015-04-01",
		"Format":           "JSON",
		"AccessKeyId":      r.AccessKey,
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureVersion": "1.0",
		"SignatureNonce":   fmt.Sprintf("%d", time.Now().UnixNano()),
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"RoleArn":          roleARN,
		"RoleSessionName":  "ai-cloudhub",
		"DurationSeconds":  fmt.Sprintf("%d", secs),
	}
	// Optional policy / external id style not required for basic AssumeRole.

	params["Signature"] = aliyunRPCSignature("GET", params, r.SecretKey)

	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	endpoint := aliyunSTSEndpoint()
	reqURL := endpoint + "/?" + q.Encode()

	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Get(reqURL)
	if err != nil {
		return "", "", "", time.Time{}, fmt.Errorf("aliyun STS request: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", "", "", time.Time{}, fmt.Errorf("aliyun STS read: %w", err)
	}
	if res.StatusCode >= 300 {
		return "", "", "", time.Time{}, fmt.Errorf("aliyun STS HTTP %d: %s", res.StatusCode, truncateSTS(string(body), 256))
	}

	var parsed struct {
		Credentials struct {
			AccessKeyId     string `json:"AccessKeyId"`
			AccessKeySecret string `json:"AccessKeySecret"`
			SecurityToken   string `json:"SecurityToken"`
			Expiration      string `json:"Expiration"`
		} `json:"Credentials"`
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", "", "", time.Time{}, fmt.Errorf("aliyun STS json: %w", err)
	}
	if parsed.Credentials.AccessKeyId == "" || parsed.Credentials.AccessKeySecret == "" {
		if parsed.Code != "" || parsed.Message != "" {
			return "", "", "", time.Time{}, fmt.Errorf("aliyun STS: %s %s", parsed.Code, parsed.Message)
		}
		return "", "", "", time.Time{}, fmt.Errorf("aliyun STS: empty temporary credentials")
	}
	exp = time.Now().UTC().Add(time.Duration(secs) * time.Second)
	if t, err := time.Parse(time.RFC3339, parsed.Credentials.Expiration); err == nil {
		exp = t.UTC()
	}
	return parsed.Credentials.AccessKeyId, parsed.Credentials.AccessKeySecret, parsed.Credentials.SecurityToken, exp, nil
}

// aliyunRPCSignature computes Aliyun OpenAPI RPC HMAC-SHA1 signature.
func aliyunRPCSignature(method string, params map[string]string, accessKeySecret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "Signature" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var pairs []string
	for _, k := range keys {
		pairs = append(pairs, aliyunPercentEncode(k)+"="+aliyunPercentEncode(params[k]))
	}
	canonicalized := strings.Join(pairs, "&")
	stringToSign := method + "&" + aliyunPercentEncode("/") + "&" + aliyunPercentEncode(canonicalized)
	mac := hmac.New(sha1.New, []byte(accessKeySecret+"&"))
	_, _ = mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// aliyunPercentEncode is Aliyun's special percent-encoding for signing.
func aliyunPercentEncode(s string) string {
	// url.QueryEscape then Aliyun replacements
	enc := url.QueryEscape(s)
	enc = strings.ReplaceAll(enc, "+", "%20")
	enc = strings.ReplaceAll(enc, "*", "%2A")
	enc = strings.ReplaceAll(enc, "%7E", "~")
	return enc
}

func truncateSTS(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// applyOptionalOSSSTS prefers Aliyun native RAM STS when enabled / RoleArn looks Aliyun,
// then falls back to S3-compatible AssumeRole on the OSS endpoint.
func applyOptionalOSSSTS(resolved *provider.Resolved, duration time.Duration, fallbackSource string) (out *provider.Resolved, source, note string) {
	if resolved == nil {
		return nil, fallbackSource, ""
	}
	if resolved.Type != provider.TypeOSS {
		return resolved, fallbackSource, ""
	}

	wantNative := aliyunNativeSTSEnabled() || looksLikeAliyunRoleARN(aliyunRoleARN())
	// Native also when OSS STS flag is on and RoleArn is set (prefer RAM over S3-compat).
	if !wantNative && s3CompatSTSWanted(provider.TypeOSS) && looksLikeAliyunRoleARN(aliyunRoleARN()) {
		wantNative = true
	}
	// If only native flag on (without generic OSS_STS), still try native.
	if !wantNative && !s3CompatSTSWanted(provider.TypeOSS) {
		return resolved, fallbackSource, noteOSS
	}

	if wantNative && aliyunRoleARN() != "" {
		ak, sk, tok, _, err := TryAliyunAssumeRole(resolved, duration)
		if err == nil {
			cp := *resolved
			cp.AccessKey = ak
			cp.SecretKey = sk
			cp.SessionToken = tok
			return &cp, SourceAliyunSTS, ""
		}
		// Fall through to S3-compat if also wanted; else note failure.
		if !s3CompatSTSWanted(provider.TypeOSS) {
			return resolved, fallbackSource,
				"Aliyun STS AssumeRole failed; using embedded credentials in short-lived session"
		}
		// continue to S3-compat below with context that native failed
		out, source, note = applyOptionalS3CompatSTS(resolved, duration, fallbackSource, SourceS3STS, "OSS", "")
		if note == "" && source == SourceS3STS {
			return out, source, ""
		}
		if source == fallbackSource {
			return resolved, fallbackSource,
				"Aliyun native + S3-compatible STS failed; using embedded credentials in short-lived session"
		}
		return out, source, note
	}

	return applyOptionalS3CompatSTS(resolved, duration, fallbackSource, SourceS3STS, "OSS", noteOSS)
}
