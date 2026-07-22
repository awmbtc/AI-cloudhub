package sts

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// SourceS3STS labels temporary credentials from S3-compatible STS AssumeRole
// (generic / non-MinIO / non-AWS paths). MinIO keeps SourceMinioSTS; AWS keeps SourceAWSSTS.
const SourceS3STS = "s3_sts"

// s3STSEnabled reports whether the generic S3-compatible STS flag is on.
// Set AI_CLOUDHUB_S3_STS=1 to attempt AssumeRole against the provider endpoint
// for minio, b2, oss, cos, qiniu, oracle, r2, and non-AWS type=s3 endpoints.
func s3STSEnabled() bool {
	return envFlagTrue("AI_CLOUDHUB_S3_STS")
}

// vendorSTSEnabled reports AI_CLOUDHUB_<VENDOR>_STS (e.g. B2, OSS, R2).
func vendorSTSEnabled(vendor string) bool {
	vendor = strings.ToUpper(strings.TrimSpace(vendor))
	if vendor == "" {
		return false
	}
	return envFlagTrue("AI_CLOUDHUB_" + vendor + "_STS")
}

// s3CompatSTSWanted reports whether S3-compatible AssumeRole should be attempted
// for the given provider type (generic flag or per-vendor flag).
// MinIO also honors AI_CLOUDHUB_MINIO_STS (handled separately via minioSTSEnabled).
func s3CompatSTSWanted(t provider.Type) bool {
	if s3STSEnabled() {
		return true
	}
	switch t {
	case provider.TypeMinIO:
		return minioSTSEnabled()
	case provider.TypeB2:
		return vendorSTSEnabled("B2")
	case provider.TypeOSS:
		return vendorSTSEnabled("OSS")
	case provider.TypeCOS:
		return vendorSTSEnabled("COS")
	case provider.TypeQiniu:
		return vendorSTSEnabled("QINIU")
	case provider.TypeOracle:
		return vendorSTSEnabled("ORACLE")
	case provider.TypeR2:
		return vendorSTSEnabled("R2")
	case provider.TypeS3:
		// Custom (non-AWS) S3 endpoints only via generic S3_STS (checked above).
		return false
	default:
		return false
	}
}

