package sts

import (
	"os"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

// Credential source labels for Session.Source.
const (
	SourceEmbedded = "embedded"  // long-lived access/secret from provider record
	SourceRefresh  = "refresh"   // re-issued session from control-plane Refresh
	SourceMinioSTS = "minio_sts" // temporary creds from MinIO STS AssumeRole
	SourceAWSSTS   = "aws_sts"   // temporary creds from AWS STS AssumeRole
)

// envFlagTrue reports whether an env var is a common truthy value (1/true/yes).
func envFlagTrue(key string) bool {
	v := strings.TrimSpace(os.Getenv(key))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

// minioSTSEnabled reports whether optional MinIO native STS is requested.
// Set AI_CLOUDHUB_MINIO_STS=1 to attempt AssumeRole when provider type is minio.
// Generic AI_CLOUDHUB_S3_STS=1 also enables MinIO (see s3CompatSTSWanted).
func minioSTSEnabled() bool {
	return envFlagTrue("AI_CLOUDHUB_MINIO_STS")
}

// clampSTSDurationSeconds maps a session TTL into a typical STS DurationSeconds range.
func clampSTSDurationSeconds(duration time.Duration) int {
	secs := int(duration.Seconds())
	if secs < 900 {
		secs = 900 // typical STS lower bound
	}
	if secs > 12*3600 {
		secs = 12 * 3600
	}
	return secs
}

// TryMinioAssumeRole calls MinIO (S3-compatible) STS AssumeRole and returns
// temporary access/secret/session-token credentials.
//
// Delegates to TryS3AssumeRole. Role ARN from AI_CLOUDHUB_MINIO_STS_ROLE_ARN
// or AI_CLOUDHUB_S3_STS_ROLE_ARN. Callers should fall back to embedded keys on error.
func TryMinioAssumeRole(r *provider.Resolved, duration time.Duration) (access, secret, token string, exp time.Time, err error) {
	return TryS3AssumeRole(r, duration, roleARNForType(provider.TypeMinIO))
}

// stsEndpointURL builds the MinIO/S3-compatible STS base URL from resolved endpoint.
// MinIO serves STS on the same host as the S3 API.
func stsEndpointURL(r *provider.Resolved) string {
	scheme := "https"
	if !r.UseSSL {
		scheme = "http"
	}
	ep := strings.TrimSpace(r.Endpoint)
	if strings.HasPrefix(ep, "http://") || strings.HasPrefix(ep, "https://") {
		return strings.TrimRight(ep, "/")
	}
	return scheme + "://" + ep
}

// applyOptionalMinioSTS is the MinIO branch of multi-vendor optional STS.
// Enabled by AI_CLOUDHUB_MINIO_STS=1 or AI_CLOUDHUB_S3_STS=1.
// On any failure or when disabled, returns the original resolved and fallbackSource.
func applyOptionalMinioSTS(resolved *provider.Resolved, duration time.Duration, fallbackSource string) (out *provider.Resolved, source, note string) {
	if resolved == nil {
		return nil, fallbackSource, ""
	}
	if resolved.Type != provider.TypeMinIO {
		return resolved, fallbackSource, ""
	}
	return applyOptionalS3CompatSTS(resolved, duration, fallbackSource, SourceMinioSTS, "MinIO", "")
}
