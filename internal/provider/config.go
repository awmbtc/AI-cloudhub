package provider

import (
	"fmt"
	"net/url"
	"strings"
)

// Credentials holds user-supplied secrets (stored encrypted in production).
type Credentials struct {
	AccessKey      string `json:"access_key"`
	SecretKey      string `json:"secret_key"`
	Endpoint       string `json:"endpoint"`                  // host or URL
	Region         string `json:"region,omitempty"`
	AccountID      string `json:"account_id,omitempty"`      // R2
	ForcePathStyle *bool  `json:"force_path_style,omitempty"` // nil = type default
	UseSSL         *bool  `json:"use_ssl,omitempty"`          // nil = infer from endpoint
}

// Record is a saved provider binding for a user.
type Record struct {
	ID     string      `json:"id"`
	UserID string      `json:"user_id"`
	Name   string      `json:"name"`
	Type   Type        `json:"type"`
	Creds  Credentials `json:"-"` // never JSON-serialize secrets by default
	// SecretEnc holds NaCl secretbox ciphertext for Creds.SecretKey when
	// AI_CLOUDHUB_MASTER_KEY is configured (see internal/crypto/secretbox).
	// Empty means SecretKey is stored in plaintext (dev mode only).
	SecretEnc []byte `json:"-"`
	// Public view fields
	EndpointPublic string `json:"endpoint,omitempty"`
	Region         string `json:"region,omitempty"`
	AccountID      string `json:"account_id,omitempty"`
}

// Public returns a copy safe for API responses (no secret).
func (r *Record) Public() map[string]interface{} {
	return map[string]interface{}{
		"id":         r.ID,
		"name":       r.Name,
		"type":       r.Type,
		"endpoint":   r.EndpointPublic,
		"region":     r.Region,
		"account_id": r.AccountID,
	}
}

// Resolved is the normalized connection used by S3 clients and rclone.
type Resolved struct {
	Type           Type
	AccessKey      string
	SecretKey      string
	SessionToken   string // optional STS session token (temp creds)
	Endpoint       string // host:port, no scheme
	Region         string
	ForcePathStyle bool
	UseSSL         bool
	ProviderLabel  string // rclone provider= hint
}

