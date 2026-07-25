package sts

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

// OCI Identity validation cache (short TTL) so session Issue does not hit the API every time.
var (
	ociValMu    sync.Mutex
	ociValCache = map[string]ociValEntry{}
)

type ociValEntry struct {
	user map[string]interface{}
	at   time.Time
}

func ociIAMCacheTTL() time.Duration {
	// AI_CLOUDHUB_OCI_IAM_CACHE_SEC (default 300). 0 disables cache.
	v := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_OCI_IAM_CACHE_SEC"))
	if v == "" {
		return 5 * time.Minute
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 5 * time.Minute
	}
	if n == 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

func ociCacheKey(key *OCIAPIKey) string {
	if key == nil {
		return ""
	}
	return key.TenancyOCID + "|" + key.UserOCID + "|" + key.Fingerprint + "|" + key.Region
}

// ClearOCIValidateCache drops cached Identity results (tests).
func ClearOCIValidateCache() {
	ociValMu.Lock()
	ociValCache = map[string]ociValEntry{}
	ociValMu.Unlock()
}

// SourceOCIIAM labels best-effort OCI API-key (private key) identity validation /
// session assist. Does not replace S3-compatible mount credentials when AK/SK present.
const SourceOCIIAM = "oci_iam"

// ociNativeIAMEnabled: AI_CLOUDHUB_ORACLE_NATIVE_IAM=1|true|yes
func ociNativeIAMEnabled() bool {
	return envFlagTrue("AI_CLOUDHUB_ORACLE_NATIVE_IAM") || envFlagTrue("AI_CLOUDHUB_OCI_IAM")
}

// OCIAPIKey holds RSA API signing material for OCI.
type OCIAPIKey struct {
	TenancyOCID string
	UserOCID    string
	Fingerprint string
	PrivateKey  *rsa.PrivateKey
	Region      string
}

// ParseOCIPrivateKeyPEM parses PKCS1 or PKCS8 RSA private key PEM.
func ParseOCIPrivateKeyPEM(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("oci: no PEM block in private key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("oci: parse private key: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("oci: private key is not RSA")
	}
	return rk, nil
}

// LoadOCIAPIKeyFromEnv builds OCIAPIKey from env (tests / advanced ops).
// AI_CLOUDHUB_OCI_TENANCY_OCID, AI_CLOUDHUB_OCI_USER_OCID, AI_CLOUDHUB_OCI_FINGERPRINT,
// AI_CLOUDHUB_OCI_PRIVATE_KEY_PEM (or path via AI_CLOUDHUB_OCI_PRIVATE_KEY_FILE), AI_CLOUDHUB_OCI_REGION
func LoadOCIAPIKeyFromEnv() (*OCIAPIKey, error) {
	tenancy := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_OCI_TENANCY_OCID"))
	user := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_OCI_USER_OCID"))
	fp := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_OCI_FINGERPRINT"))
	region := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_OCI_REGION"))
	if region == "" {
		region = "us-ashburn-1"
	}
	var pemBytes []byte
	if p := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_OCI_PRIVATE_KEY_FILE")); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		pemBytes = b
	} else {
		pemBytes = []byte(os.Getenv("AI_CLOUDHUB_OCI_PRIVATE_KEY_PEM"))
	}
	if tenancy == "" || user == "" || fp == "" || len(pemBytes) == 0 {
		return nil, fmt.Errorf("oci iam env incomplete (tenancy/user/fingerprint/private_key)")
	}
	key, err := ParseOCIPrivateKeyPEM(pemBytes)
	if err != nil {
		return nil, err
	}
	return &OCIAPIKey{TenancyOCID: tenancy, UserOCID: user, Fingerprint: fp, PrivateKey: key, Region: region}, nil
}

// OCISignHeaders builds the Authorization signature for an OCI REST request.
// dateRFC1123 e.g. time.Now().UTC().Format(http.TimeFormat)
func OCISignHeaders(key *OCIAPIKey, method, host, pathAndQuery, dateRFC1123 string, body []byte) (string, error) {
	if key == nil || key.PrivateKey == nil {
		return "", fmt.Errorf("oci key required")
	}
	method = strings.ToUpper(method)
	// (request-target) = method + " " + path
	// For OCI: signing string includes date, (request-target), host, and optionally x-content-sha256
	target := strings.ToLower(method) + " " + pathAndQuery
	var parts []string
	parts = append(parts, "date: "+dateRFC1123)
	parts = append(parts, "(request-target): "+target)
	parts = append(parts, "host: "+host)
	headers := "date (request-target) host"
	if len(body) > 0 || method == "POST" || method == "PUT" || method == "PATCH" {
		sum := sha256.Sum256(body)
		contentSHA := base64.StdEncoding.EncodeToString(sum[:])
		parts = append(parts, "x-content-sha256: "+contentSHA)
		parts = append(parts, fmt.Sprintf("content-length: %d", len(body)))
		parts = append(parts, "content-type: application/json")
		headers = "date (request-target) host x-content-sha256 content-length content-type"
	}
	signingString := strings.Join(parts, "\n")
	h := sha256.Sum256([]byte(signingString))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key.PrivateKey, crypto.SHA256, h[:])
	if err != nil {
		return "", err
	}
	sigB64 := base64.StdEncoding.EncodeToString(sig)
	keyID := key.TenancyOCID + "/" + key.UserOCID + "/" + key.Fingerprint
	return fmt.Sprintf(
		`Signature version="1",keyId="%s",algorithm="rsa-sha256",headers="%s",signature="%s"`,
		keyID, headers, sigB64,
	), nil
}

