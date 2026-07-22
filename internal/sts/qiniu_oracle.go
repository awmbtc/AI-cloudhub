package sts

import (
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

// SourceQiniuSTS / SourceOracleSTS label S3-compatible AssumeRole success when
// the vendor-specific flag was used (or dedicated STS endpoint). Full native
// Qiniu IAM / OCI IAM (private-key) is out of scope for the AK/SK S3 model.
const (
	SourceQiniuSTS  = "qiniu_sts"
	SourceOracleSTS = "oracle_sts"
)

// applyOptionalQiniuSTS tries S3-compatible AssumeRole for Qiniu Kodo.
// Prefer AI_CLOUDHUB_QINIU_STS_ENDPOINT when STS is not on the data endpoint.
// Native Qiniu “temporary download tokens” are not S3 session credentials.
func applyOptionalQiniuSTS(resolved *provider.Resolved, duration time.Duration, fallbackSource string) (out *provider.Resolved, source, note string) {
	if resolved == nil {
		return nil, fallbackSource, ""
	}
	if resolved.Type != provider.TypeQiniu {
		return resolved, fallbackSource, ""
	}
	if !s3CompatSTSWanted(provider.TypeQiniu) {
		return resolved, fallbackSource, noteQiniu
	}
	// Vendor-specific source label when QINIU_STS is on (even if S3_STS also on).
	success := SourceS3STS
	if vendorSTSEnabled("QINIU") {
		success = SourceQiniuSTS
	}
	return applyOptionalS3CompatSTS(resolved, duration, fallbackSource, success, "Qiniu", noteQiniu)
}

// applyOptionalOracleSTS tries S3-compatible AssumeRole for OCI Object Storage
// S3 compatibility API. Full OCI IAM (user OCID + private key) is not supported
// here — set AI_CLOUDHUB_ORACLE_STS_ENDPOINT if STS is hosted separately.
func applyOptionalOracleSTS(resolved *provider.Resolved, duration time.Duration, fallbackSource string) (out *provider.Resolved, source, note string) {
	if resolved == nil {
		return nil, fallbackSource, ""
	}
	if resolved.Type != provider.TypeOracle {
		return resolved, fallbackSource, ""
	}
	if !s3CompatSTSWanted(provider.TypeOracle) {
		return resolved, fallbackSource, noteOracle
	}
	success := SourceS3STS
	if vendorSTSEnabled("ORACLE") {
		success = SourceOracleSTS
	}
	return applyOptionalS3CompatSTS(resolved, duration, fallbackSource, success, "Oracle", noteOracle)
}
