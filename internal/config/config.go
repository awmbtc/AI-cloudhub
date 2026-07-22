package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// MinJWTSecretLen is the minimum accepted JWT secret length when validation runs.
const MinJWTSecretLen = 16

// DefaultJWTSecret is the insecure local-dev default (must be overridden in production).
const DefaultJWTSecret = "dev-change-me"

// Config holds process configuration from environment variables.
type Config struct {
	HTTPAddr string

	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3UseSSL    bool
	S3Region    string

	// BucketPrefix is prepended to workspace bucket names, e.g. "ws-".
	BucketPrefix string

	// JWTSecret signs simple session tokens (dev only; replace for production).
	JWTSecret string

	// MasterKey is AI_CLOUDHUB_MASTER_KEY for envelope encryption of provider secrets.
	// Empty = plaintext/dev mode (not for production). See internal/crypto/secretbox.
	MasterKey string

	// PresignTTL is how long upload/download URLs stay valid.
	PresignTTL time.Duration

	// DBPath is the SQLite file path, or "memory" for in-memory store.
	// Env: AI_CLOUDHUB_DB (default ./data/ai-cloudhub.db).
	DBPath string

	// Strict when true (AI_CLOUDHUB_STRICT=1) turns Validate warnings into hard errors
	// for weak JWT and missing master key.
	Strict bool

	// AllowRegister when false disables POST /v1/auth/register except bootstrap
	// (zero users). Env: AI_CLOUDHUB_ALLOW_REGISTER (default true).
	AllowRegister bool

	// TokenTTL is access-token lifetime. Env: AI_CLOUDHUB_TOKEN_TTL_HOURS (default 24).
	TokenTTL time.Duration
	// RefreshTTL is refresh-token lifetime. Env: AI_CLOUDHUB_REFRESH_TTL_HOURS (default 168 = 7d).
	RefreshTTL time.Duration

	// MaxBodyBytes caps request bodies. Env: AI_CLOUDHUB_MAX_BODY_BYTES (default 1MiB).
	MaxBodyBytes int64

	// HSTS enables Strict-Transport-Security response header (set only behind HTTPS).
	// Env: AI_CLOUDHUB_HSTS=1
	HSTS bool

	// MetricsToken when set requires Authorization: Bearer <token> (or ?token=) for /metrics.
	// Env: AI_CLOUDHUB_METRICS_TOKEN.
	MetricsToken string

	// AuthRatePerMin limits login/register attempts per client IP (default 20).
	AuthRatePerMin int
	// AuthFailMax consecutive failures before lockout (default 8).
	AuthFailMax int
	// AuthFailWindowMin lockout window minutes (default 15).
	AuthFailWindowMin int

	// AdminCIDRs when non-empty, restricts admin API routes to these IPs/CIDRs
	// (comma-separated). Empty = no extra IP restriction.
	// Env: AI_CLOUDHUB_ADMIN_CIDRS (e.g. "127.0.0.1,10.0.0.0/8")
	AdminCIDRs []string

	// PolicyFile is optional external JSON policy (AI_CLOUDHUB_POLICY_FILE).
	// Empty = built-in scope/drive/path checks only. See docs/POLICY.md.
	PolicyFile string
	// PolicyReloadSec re-reads the policy file when mtime changes (0 = load once at start).
	// Env: AI_CLOUDHUB_POLICY_RELOAD_SEC
	PolicyReloadSec int
}