// Resolve normalizes credentials for Batch A/B/C types.
func Resolve(t Type, c Credentials) (*Resolved, error) {
	if !IsImplemented(t) {
		return nil, fmt.Errorf("provider type %q not implemented yet (see docs/VENDORS.md batch order)", t)
	}
	if strings.TrimSpace(c.AccessKey) == "" || strings.TrimSpace(c.SecretKey) == "" {
		return nil, fmt.Errorf("access_key and secret_key required")
	}

	r := &Resolved{
		Type:      t,
		AccessKey: strings.TrimSpace(c.AccessKey),
		SecretKey: strings.TrimSpace(c.SecretKey),
		Region:    strings.TrimSpace(c.Region),
	}

	switch t {
	case TypeR2:
		r.ProviderLabel = "Cloudflare"
		r.ForcePathStyle = false
		r.UseSSL = true
		if r.Region == "" {
			r.Region = "auto"
		}
		ep := strings.TrimSpace(c.Endpoint)
		if ep == "" {
			aid := strings.TrimSpace(c.AccountID)
			if aid == "" {
				return nil, fmt.Errorf("r2 requires account_id or endpoint")
			}
			ep = fmt.Sprintf("%s.r2.cloudflarestorage.com", aid)
		}
		host, ssl, err := parseEndpoint(ep, true)
		if err != nil {
			return nil, err
		}
		r.Endpoint = host
		if c.UseSSL != nil {
			r.UseSSL = *c.UseSSL
		} else {
			r.UseSSL = ssl
		}

	case TypeMinIO:
		r.ProviderLabel = "Minio"
		r.ForcePathStyle = true
		if c.ForcePathStyle != nil {
			r.ForcePathStyle = *c.ForcePathStyle
		}
		if r.Region == "" {
			r.Region = "us-east-1"
		}
		ep := strings.TrimSpace(c.Endpoint)
		if ep == "" {
			return nil, fmt.Errorf("minio requires endpoint")
		}
		host, ssl, err := parseEndpoint(ep, false)
		if err != nil {
			return nil, err
		}
		r.Endpoint = host
		if c.UseSSL != nil {
			r.UseSSL = *c.UseSSL
		} else {
			r.UseSSL = ssl
		}

	case TypeS3:
		r.ProviderLabel = "Other"
		r.ForcePathStyle = false
		if c.ForcePathStyle != nil {
			r.ForcePathStyle = *c.ForcePathStyle
		}
		if r.Region == "" {
			r.Region = "us-east-1"
		}
		ep := strings.TrimSpace(c.Endpoint)
		if ep == "" {
			// AWS default
			r.Endpoint = "s3.amazonaws.com"
			r.UseSSL = true
			r.ProviderLabel = "AWS"
		} else {
			host, ssl, err := parseEndpoint(ep, true)
			if err != nil {
				return nil, err
			}
			r.Endpoint = host
			if c.UseSSL != nil {
				r.UseSSL = *c.UseSSL
			} else {
				r.UseSSL = ssl
			}
		}

	case TypeB2:
		// Backblaze B2 S3-compatible API; path-style typically true.
		// User provides endpoint, e.g. s3.us-west-000.backblazeb2.com
		r.ProviderLabel = "Other"
		r.ForcePathStyle = true
		if c.ForcePathStyle != nil {
			r.ForcePathStyle = *c.ForcePathStyle
		}
		// region optional; often matches cluster (e.g. us-west-000)
		ep := strings.TrimSpace(c.Endpoint)
		if ep == "" {
			return nil, fmt.Errorf("b2 requires endpoint (e.g. s3.us-west-000.backblazeb2.com)")
		}
		host, ssl, err := parseEndpoint(ep, true)
		if err != nil {
			return nil, err
		}
		r.Endpoint = host
		if c.UseSSL != nil {
			r.UseSSL = *c.UseSSL
		} else {
			r.UseSSL = ssl
		}

	case TypeOSS:
		// Aliyun OSS S3-compatible; virtual-hosted (force_path_style false) usual.
		// User provides endpoint, e.g. oss-cn-hangzhou.aliyuncs.com
		r.ProviderLabel = "Other"
		r.ForcePathStyle = false
		if c.ForcePathStyle != nil {
			r.ForcePathStyle = *c.ForcePathStyle
		}
		if r.Region == "" {
			// best-effort: leave empty if user omitted; S3 clients may still work with endpoint
			r.Region = "us-east-1"
		}
		ep := strings.TrimSpace(c.Endpoint)
		if ep == "" {
			return nil, fmt.Errorf("oss requires endpoint (e.g. oss-cn-hangzhou.aliyuncs.com)")
		}
		host, ssl, err := parseEndpoint(ep, true)
		if err != nil {
			return nil, err
		}
		r.Endpoint = host
		if c.UseSSL != nil {
			r.UseSSL = *c.UseSSL
		} else {
			r.UseSSL = ssl
		}

	case TypeCOS:
		// Tencent COS S3-compatible; e.g. cos.ap-guangzhou.myqcloud.com
		r.ProviderLabel = "Other"
		r.ForcePathStyle = false
		if c.ForcePathStyle != nil {
			r.ForcePathStyle = *c.ForcePathStyle
		}
		if r.Region == "" {
			r.Region = "us-east-1"
		}
		ep := strings.TrimSpace(c.Endpoint)
		if ep == "" {
			return nil, fmt.Errorf("cos requires endpoint (e.g. cos.ap-guangzhou.myqcloud.com)")
		}
		host, ssl, err := parseEndpoint(ep, true)
		if err != nil {
			return nil, err
		}
		r.Endpoint = host
		if c.UseSSL != nil {
			r.UseSSL = *c.UseSSL
		} else {
			r.UseSSL = ssl
		}

	case TypeQiniu:
		// Qiniu Kodo S3-compatible API; path-style often true.
		// User provides endpoint, e.g. s3-cn-east-1.qiniucs.com
		r.ProviderLabel = "Other"
		r.ForcePathStyle = true
		if c.ForcePathStyle != nil {
			r.ForcePathStyle = *c.ForcePathStyle
		}
		// region optional
		ep := strings.TrimSpace(c.Endpoint)
		if ep == "" {
			return nil, fmt.Errorf("qiniu requires endpoint (e.g. s3-cn-east-1.qiniucs.com)")
		}
		host, ssl, err := parseEndpoint(ep, true)
		if err != nil {
			return nil, err
		}
		r.Endpoint = host
		if c.UseSSL != nil {
			r.UseSSL = *c.UseSSL
		} else {
			r.UseSSL = ssl
		}

	case TypeOracle:
		// Oracle OCI Object Storage S3 compatibility API; path-style typically true.
		// User provides endpoint, e.g. <namespace>.compat.objectstorage.<region>.oraclecloud.com
		r.ProviderLabel = "Other"
		r.ForcePathStyle = true
		if c.ForcePathStyle != nil {
			r.ForcePathStyle = *c.ForcePathStyle
		}
		if r.Region == "" {
			r.Region = "us-ashburn-1"
		}
		ep := strings.TrimSpace(c.Endpoint)
		if ep == "" {
			return nil, fmt.Errorf("oracle requires endpoint (OCI S3 compatibility endpoint)")
		}
		host, ssl, err := parseEndpoint(ep, true)
		if err != nil {
			return nil, err
		}
		r.Endpoint = host
		if c.UseSSL != nil {
			r.UseSSL = *c.UseSSL
		} else {
			r.UseSSL = ssl
		}
	}

	return r, nil
}

func parseEndpoint(raw string, defaultSSL bool) (host string, useSSL bool, err error) {
	raw = strings.TrimSpace(raw)
	useSSL = defaultSSL
	if strings.Contains(raw, "://") {
		u, e := url.Parse(raw)
		if e != nil {
			return "", false, fmt.Errorf("invalid endpoint: %w", e)
		}
		host = u.Host
		if host == "" {
			host = u.Path
		}
		if u.Scheme == "http" {
			useSSL = false
		} else if u.Scheme == "https" {
			useSSL = true
		}
		return host, useSSL, nil
	}
	// host:port or host
	return raw, useSSL, nil
}
