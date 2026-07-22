package sts

import (
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

// Vendor notes when native / S3-compatible STS is not attempted (honest best-effort).
const (
	noteR2     = "Cloudflare R2 typically uses long-lived S3 API tokens, not classic STS AssumeRole; set AI_CLOUDHUB_R2_STS=1 or AI_CLOUDHUB_S3_STS=1 to attempt S3-compatible AssumeRole"
	noteB2     = "Backblaze B2: S3-compatible STS not enabled; set AI_CLOUDHUB_B2_STS=1 or AI_CLOUDHUB_S3_STS=1 to attempt AssumeRole"
	noteOSS    = "Aliyun OSS: set AI_CLOUDHUB_OSS_NATIVE_STS=1 + RoleArn (acs:ram::…) for RAM STS, or AI_CLOUDHUB_OSS_STS=1 / AI_CLOUDHUB_S3_STS=1 for S3-compat AssumeRole"
	noteCOS    = "Tencent COS: set AI_CLOUDHUB_COS_NATIVE_STS=1 + RoleArn (qcs::cam::…) for CAM STS, or AI_CLOUDHUB_COS_STS=1 / AI_CLOUDHUB_S3_STS=1 for S3-compat AssumeRole"
	noteQiniu  = "Qiniu Kodo: S3-compatible STS not enabled; set AI_CLOUDHUB_QINIU_STS=1 or AI_CLOUDHUB_S3_STS=1 to attempt AssumeRole"
	noteOracle = "Oracle OCI: S3-compatible STS not enabled; set AI_CLOUDHUB_ORACLE_STS=1 or AI_CLOUDHUB_S3_STS=1 to attempt AssumeRole"
)

// applyOptionalSTS is the multi-vendor best-effort STS entry used by Issue/Refresh.
//
// Behavior:
//   - minio: AI_CLOUDHUB_MINIO_STS=1 or AI_CLOUDHUB_S3_STS=1 → AssumeRole → source=minio_sts
//   - s3 + AWS-looking endpoint: AI_CLOUDHUB_AWS_STS=1 → AWS AssumeRole → source=aws_sts
//   - s3 + custom endpoint: AI_CLOUDHUB_S3_STS=1 → S3-compat AssumeRole → source=s3_sts
//   - oss: native Aliyun RAM STS when NATIVE flag / acs:ram RoleArn → source=aliyun_sts;
//     else S3-compat → source=s3_sts
//   - cos: native Tencent CAM STS when NATIVE flag / qcs RoleArn → source=tencent_sts;
//     else S3-compat → source=s3_sts
//   - r2/b2/qiniu/oracle: per-vendor or AI_CLOUDHUB_S3_STS → S3-compat → source=s3_sts
//     when flags off: embedded/refresh + Note
//
// Always falls back to the original resolved credentials on failure or when disabled.
// fallbackSource is typically SourceEmbedded (Issue) or SourceRefresh (Refresh).
func applyOptionalSTS(resolved *provider.Resolved, duration time.Duration, fallbackSource string) (out *provider.Resolved, source, note string) {
	if resolved == nil {
		return nil, fallbackSource, ""
	}
	switch resolved.Type {
	case provider.TypeMinIO:
		return applyOptionalMinioSTS(resolved, duration, fallbackSource)
	case provider.TypeS3:
		return applyOptionalTypeS3STS(resolved, duration, fallbackSource)
	case provider.TypeR2:
		return applyOptionalS3CompatSTS(resolved, duration, fallbackSource, SourceS3STS, "R2", noteR2)
	case provider.TypeB2:
		return applyOptionalS3CompatSTS(resolved, duration, fallbackSource, SourceS3STS, "B2", noteB2)
	case provider.TypeOSS:
		return applyOptionalOSSSTS(resolved, duration, fallbackSource)
	case provider.TypeCOS:
		return applyOptionalCOSSTS(resolved, duration, fallbackSource)
	case provider.TypeQiniu:
		return applyOptionalS3CompatSTS(resolved, duration, fallbackSource, SourceS3STS, "Qiniu", noteQiniu)
	case provider.TypeOracle:
		return applyOptionalS3CompatSTS(resolved, duration, fallbackSource, SourceS3STS, "Oracle", noteOracle)
	default:
		return resolved, fallbackSource, ""
	}
}

// applyOptionalTypeS3STS handles type=s3:
// AWS-looking endpoints use AWS STS when AI_CLOUDHUB_AWS_STS is on;
// custom S3-compatible endpoints use shared AssumeRole when AI_CLOUDHUB_S3_STS is on.
func applyOptionalTypeS3STS(resolved *provider.Resolved, duration time.Duration, fallbackSource string) (out *provider.Resolved, source, note string) {
	if resolved == nil {
		return nil, fallbackSource, ""
	}
	// Prefer real AWS STS for AWS endpoints when enabled.
	if looksLikeAWS(resolved) {
		return applyOptionalAWSSTS(resolved, duration, fallbackSource)
	}
	// Custom / non-AWS S3 endpoint: optional S3-compatible AssumeRole on the data endpoint.
	if !s3STSEnabled() {
		return resolved, fallbackSource, ""
	}
	return applyOptionalS3CompatSTS(resolved, duration, fallbackSource, SourceS3STS, "S3", "")
}
