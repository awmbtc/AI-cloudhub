package sts

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

// SourceQiniuDownload labels sessions that include a native Qiniu private download token
// (HMAC URL token — not S3 session credentials).
const SourceQiniuDownload = "qiniu_download"

// qiniuDownloadTokenEnabled: AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN=1|true|yes
// When set, Issue attaches a time-limited Qiniu private download token for the drive prefix.
func qiniuDownloadTokenEnabled() bool {
	return envFlagTrue("AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN")
}

// QiniuDownloadToken builds a private download token for a base URL (no query).
// Spec: token = AccessKey + ":" + urlsafe_base64(hmac_sha1(secret, data))
// data = path_with_query + "\n" + deadline_unix
//
// url should be the full download URL without token query (scheme://host/key).
// deadline is absolute unix seconds.
func QiniuDownloadToken(accessKey, secretKey, rawURL string, deadline int64) (string, error) {
	accessKey = strings.TrimSpace(accessKey)
	secretKey = strings.TrimSpace(secretKey)
	if accessKey == "" || secretKey == "" {
		return "", fmt.Errorf("qiniu access_key and secret_key required")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("qiniu url: %w", err)
	}
	// Qiniu private download (SDK-compatible):
	//   signed = baseURL + "?e=" + deadline
	//   sign = urlsafe_b64(hmac_sha1(sk, signed))  (padding stripped)
	//   token = ak + ":" + sign
	//   final = signed + "&token=" + token
	signedURL := u.Scheme + "://" + u.Host + u.Path + "?e=" + fmt.Sprintf("%d", deadline)
	mac := hmac.New(sha1.New, []byte(secretKey))
	_, _ = mac.Write([]byte(signedURL))
	sign := base64.URLEncoding.EncodeToString(mac.Sum(nil))
	sign = strings.TrimRight(sign, "=")
	return accessKey + ":" + sign, nil
}

// QiniuSignedDownloadURL returns full URL with e= and token=.
func QiniuSignedDownloadURL(accessKey, secretKey, baseURL string, ttl time.Duration) (string, int64, error) {
	if ttl <= 0 {
		ttl = time.Hour
	}
	if ttl > 24*time.Hour {
		ttl = 24 * time.Hour
	}
	deadline := time.Now().UTC().Add(ttl).Unix()
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", 0, err
	}
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	base := strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "?")
	tok, err := QiniuDownloadToken(accessKey, secretKey, base, deadline)
	if err != nil {
		return "", 0, err
	}
	final := base + "?e=" + fmt.Sprintf("%d", deadline) + "&token=" + url.QueryEscape(tok)
	return final, deadline, nil
}

// applyOptionalQiniuSTS prefers S3-compat AssumeRole when enabled; always can attach download token note.
func applyOptionalQiniuSTS(resolved *provider.Resolved, duration time.Duration, fallbackSource string) (out *provider.Resolved, source, note string) {
	if resolved == nil {
		return nil, fallbackSource, ""
	}
	if resolved.Type != provider.TypeQiniu {
		return resolved, fallbackSource, ""
	}

	// Prefer S3-compat STS when wanted.
	if s3CompatSTSWanted(provider.TypeQiniu) {
		success := SourceS3STS
		if vendorSTSEnabled("QINIU") {
			success = SourceQiniuSTS
		}
		out, source, note = applyOptionalS3CompatSTS(resolved, duration, fallbackSource, success, "Qiniu", noteQiniu)
		// Optionally still document download-token path for object-level access.
		if qiniuDownloadTokenEnabled() {
			extra := qiniuDownloadHint(resolved, duration)
			if note != "" {
				note += " | "
			}
			note += extra
			if source == fallbackSource || source == SourceEmbedded || source == SourceRefresh {
				// No S3 STS success — mark download-token assist as primary note path.
				if source == fallbackSource {
					source = SourceQiniuDownload
				}
			}
		}
		return out, source, note
	}

	// No S3 STS: optional native download token assist.
	if qiniuDownloadTokenEnabled() {
		cp := *resolved
		return &cp, SourceQiniuDownload, qiniuDownloadHint(resolved, duration)
	}
	return resolved, fallbackSource, noteQiniu
}

func qiniuDownloadHint(r *provider.Resolved, duration time.Duration) string {
	// Build a sample signed URL for a placeholder key under the endpoint.
	// Callers still use BYOS; this demonstrates the token format for agents.
	host := r.Endpoint
	if host == "" {
		host = "download.example.com"
	}
	scheme := "https"
	if !r.UseSSL {
		scheme = "http"
	}
	// Domain-style: often bucket is in host for Qiniu. Use generic path sample.
	sample := scheme + "://" + host + "/__sample_key__"
	signed, deadline, err := QiniuSignedDownloadURL(r.AccessKey, r.SecretKey, sample, duration)
	if err != nil {
		return "Qiniu download-token assist failed: " + err.Error()
	}
	// Never put full long-lived secrets; token is short-lived URL signature.
	max := 180
	if len(signed) < max {
		max = len(signed)
	}
	return fmt.Sprintf(
		"Qiniu private download token (source=qiniu_download): sample_signed_url_prefix=%s… deadline=%d; set AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN=1; replace __sample_key__ with object key under drive prefix",
		signed[:max], deadline,
	)
}

// QiniuUploadToken builds a simple upload (put) token with deadline.
// policy JSON: {"scope":"bucket:keyPrefix","deadline":unix}
func QiniuUploadToken(accessKey, secretKey, scope string, deadline int64) (string, error) {
	accessKey = strings.TrimSpace(accessKey)
	secretKey = strings.TrimSpace(secretKey)
	if accessKey == "" || secretKey == "" {
		return "", fmt.Errorf("qiniu access_key and secret_key required")
	}
	if scope == "" {
		return "", fmt.Errorf("scope required (bucket or bucket:key)")
	}
	// Minimal put policy
	policy := fmt.Sprintf(`{"scope":%q,"deadline":%d}`, scope, deadline)
	encodedPolicy := base64.URLEncoding.EncodeToString([]byte(policy))
	encodedPolicy = strings.TrimRight(encodedPolicy, "=")
	mac := hmac.New(sha1.New, []byte(secretKey))
	_, _ = mac.Write([]byte(encodedPolicy))
	sign := base64.URLEncoding.EncodeToString(mac.Sum(nil))
	sign = strings.TrimRight(sign, "=")
	return accessKey + ":" + sign + ":" + encodedPolicy, nil
}


