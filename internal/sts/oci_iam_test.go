package sts

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

func testOCIKey(t *testing.T) (*OCIAPIKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
	k := &OCIAPIKey{
		TenancyOCID: "ocid1.tenancy.oc1..aaa",
		UserOCID:    "ocid1.user.oc1..bbb",
		Fingerprint: "https://example.com/fp",
		PrivateKey:  key,
		Region:      "us-ashburn-1",
	}
	// fix fingerprint to look real
	k.Fingerprint = "aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99"
	return k, string(pemBytes)
}

func TestParseOCIPrivateKeyPEM(t *testing.T) {
	_, pemStr := testOCIKey(t)
	k, err := ParseOCIPrivateKeyPEM([]byte(pemStr))
	if err != nil || k == nil {
		t.Fatal(err)
	}
}

func TestOCISignHeadersNonEmpty(t *testing.T) {
	key, _ := testOCIKey(t)
	date := time.Now().UTC().Format(http.TimeFormat)
	auth, err := OCISignHeaders(key, "GET", "identity.us-ashburn-1.oraclecloud.com", "/20160918/users/x", date, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(auth, "Signature version=") || !strings.Contains(auth, "rsa-sha256") {
		t.Fatal(auth)
	}
}

func TestTryOCIValidateUserMock(t *testing.T) {
	key, pemStr := testOCIKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "no auth", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id":   key.UserOCID,
			"name": "test-user",
		})
	}))
	defer srv.Close()
	t.Setenv("AI_CLOUDHUB_OCI_IDENTITY_ENDPOINT", srv.URL)
	// re-sign uses server host — TryOCIValidateUser with override
	// Load from key directly
	user, err := TryOCIValidateUser(key)
	// Default path hits real host; with env override it should use mock
	if err != nil {
		// If override path works
		t.Log(err)
	}
	// Force call with mock by setting env and using Load + validate
	t.Setenv("AI_CLOUDHUB_OCI_TENANCY_OCID", key.TenancyOCID)
	t.Setenv("AI_CLOUDHUB_OCI_USER_OCID", key.UserOCID)
	t.Setenv("AI_CLOUDHUB_OCI_FINGERPRINT", key.Fingerprint)
	t.Setenv("AI_CLOUDHUB_OCI_PRIVATE_KEY_PEM", pemStr)
	t.Setenv("AI_CLOUDHUB_OCI_REGION", "us-ashburn-1")
	loaded, err := LoadOCIAPIKeyFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	user, err = TryOCIValidateUser(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if user["name"] != "test-user" {
		t.Fatalf("%v", user)
	}
}

func TestTryOCIValidateUserCache(t *testing.T) {
	ClearOCIValidateCache()
	t.Cleanup(ClearOCIValidateCache)
	key, _ := testOCIKey(t)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "cached-user", "id": key.UserOCID})
	}))
	defer srv.Close()
	t.Setenv("AI_CLOUDHUB_OCI_IDENTITY_ENDPOINT", srv.URL)
	t.Setenv("AI_CLOUDHUB_OCI_IAM_CACHE_SEC", "60")

	u1, err := TryOCIValidateUser(key)
	if err != nil {
		t.Fatal(err)
	}
	u2, err := TryOCIValidateUser(key)
	if err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("expected 1 HTTP hit with cache, got %d", hits)
	}
	if u1["name"] != "cached-user" || u2["name"] != "cached-user" {
		t.Fatalf("%v %v", u1, u2)
	}

	// Cache disabled → second call hits network again
	ClearOCIValidateCache()
	t.Setenv("AI_CLOUDHUB_OCI_IAM_CACHE_SEC", "0")
	_, _ = TryOCIValidateUser(key)
	_, _ = TryOCIValidateUser(key)
	if hits != 3 {
		t.Fatalf("expected 3 hits with cache off (1 prior + 2), got %d", hits)
	}
}

func TestApplyOptionalOracleNativeIAM(t *testing.T) {
	ClearOCIValidateCache()
	t.Cleanup(ClearOCIValidateCache)
	key, pemStr := testOCIKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "u1", "id": key.UserOCID})
	}))
	defer srv.Close()
	t.Setenv("AI_CLOUDHUB_ORACLE_STS", "0")
	t.Setenv("AI_CLOUDHUB_S3_STS", "0")
	t.Setenv("AI_CLOUDHUB_ORACLE_NATIVE_IAM", "1")
	t.Setenv("AI_CLOUDHUB_OCI_IDENTITY_ENDPOINT", srv.URL)
	t.Setenv("AI_CLOUDHUB_OCI_TENANCY_OCID", key.TenancyOCID)
	t.Setenv("AI_CLOUDHUB_OCI_USER_OCID", key.UserOCID)
	t.Setenv("AI_CLOUDHUB_OCI_FINGERPRINT", key.Fingerprint)
	t.Setenv("AI_CLOUDHUB_OCI_PRIVATE_KEY_PEM", pemStr)
	t.Setenv("AI_CLOUDHUB_OCI_REGION", "us-ashburn-1")

	r := &provider.Resolved{
		Type:      provider.TypeOracle,
		AccessKey: "ocid.s3ak",
		SecretKey: "s3sk",
		Endpoint:  "namespace.compat.objectstorage.us-ashburn-1.oraclecloud.com",
		UseSSL:    true,
	}
	out, source, note := applyOptionalOracleSTS(r, time.Hour, SourceEmbedded)
	if source != SourceOCIIAM {
		t.Fatalf("source=%s note=%s", source, note)
	}
	if !strings.Contains(note, "validated") {
		t.Fatalf("note=%s", note)
	}
	if out.AccessKey != "ocid.s3ak" {
		t.Fatal(out.AccessKey)
	}
}
