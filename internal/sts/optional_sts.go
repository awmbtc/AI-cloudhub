package sts

import (
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

// Vendor notes when native STS is not attempted (honest best-effort).
const (
	noteR2     = "Cloudflare R2 typically uses long-lived S3 API tokens, not classic STS AssumeRole; native STS not attempted"
	noteB2     = "Backblaze B2: no clean control-plane STS path in this build; using embedded credentials in short-lived session"
	noteOSS    = "Aliyun OSS: native STS (AssumeRole) not wired; using embedded credentials in short-lived session"
	noteCOS    = "Tencent COS: native STS not wired; using embedded credentials in short-lived session"
	noteQiniu  = "Qiniu Kodo: native STS not wired; using embedded credentials in short-lived session"
	noteOracle = "Oracle OCI: native STS not wired; using embedded credentials in short-lived session"
)

// applyOptionalSTS is the multi-vendor best-effort STS entry used by Issue/Refresh.
//
// Behavior:
//   - minio: if AI_CLOUDHUB_MINIO_STS=1, try MinIO AssumeRole → source=minio_sts
//   - s3: if AI_CLOUDHUB_AWS_STS=1 and endpoint looks like AWS, try AWS AssumeRole → source=aws_sts
//   - r2 / b2 / oss / cos / qiniu / oracle: no harmful STS probe; keep embedded/refresh + Note
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
		return applyOptionalAWSSTS(resolved, duration, fallbackSource)
	case provider.TypeR2:
		return resolved, fallbackSource, noteR2
	case provider.TypeB2:
		return resolved, fallbackSource, noteB2
	case provider.TypeOSS:
		return resolved, fallbackSource, noteOSS
	case provider.TypeCOS:
		return resolved, fallbackSource, noteCOS
	case provider.TypeQiniu:
		return resolved, fallbackSource, noteQiniu
	case provider.TypeOracle:
		return resolved, fallbackSource, noteOracle
	default:
		return resolved, fallbackSource, ""
	}
}
