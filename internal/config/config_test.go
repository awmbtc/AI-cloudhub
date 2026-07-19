package config

import "testing"

func TestValidateDefaultDev(t *testing.T) {
	c := Config{
		HTTPAddr:  ":8080",
		JWTSecret: DefaultJWTSecret,
		// MasterKey empty
	}
	r := c.Validate()
	if !r.OK() {
		t.Fatalf("default dev should only warn, got errors: %v", r.Errors)
	}
	if len(r.Warnings) < 2 {
		t.Fatalf("expected JWT default + master key warnings, got %v", r.Warnings)
	}
}

func TestValidateStrictFailsWeak(t *testing.T) {
	c := Config{
		HTTPAddr:  ":8080",
		JWTSecret: DefaultJWTSecret,
		Strict:    true,
	}
	r := c.Validate()
	if r.OK() {
		t.Fatal("strict mode should error on default JWT and missing master key")
	}
}

func TestValidateShortJWT(t *testing.T) {
	c := Config{HTTPAddr: ":8080", JWTSecret: "short", Strict: true}
	r := c.Validate()
	if r.OK() {
		t.Fatal("expected short JWT error in strict mode")
	}
}

func TestValidateStrongOK(t *testing.T) {
	c := Config{
		HTTPAddr:  ":8080",
		JWTSecret: "this-is-a-long-enough-jwt-secret",
		MasterKey: "some-master-key-material",
		Strict:    true,
	}
	r := c.Validate()
	if !r.OK() {
		t.Fatalf("expected ok: %v", r.Errors)
	}
	if len(r.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", r.Warnings)
	}
}

func TestValidateEmptyHTTP(t *testing.T) {
	c := Config{JWTSecret: "this-is-a-long-enough-jwt-secret", MasterKey: "k"}
	r := c.Validate()
	if r.OK() {
		t.Fatal("empty HTTP_ADDR should error")
	}
}