// roleARNForType returns optional RoleArn for S3-compatible AssumeRole.
// Prefers vendor-specific AI_CLOUDHUB_<VENDOR>_STS_ROLE_ARN, then AI_CLOUDHUB_S3_STS_ROLE_ARN.
// MinIO also checks AI_CLOUDHUB_MINIO_STS_ROLE_ARN (existing).
func roleARNForType(t provider.Type) string {
	var keys []string
	switch t {
	case provider.TypeMinIO:
		keys = []string{"AI_CLOUDHUB_MINIO_STS_ROLE_ARN", "AI_CLOUDHUB_S3_STS_ROLE_ARN"}
	case provider.TypeB2:
		keys = []string{"AI_CLOUDHUB_B2_STS_ROLE_ARN", "AI_CLOUDHUB_S3_STS_ROLE_ARN"}
	case provider.TypeOSS:
		keys = []string{"AI_CLOUDHUB_OSS_STS_ROLE_ARN", "AI_CLOUDHUB_S3_STS_ROLE_ARN"}
	case provider.TypeCOS:
		keys = []string{"AI_CLOUDHUB_COS_STS_ROLE_ARN", "AI_CLOUDHUB_S3_STS_ROLE_ARN"}
	case provider.TypeQiniu:
		keys = []string{"AI_CLOUDHUB_QINIU_STS_ROLE_ARN", "AI_CLOUDHUB_S3_STS_ROLE_ARN"}
	case provider.TypeOracle:
		keys = []string{"AI_CLOUDHUB_ORACLE_STS_ROLE_ARN", "AI_CLOUDHUB_S3_STS_ROLE_ARN"}
	case provider.TypeR2:
		keys = []string{"AI_CLOUDHUB_R2_STS_ROLE_ARN", "AI_CLOUDHUB_S3_STS_ROLE_ARN"}
	case provider.TypeS3:
		keys = []string{"AI_CLOUDHUB_S3_STS_ROLE_ARN"}
	default:
		keys = []string{"AI_CLOUDHUB_S3_STS_ROLE_ARN"}
	}
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// TryS3AssumeRole calls S3-compatible STS AssumeRole against the provider endpoint
// using minio-go credentials.STSAssumeRole (SigV4 form POST). No extra deps.
//
// STS is served on the same host as the S3 API for MinIO and many S3-compatible
// gateways. roleARN may be empty when the server allows root/admin AssumeRole.
//
// Best-effort only: callers must fall back to embedded credentials on error.
func TryS3AssumeRole(r *provider.Resolved, duration time.Duration, roleARN string) (access, secret, token string, exp time.Time, err error) {
	if r == nil {
		return "", "", "", time.Time{}, fmt.Errorf("resolved provider required")
	}
	if r.Endpoint == "" {
		return "", "", "", time.Time{}, fmt.Errorf("endpoint required for S3-compatible STS")
	}
	if r.AccessKey == "" || r.SecretKey == "" {
		return "", "", "", time.Time{}, fmt.Errorf("access_key and secret_key required for S3-compatible STS")
	}

	stsEndpoint := stsEndpointURL(r)
	secs := clampSTSDurationSeconds(duration)

	loc := r.Region
	if loc == "" {
		loc = "us-east-1"
	}
	opts := credentials.STSAssumeRoleOptions{
		AccessKey:       r.AccessKey,
		SecretKey:       r.SecretKey,
		Location:        loc,
		DurationSeconds: secs,
		RoleARN:         strings.TrimSpace(roleARN),
		RoleSessionName: "ai-cloudhub",
	}

	p := &credentials.STSAssumeRole{
		Client: &http.Client{
			Timeout: 15 * time.Second,
		},
		STSEndpoint: stsEndpoint,
		Options:     opts,
	}
	creds := credentials.New(p)
	val, err := creds.Get()
	if err != nil {
		return "", "", "", time.Time{}, fmt.Errorf("s3-compatible AssumeRole: %w", err)
	}
	if val.AccessKeyID == "" || val.SecretAccessKey == "" {
		return "", "", "", time.Time{}, fmt.Errorf("s3-compatible AssumeRole: empty temporary credentials")
	}
	exp = val.Expiration
	if exp.IsZero() {
		exp = time.Now().UTC().Add(time.Duration(secs) * time.Second)
	}
	return val.AccessKeyID, val.SecretAccessKey, val.SessionToken, exp, nil
}

// applyOptionalS3CompatSTS tries S3-compatible AssumeRole when wanted for this type.
// successSource is typically SourceS3STS (or SourceMinioSTS for minio).
// When flags are off, returns noteWhenDisabled (may be empty for silent types).
// On any failure, falls back to original resolved + fallbackSource + failure note.
func applyOptionalS3CompatSTS(
	resolved *provider.Resolved,
	duration time.Duration,
	fallbackSource string,
	successSource string,
	vendorLabel string,
	noteWhenDisabled string,
) (out *provider.Resolved, source, note string) {
	if resolved == nil {
		return nil, fallbackSource, ""
	}
	if !s3CompatSTSWanted(resolved.Type) {
		return resolved, fallbackSource, noteWhenDisabled
	}
	ak, sk, tok, _, err := TryS3AssumeRole(resolved, duration, roleARNForType(resolved.Type))
	if err != nil {
		return resolved, fallbackSource,
			vendorLabel + " S3-compatible STS AssumeRole failed; using embedded credentials in short-lived session"
	}
	cp := *resolved
	cp.AccessKey = ak
	cp.SecretKey = sk
	cp.SessionToken = tok
	return &cp, successSource, ""
}
