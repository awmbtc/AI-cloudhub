package sts

// Source labels for vendor-specific S3-compatible STS (non-native).
// Native Qiniu download tokens: see qiniu_token.go (SourceQiniuDownload).
// Native OCI API-key IAM: see oci_iam.go (SourceOCIIAM).
const (
	SourceQiniuSTS  = "qiniu_sts"  // S3-compat AssumeRole
	SourceOracleSTS = "oracle_sts" // S3-compat AssumeRole
)