// TryOCIValidateUser calls GET /20160918/users/{userId} to validate API key material.
// Best-effort identity proof; does not mint S3 session keys.
// Successful results are cached briefly (AI_CLOUDHUB_OCI_IAM_CACHE_SEC, default 300).
// Override base with AI_CLOUDHUB_OCI_IDENTITY_ENDPOINT (tests).
func TryOCIValidateUser(key *OCIAPIKey) (map[string]interface{}, error) {
	if key == nil {
		return nil, fmt.Errorf("oci key required")
	}
	ck := ociCacheKey(key)
	ttl := ociIAMCacheTTL()
	if ttl > 0 && ck != "" {
		ociValMu.Lock()
		if e, ok := ociValCache[ck]; ok && time.Since(e.at) < ttl {
			cp := cloneStringMap(e.user)
			ociValMu.Unlock()
			return cp, nil
		}
		ociValMu.Unlock()
	}

	path := "/20160918/users/" + key.UserOCID
	base := fmt.Sprintf("https://identity.%s.oraclecloud.com", key.Region)
	if ep := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_OCI_IDENTITY_ENDPOINT")); ep != "" {
		base = strings.TrimRight(ep, "/")
	}
	full := base + path
	req, err := http.NewRequest(http.MethodGet, full, nil)
	if err != nil {
		return nil, err
	}
	host := req.URL.Host
	reqTarget := req.URL.RequestURI()
	date := time.Now().UTC().Format(http.TimeFormat)
	auth, err := OCISignHeaders(key, http.MethodGet, host, reqTarget, date, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("date", date)
	req.Header.Set("host", host)
	req.Header.Set("Authorization", auth)
	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oci identity: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("oci identity HTTP %d: %s", res.StatusCode, truncateSTS(string(body), 200))
	}
	var out map[string]interface{}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("oci identity json: %w", err)
	}
	if ttl > 0 && ck != "" {
		ociValMu.Lock()
		ociValCache[ck] = ociValEntry{user: cloneStringMap(out), at: time.Now()}
		ociValMu.Unlock()
	}
	return out, nil
}

func cloneStringMap(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// applyOptionalOracleSTS: S3-compat first; optional native IAM validation via private key env.
func applyOptionalOracleSTS(resolved *provider.Resolved, duration time.Duration, fallbackSource string) (out *provider.Resolved, source, note string) {
	if resolved == nil {
		return nil, fallbackSource, ""
	}
	if resolved.Type != provider.TypeOracle {
		return resolved, fallbackSource, ""
	}

	// S3-compatible path when enabled.
	var s3out *provider.Resolved
	var s3source, s3note string
	if s3CompatSTSWanted(provider.TypeOracle) {
		success := SourceS3STS
		if vendorSTSEnabled("ORACLE") {
			success = SourceOracleSTS
		}
		s3out, s3source, s3note = applyOptionalS3CompatSTS(resolved, duration, fallbackSource, success, "Oracle", noteOracle)
	}

	// Native IAM private-key assist (env-based; not stored in provider JSON for now).
	if ociNativeIAMEnabled() {
		key, err := LoadOCIAPIKeyFromEnv()
		if err != nil {
			msg := "OCI native IAM enabled but key env incomplete: " + err.Error()
			if s3out != nil {
				if s3note != "" {
					s3note += " | "
				}
				return s3out, s3source, s3note + msg
			}
			return resolved, fallbackSource, msg
		}
		user, err := TryOCIValidateUser(key)
		if err != nil {
			msg := "OCI IAM API-key validation failed: " + err.Error() + "; using embedded/S3-compat path"
			if s3out != nil {
				if s3note != "" {
					s3note += " | "
				}
				return s3out, s3source, s3note + msg
			}
			return resolved, fallbackSource, msg
		}
		name, _ := user["name"].(string)
		msg := fmt.Sprintf("OCI IAM API-key validated (user=%s region=%s); mount uses S3-compat AK/SK when present", name, key.Region)
		var base *provider.Resolved
		var baseSrc string
		if s3out != nil && (s3source == SourceOracleSTS || s3source == SourceS3STS) {
			base, baseSrc = s3out, s3source
			if s3note != "" {
				msg = s3note + " | " + msg
			}
		} else {
			cp := *resolved
			base = &cp
			baseSrc = SourceOCIIAM
			if base.AccessKey == "" {
				msg += "; no S3 access_key on provider — enable AI_CLOUDHUB_OCI_CREATE_SECRET=1 to mint Customer Secret Key (best-effort)"
			}
		}
		// Stage C: optional Customer Secret mint + Object PAR assist.
		return applyOCIStageCAssists(key, base, duration, baseSrc, msg)
	}

	if s3out != nil {
		return s3out, s3source, s3note
	}
	return resolved, fallbackSource, noteOracle
}