// Load reads configuration from the environment with safe defaults for local docker-compose.
func Load() Config {
	ttlH := getenvInt("AI_CLOUDHUB_TOKEN_TTL_HOURS", 24)
	if ttlH <= 0 {
		ttlH = 24
	}
	refH := getenvInt("AI_CLOUDHUB_REFRESH_TTL_HOURS", 168)
	if refH <= 0 {
		refH = 168
	}
	maxBody := getenvInt("AI_CLOUDHUB_MAX_BODY_BYTES", 1<<20) // 1 MiB
	if maxBody <= 0 {
		maxBody = 1 << 20
	}
	return Config{
		HTTPAddr:          getenv("HTTP_ADDR", ":8080"),
		S3Endpoint:        getenv("S3_ENDPOINT", "127.0.0.1:9000"),
		S3AccessKey:       getenv("S3_ACCESS_KEY", "minioadmin"),
		S3SecretKey:       getenv("S3_SECRET_KEY", "minioadmin"),
		S3UseSSL:          getenvBool("S3_USE_SSL", false),
		S3Region:          getenv("S3_REGION", "us-east-1"),
		BucketPrefix:      getenv("BUCKET_PREFIX", "ws-"),
		JWTSecret:         getenv("JWT_SECRET", DefaultJWTSecret),
		MasterKey:         getenv("AI_CLOUDHUB_MASTER_KEY", ""),
		PresignTTL:        time.Duration(getenvInt("PRESIGN_TTL_MIN", 15)) * time.Minute,
		DBPath:            getenv("AI_CLOUDHUB_DB", "./data/ai-cloudhub.db"),
		Strict:            getenvBool("AI_CLOUDHUB_STRICT", false),
		AllowRegister:     getenvBool("AI_CLOUDHUB_ALLOW_REGISTER", true),
		TokenTTL:          time.Duration(ttlH) * time.Hour,
		RefreshTTL:        time.Duration(refH) * time.Hour,
		MaxBodyBytes:      int64(maxBody),
		HSTS:              getenvBool("AI_CLOUDHUB_HSTS", false),
		MetricsToken:      getenv("AI_CLOUDHUB_METRICS_TOKEN", ""),
		AuthRatePerMin:    getenvInt("AI_CLOUDHUB_AUTH_RATE_PER_MIN", 20),
		AuthFailMax:       getenvInt("AI_CLOUDHUB_AUTH_FAIL_MAX", 8),
		AuthFailWindowMin: getenvInt("AI_CLOUDHUB_AUTH_FAIL_WINDOW_MIN", 15),
		AdminCIDRs:        splitCSV(getenv("AI_CLOUDHUB_ADMIN_CIDRS", "")),
		PolicyFile:        getenv("AI_CLOUDHUB_POLICY_FILE", ""),
		PolicyReloadSec:   getenvInt("AI_CLOUDHUB_POLICY_RELOAD_SEC", 0),
	}
}

func splitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ValidationResult holds hard errors and soft warnings from Validate.
type ValidationResult struct {
	Errors   []string
	Warnings []string
}

// OK reports whether there are no hard errors.
func (v ValidationResult) OK() bool {
	return len(v.Errors) == 0
}

// Validate checks config safety. Hard errors fail process start always;
// warnings are logged; under Strict mode weak-dev settings become errors.
func (c Config) Validate() ValidationResult {
	var r ValidationResult
	sec := strings.TrimSpace(c.JWTSecret)
	if sec == "" {
		r.Errors = append(r.Errors, "JWT_SECRET is empty")
	} else if sec == DefaultJWTSecret {
		// Check default before length: DefaultJWTSecret may be shorter than MinJWTSecretLen.
		msg := "JWT_SECRET is the insecure default (dev-change-me); set a strong secret for production"
		if c.Strict {
			r.Errors = append(r.Errors, msg)
		} else {
			r.Warnings = append(r.Warnings, msg)
		}
	} else if len(sec) < MinJWTSecretLen {
		msg := fmt.Sprintf("JWT_SECRET too short (%d chars; need >= %d)", len(sec), MinJWTSecretLen)
		if c.Strict {
			r.Errors = append(r.Errors, msg)
		} else {
			r.Warnings = append(r.Warnings, msg)
		}
	}

	if strings.TrimSpace(c.MasterKey) == "" {
		msg := "AI_CLOUDHUB_MASTER_KEY unset — provider secrets stored in plaintext"
		if c.Strict {
			r.Errors = append(r.Errors, msg)
		} else {
			r.Warnings = append(r.Warnings, msg)
		}
	}

	if c.HTTPAddr == "" {
		r.Errors = append(r.Errors, "HTTP_ADDR is empty")
	}

	if c.Strict && c.AllowRegister {
		r.Warnings = append(r.Warnings, "AI_CLOUDHUB_ALLOW_REGISTER is true under STRICT; consider false after bootstrap")
	}
	if c.TokenTTL > 0 && c.TokenTTL < time.Hour {
		r.Warnings = append(r.Warnings, "AI_CLOUDHUB_TOKEN_TTL_HOURS < 1h is very short")
	}
	return r
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getenvBool(k string, def bool) bool {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getenvInt(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
