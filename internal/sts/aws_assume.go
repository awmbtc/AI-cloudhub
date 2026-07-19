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

// awsSTSEnabled reports whether optional AWS STS AssumeRole is requested.
// Set AI_CLOUDHUB_AWS_STS=1 to attempt when provider type is s3 and endpoint looks like AWS.
func awsSTSEnabled() bool {
	return envFlagTrue("AI_CLOUDHUB_AWS_STS")
}

// looksLikeAWS reports whether the resolved S3 endpoint is (or defaults to) AWS S3.
// Custom S3-compatible endpoints must not hit AWS STS.
func looksLikeAWS(r *provider.Resolved) bool {
	if r == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(r.ProviderLabel), "AWS") {
		return true
	}
	ep := strings.ToLower(strings.TrimSpace(r.Endpoint))
	if ep == "" || ep == "s3.amazonaws.com" {
		return true
	}
	// China partitions
	if strings.HasSuffix(ep, ".amazonaws.com.cn") {
		return strings.Contains(ep, "s3")
	}
	if !strings.HasSuffix(ep, ".amazonaws.com") {
		return false
	}
	// Common S3 host forms: s3.amazonaws.com, s3.us-west-2.amazonaws.com,
	// s3-us-west-2.amazonaws.com, dualstack, fips, virtual-hosted bucket.s3...
	if ep == "s3.amazonaws.com" {
		return true
	}
	if strings.HasPrefix(ep, "s3.") || strings.HasPrefix(ep, "s3-") {
		return true
	}
	if strings.Contains(ep, ".s3.") || strings.Contains(ep, ".s3-") {
		return true
	}
	if strings.Contains(ep, "s3-fips") || strings.Contains(ep, "s3.dualstack") {
		return true
	}
	return false
}

// awsSTSEndpointURL returns the STS service URL (not the S3 data endpoint).
// Override with AI_CLOUDHUB_AWS_STS_ENDPOINT (tests / GovCloud / custom).
func awsSTSEndpointURL(region string) string {
	if ep := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_AWS_STS_ENDPOINT")); ep != "" {
		return strings.TrimRight(ep, "/")
	}
	region = strings.TrimSpace(region)
	if region == "" || region == "us-east-1" {
		return "https://sts.amazonaws.com"
	}
	// China regions use partition-specific hosts; prefer explicit override env.
	if strings.HasPrefix(region, "cn-") {
		return "https://sts." + region + ".amazonaws.com.cn"
	}
	return "https://sts." + region + ".amazonaws.com"
}

// TryAWSAssumeRole calls AWS STS AssumeRole via minio-go SigV4 form POST.
// Requires AI_CLOUDHUB_AWS_STS_ROLE_ARN (AWS mandates RoleArn for AssumeRole).
// Optional: AI_CLOUDHUB_AWS_STS_EXTERNAL_ID, AI_CLOUDHUB_AWS_STS_ENDPOINT.
//
// Best-effort only: callers must fall back to embedded credentials on error.
func TryAWSAssumeRole(r *provider.Resolved, duration time.Duration) (access, secret, token string, exp time.Time, err error) {
	if r == nil {
		return "", "", "", time.Time{}, fmt.Errorf("resolved provider required")
	}
	if r.AccessKey == "" || r.SecretKey == "" {
		return "", "", "", time.Time{}, fmt.Errorf("access_key and secret_key required for AWS STS")
	}
	roleARN := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_AWS_STS_ROLE_ARN"))
	if roleARN == "" {
		return "", "", "", time.Time{}, fmt.Errorf("AI_CLOUDHUB_AWS_STS_ROLE_ARN required for AWS AssumeRole")
	}

	secs := clampSTSDurationSeconds(duration)
	loc := r.Region
	if loc == "" {
		loc = "us-east-1"
	}
	stsEndpoint := awsSTSEndpointURL(loc)

	opts := credentials.STSAssumeRoleOptions{
		AccessKey:       r.AccessKey,
		SecretKey:       r.SecretKey,
		Location:        loc,
		DurationSeconds: secs,
		RoleARN:         roleARN,
		RoleSessionName: "ai-cloudhub",
		ExternalID:      strings.TrimSpace(os.Getenv("AI_CLOUDHUB_AWS_STS_EXTERNAL_ID")),
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
		return "", "", "", time.Time{}, fmt.Errorf("aws AssumeRole: %w", err)
	}
	if val.AccessKeyID == "" || val.SecretAccessKey == "" {
		return "", "", "", time.Time{}, fmt.Errorf("aws AssumeRole: empty temporary credentials")
	}
	exp = val.Expiration
	if exp.IsZero() {
		exp = time.Now().UTC().Add(time.Duration(secs) * time.Second)
	}
	return val.AccessKeyID, val.SecretAccessKey, val.SessionToken, exp, nil
}

// applyOptionalAWSSTS clones resolved with temporary AWS STS creds when enabled
// and the endpoint looks like AWS. Otherwise returns original + fallbackSource.
func applyOptionalAWSSTS(resolved *provider.Resolved, duration time.Duration, fallbackSource string) (out *provider.Resolved, source, note string) {
	if resolved == nil {
		return nil, fallbackSource, ""
	}
	if resolved.Type != provider.TypeS3 {
		return resolved, fallbackSource, ""
	}
	if !awsSTSEnabled() {
		return resolved, fallbackSource, ""
	}
	if !looksLikeAWS(resolved) {
		return resolved, fallbackSource,
			"S3 endpoint is not AWS; AI_CLOUDHUB_AWS_STS skipped; using embedded credentials in short-lived session"
	}
	ak, sk, tok, _, err := TryAWSAssumeRole(resolved, duration)
	if err != nil {
		return resolved, fallbackSource,
			"AWS STS AssumeRole failed; using embedded credentials in short-lived session"
	}
	cp := *resolved
	cp.AccessKey = ak
	cp.SecretKey = sk
	cp.SessionToken = tok
	return &cp, SourceAWSSTS, ""
}
