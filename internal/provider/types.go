package provider

// Type is a supported object-storage backend (see docs/VENDORS.md).
type Type string

const (
	TypeS3    Type = "s3"    // A1: generic S3-compatible
	TypeR2    Type = "r2"    // A2: Cloudflare R2
	TypeMinIO Type = "minio" // A3: self-hosted MinIO
	// Batch B+
	TypeB2     Type = "b2"
	TypeOSS    Type = "oss"
	TypeCOS    Type = "cos"
	TypeQiniu  Type = "qiniu"
	TypeOracle Type = "oracle"
)

// BatchA is the implementation order for the first release.
var BatchA = []Type{TypeS3, TypeR2, TypeMinIO}

// BatchB is S3-compatible vendor templates (B2, OSS, COS).
var BatchB = []Type{TypeB2, TypeOSS, TypeCOS}

// BatchC is S3-compatible vendor templates (Qiniu Kodo, Oracle OCI).
var BatchC = []Type{TypeQiniu, TypeOracle}

// Meta describes a provider type for API discovery.
type Meta struct {
	Type        Type     `json:"type"`
	Name        string   `json:"name"`
	Batch       string   `json:"batch"`
	S3Compat    bool     `json:"s3_compat"`
	Implemented bool     `json:"implemented"`
	Fields      []string `json:"fields"`
	Notes       string   `json:"notes"`
}

// Catalog lists all planned vendors; Implemented=true for Batch A/B/C S3-compatible types.
func Catalog() []Meta {
	return []Meta{
		{Type: TypeS3, Name: "Generic S3", Batch: "A", S3Compat: true, Implemented: true,
			Fields: []string{"access_key", "secret_key", "endpoint", "region", "force_path_style"},
			Notes:  "AWS S3, custom S3-compatible endpoints"},
		{Type: TypeR2, Name: "Cloudflare R2", Batch: "A", S3Compat: true, Implemented: true,
			Fields: []string{"access_key", "secret_key", "account_id", "endpoint?"},
			Notes:  "Free ~10GB; zero egress. endpoint optional if account_id set"},
		{Type: TypeMinIO, Name: "MinIO", Batch: "A", S3Compat: true, Implemented: true,
			Fields: []string{"access_key", "secret_key", "endpoint", "region?", "force_path_style"},
			Notes:  "Self-hosted; path-style default true"},
		{Type: TypeB2, Name: "Backblaze B2", Batch: "B", S3Compat: true, Implemented: true,
			Fields: []string{"access_key", "secret_key", "endpoint", "region?", "force_path_style"},
			Notes:  "S3-compatible; path-style default true; e.g. s3.us-west-000.backblazeb2.com"},
		{Type: TypeOSS, Name: "Aliyun OSS", Batch: "B", S3Compat: true, Implemented: true,
			Fields: []string{"access_key", "secret_key", "endpoint", "region?", "force_path_style"},
			Notes:  "S3-compatible; virtual-hosted default; e.g. oss-cn-hangzhou.aliyuncs.com"},
		{Type: TypeCOS, Name: "Tencent COS", Batch: "B", S3Compat: true, Implemented: true,
			Fields: []string{"access_key", "secret_key", "endpoint", "region?", "force_path_style"},
			Notes:  "S3-compatible; e.g. cos.ap-guangzhou.myqcloud.com"},
		{Type: TypeQiniu, Name: "Qiniu Kodo", Batch: "C", S3Compat: true, Implemented: true,
			Fields: []string{"access_key", "secret_key", "endpoint", "region?", "force_path_style"},
			Notes:  "S3-compatible; path-style often true; e.g. s3-cn-east-1.qiniucs.com"},
		{Type: TypeOracle, Name: "Oracle OCI", Batch: "C", S3Compat: true, Implemented: true,
			Fields: []string{"access_key", "secret_key", "endpoint", "region?", "force_path_style"},
			Notes:  "OCI S3 compatibility; path-style default true; region default us-ashburn-1"},
	}
}

// IsImplemented reports whether the type has Resolve support (Batch A/B/C).
func IsImplemented(t Type) bool {
	switch t {
	case TypeS3, TypeR2, TypeMinIO, TypeB2, TypeOSS, TypeCOS, TypeQiniu, TypeOracle:
		return true
	default:
		return false
	}
}
