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
// Uses minio-go credentials.STSAssumeRole (SigV4 form POST). No extra deps.
// Callers should fall back to embedded keys when this returns an error
// (MinIO may lack STS/IAM policies, network may fail, etc.).
func TryMinioAssumeRole(r *provider.Resolved, duration time.Duration) (access, secret, token string, exp time.Time, err error) {
	if r == nil {
		return "", "", "", time.Time{}, fmt.Errorf("resolved provider required")
	}
	if r.Endpoint == "" {
		return "", "", "", time.Time{}, fmt.Errorf("endpoint required for MinIO STS")
	}
	if r.AccessKey == "" || r.SecretKey == "" {
		return "", "", "", time.Time{}, fmt.Errorf("access_key and secret_key required for MinIO STS")
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
		// Optional; MinIO root/admin AssumeRole often works without RoleARN.
		// When server IAM is required, set AI_CLOUDHUB_MINIO_STS_ROLE_ARN.
		RoleARN:         strings.TrimSpace(os.Getenv("AI_CLOUDHUB_MINIO_STS_ROLE_ARN")),
		RoleSessionName: "ai-cloudhub",
	}

	// Construct provider directly so we can bound HTTP timeout.
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
		return "", "", "", time.Time{}, fmt.Errorf("minio AssumeRole: %w", err)
	}
	if val.AccessKeyID == "" || val.SecretAccessKey == "" {
		return "", "", "", time.Time{}, fmt.Errorf("minio AssumeRole: empty temporary credentials")
	}
	exp = val.Expiration
	if exp.IsZero() {
		exp = time.Now().UTC().Add(time.Duration(secs) * time.Second)
	}
	return val.AccessKeyID, val.SecretAccessKey, val.SessionToken, exp, nil
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
// On any failure or when disabled, returns the original resolved and fallbackSource.
func applyOptionalMinioSTS(resolved *provider.Resolved, duration time.Duration, fallbackSource string) (out *provider.Resolved, source, note string) {
	if resolved == nil {
		return nil, fallbackSource, ""
	}
	if !minioSTSEnabled() || resolved.Type != provider.TypeMinIO {
		return resolved, fallbackSource, ""
	}
	ak, sk, tok, _, err := TryMinioAssumeRole(resolved, duration)
	if err != nil {
		// Best-effort: fall back to embedding long-lived keys in the short-lived session.
		return resolved, fallbackSource, "MinIO STS AssumeRole failed; using embedded credentials in short-lived session"
	}
	cp := *resolved
	cp.AccessKey = ak
	cp.SecretKey = sk
	cp.SessionToken = tok
	return &cp, SourceMinioSTS, ""
}
