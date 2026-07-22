package sts

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

// SourceTencentSTS labels temporary credentials from Tencent Cloud STS AssumeRole.
const SourceTencentSTS = "tencent_sts"

// tencentNativeSTSEnabled reports whether Tencent CAM STS should be preferred for COS.
// Set AI_CLOUDHUB_COS_NATIVE_STS=1 (or AI_CLOUDHUB_TENCENT_STS=1).
func tencentNativeSTSEnabled() bool {
	return envFlagTrue("AI_CLOUDHUB_COS_NATIVE_STS") || envFlagTrue("AI_CLOUDHUB_TENCENT_STS")
}

// tencentRoleARN returns RoleArn for Tencent STS.
func tencentRoleARN() string {
	for _, k := range []string{
		"AI_CLOUDHUB_TENCENT_STS_ROLE_ARN",
		"AI_CLOUDHUB_COS_STS_ROLE_ARN",
		"AI_CLOUDHUB_S3_STS_ROLE_ARN",
	} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// tencentSTSBase returns scheme://host for the STS API.
// Override with AI_CLOUDHUB_TENCENT_STS_ENDPOINT (host or full URL; tests may use http://).
func tencentSTSBase() (scheme, host string) {
	scheme = "https"
	host = "sts.tencentcloudapi.com"
	ep := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_TENCENT_STS_ENDPOINT"))
	if ep == "" {
		return scheme, host
	}
	if strings.HasPrefix(ep, "http://") {
		scheme = "http"
		ep = strings.TrimPrefix(ep, "http://")
	} else if strings.HasPrefix(ep, "https://") {
		scheme = "https"
		ep = strings.TrimPrefix(ep, "https://")
	}
	host = strings.TrimRight(ep, "/")
	return scheme, host
}

// looksLikeTencentRoleARN reports qcs::cam::... style ARNs.
func looksLikeTencentRoleARN(arn string) bool {
	arn = strings.TrimSpace(arn)
	return strings.HasPrefix(arn, "qcs::cam::") || strings.Contains(arn, ":roleName/")
}

// TryTencentAssumeRole calls Tencent Cloud STS AssumeRole (API 3.0, TC3-HMAC-SHA256).
// Requires RoleArn. Best-effort: callers fall back on error.
//
// Spec: https://www.tencentcloud.com/document/product/1312/48197
func TryTencentAssumeRole(r *provider.Resolved, duration time.Duration) (access, secret, token string, exp time.Time, err error) {
	if r == nil {
		return "", "", "", time.Time{}, fmt.Errorf("resolved provider required")
	}
	if r.AccessKey == "" || r.SecretKey == "" {
		return "", "", "", time.Time{}, fmt.Errorf("access_key and secret_key required for Tencent STS")
	}
	roleARN := tencentRoleARN()
	if roleARN == "" {
		return "", "", "", time.Time{}, fmt.Errorf("AI_CLOUDHUB_COS_STS_ROLE_ARN (or TENCENT) required for Tencent AssumeRole")
	}

	secs := clampSTSDurationSeconds(duration)
	// Tencent DurationSeconds: 0–43200 typically; clamp to 7200 for safety.
	if secs > 7200 {
		secs = 7200
	}

	region := strings.TrimSpace(r.Region)
	if region == "" {
		region = strings.TrimSpace(os.Getenv("AI_CLOUDHUB_TENCENT_STS_REGION"))
	}
	if region == "" {
		region = "ap-guangzhou"
	}

	payload := map[string]interface{}{
		"RoleArn":         roleARN,
		"RoleSessionName": "ai-cloudhub",
		"DurationSeconds": secs,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", "", time.Time{}, err
	}

	scheme, host := tencentSTSBase()
	service := "sts"
	action := "AssumeRole"
	version := "2018-08-13"
	timestamp := time.Now().UTC().Unix()
	auth, err := tencentTC3Auth(r.AccessKey, r.SecretKey, service, host, action, version, region, timestamp, body)
	if err != nil {
		return "", "", "", time.Time{}, err
	}

	req, err := http.NewRequest(http.MethodPost, scheme+"://"+host, bytes.NewReader(body))
	if err != nil {
		return "", "", "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Host", host)
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Version", version)
	req.Header.Set("X-TC-Timestamp", strconv.FormatInt(timestamp, 10))
	req.Header.Set("X-TC-Region", region)
	req.Header.Set("Authorization", auth)

	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", "", "", time.Time{}, fmt.Errorf("tencent STS request: %w", err)
	}
	defer res.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", "", "", time.Time{}, fmt.Errorf("tencent STS read: %w", err)
	}

	var parsed struct {
		Response struct {
			Credentials struct {
				TmpSecretId  string `json:"TmpSecretId"`
				TmpSecretKey string `json:"TmpSecretKey"`
				Token        string `json:"Token"`
			} `json:"Credentials"`
			ExpiredTime int64  `json:"ExpiredTime"`
			Expiration  string `json:"Expiration"`
			Error       *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
			RequestId string `json:"RequestId"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", "", "", time.Time{}, fmt.Errorf("tencent STS json: %w", err)
	}
	if parsed.Response.Error != nil {
		return "", "", "", time.Time{}, fmt.Errorf("tencent STS: %s %s", parsed.Response.Error.Code, parsed.Response.Error.Message)
	}
	if res.StatusCode >= 300 {
		return "", "", "", time.Time{}, fmt.Errorf("tencent STS HTTP %d: %s", res.StatusCode, truncateSTS(string(respBody), 256))
	}
	c := parsed.Response.Credentials
	if c.TmpSecretId == "" || c.TmpSecretKey == "" {
		return "", "", "", time.Time{}, fmt.Errorf("tencent STS: empty temporary credentials")
	}
	exp = time.Now().UTC().Add(time.Duration(secs) * time.Second)
	if parsed.Response.ExpiredTime > 0 {
		exp = time.Unix(parsed.Response.ExpiredTime, 0).UTC()
	} else if t, err := time.Parse(time.RFC3339, parsed.Response.Expiration); err == nil {
		exp = t.UTC()
	}
	return c.TmpSecretId, c.TmpSecretKey, c.Token, exp, nil
}

// tencentTC3Auth builds Authorization header for Tencent Cloud API 3.0.
func tencentTC3Auth(secretId, secretKey, service, host, action, version, region string, timestamp int64, payload []byte) (string, error) {
	algorithm := "TC3-HMAC-SHA256"
	date := time.Unix(timestamp, 0).UTC().Format("2006-01-02")

	// ************* 1. Canonical request *************
	httpRequestMethod := "POST"
	canonicalURI := "/"
	canonicalQueryString := ""
	ct := "application/json"
	canonicalHeaders := fmt.Sprintf("content-type:%s\nhost:%s\n", ct, host)
	signedHeaders := "content-type;host"
	h := sha256.Sum256(payload)
	hashedRequestPayload := hex.EncodeToString(h[:])
	canonicalRequest := strings.Join([]string{
		httpRequestMethod,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders,
		signedHeaders,
		hashedRequestPayload,
	}, "\n")

	// ************* 2. String to sign *************
	credentialScope := date + "/" + service + "/tc3_request"
	ch := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		algorithm,
		strconv.FormatInt(timestamp, 10),
		credentialScope,
		hex.EncodeToString(ch[:]),
	}, "\n")

	// ************* 3. Signature *************
	secretDate := tencentHMAC([]byte("TC3"+secretKey), date)
	secretService := tencentHMAC(secretDate, service)
	secretSigning := tencentHMAC(secretService, "tc3_request")
	signature := hex.EncodeToString(tencentHMAC(secretSigning, stringToSign))

	// ************* 4. Authorization *************
	auth := fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, secretId, credentialScope, signedHeaders, signature)
	_ = action
	_ = version
	_ = region
	return auth, nil
}

func tencentHMAC(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(data))
	return m.Sum(nil)
}

// applyOptionalCOSSTS prefers Tencent native CAM STS when enabled / RoleArn looks Tencent,
// then falls back to S3-compatible AssumeRole on the COS endpoint.
func applyOptionalCOSSTS(resolved *provider.Resolved, duration time.Duration, fallbackSource string) (out *provider.Resolved, source, note string) {
	if resolved == nil {
		return nil, fallbackSource, ""
	}
	if resolved.Type != provider.TypeCOS {
		return resolved, fallbackSource, ""
	}

	wantNative := tencentNativeSTSEnabled() || looksLikeTencentRoleARN(tencentRoleARN())
	if !wantNative && s3CompatSTSWanted(provider.TypeCOS) && looksLikeTencentRoleARN(tencentRoleARN()) {
		wantNative = true
	}
	if !wantNative && !s3CompatSTSWanted(provider.TypeCOS) {
		return resolved, fallbackSource, noteCOS
	}

	if wantNative && tencentRoleARN() != "" {
		ak, sk, tok, _, err := TryTencentAssumeRole(resolved, duration)
		if err == nil {
			cp := *resolved
			cp.AccessKey = ak
			cp.SecretKey = sk
			cp.SessionToken = tok
			return &cp, SourceTencentSTS, ""
		}
		if !s3CompatSTSWanted(provider.TypeCOS) {
			return resolved, fallbackSource,
				"Tencent STS AssumeRole failed; using embedded credentials in short-lived session"
		}
		out, source, note = applyOptionalS3CompatSTS(resolved, duration, fallbackSource, SourceS3STS, "COS", "")
		if source == fallbackSource {
			return resolved, fallbackSource,
				"Tencent native + S3-compatible STS failed; using embedded credentials in short-lived session"
		}
		return out, source, note
	}

	return applyOptionalS3CompatSTS(resolved, duration, fallbackSource, SourceS3STS, "COS", noteCOS)
}
