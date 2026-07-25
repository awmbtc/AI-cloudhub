package sts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

func TestTryOCICreateCustomerSecretKeyMock(t *testing.T) {
	ClearOCIValidateCache()
	key, _ := testOCIKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "customerSecretKeys") {
			http.Error(w, "bad path", 404)
			return
		}
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "no auth", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id":  "ocid1.credential.oc1..ak",
			"key": "super-secret-once",
		})
	}))
	defer srv.Close()
	t.Setenv("AI_CLOUDHUB_OCI_IDENTITY_ENDPOINT", srv.URL)
	ak, sk, _, err := TryOCICreateCustomerSecretKey(key, "test")
	if err != nil {
		t.Fatal(err)
	}
	if ak == "" || sk != "super-secret-once" {
		t.Fatalf("%s %s", ak, sk)
	}
}

func TestTryOCICreateObjectPARMock(t *testing.T) {
	key, _ := testOCIKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/p/") {
			http.Error(w, "bad", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"accessUri": "/p/abcdef/o/__sample_key__",
		})
	}))
	defer srv.Close()
	t.Setenv("AI_CLOUDHUB_OCI_OBJECT_ENDPOINT", srv.URL)
	uri, _, err := TryOCICreateObjectPAR(key, "ns", "bucket", "__sample_key__", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(uri, "__sample_key__") {
		t.Fatal(uri)
	}
}

func TestApplyOCIStageCAssistsSecret(t *testing.T) {
	ClearOCIValidateCache()
	key, pemStr := testOCIKey(t)
	idSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "customerSecretKeys") {
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "ak1", "key": "sk1"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "u", "id": key.UserOCID})
	}))
	defer idSrv.Close()
	t.Setenv("AI_CLOUDHUB_OCI_IDENTITY_ENDPOINT", idSrv.URL)
	t.Setenv("AI_CLOUDHUB_ORACLE_STS", "0")
	t.Setenv("AI_CLOUDHUB_S3_STS", "0")
	t.Setenv("AI_CLOUDHUB_ORACLE_NATIVE_IAM", "1")
	t.Setenv("AI_CLOUDHUB_OCI_CREATE_SECRET", "1")
	t.Setenv("AI_CLOUDHUB_OCI_PAR", "0")
	t.Setenv("AI_CLOUDHUB_OCI_TENANCY_OCID", key.TenancyOCID)
	t.Setenv("AI_CLOUDHUB_OCI_USER_OCID", key.UserOCID)
	t.Setenv("AI_CLOUDHUB_OCI_FINGERPRINT", key.Fingerprint)
	t.Setenv("AI_CLOUDHUB_OCI_PRIVATE_KEY_PEM", pemStr)
	t.Setenv("AI_CLOUDHUB_OCI_REGION", "us-ashburn-1")

	r := &provider.Resolved{Type: provider.TypeOracle, Endpoint: "x.compat.objectstorage.us-ashburn-1.oraclecloud.com", UseSSL: true}
	out, source, note := applyOptionalOracleSTS(r, time.Hour, SourceEmbedded)
	if source != SourceOCISecret {
		t.Fatalf("source=%s note=%s", source, note)
	}
	if out.AccessKey != "ak1" || out.SecretKey != "sk1" {
		t.Fatalf("%+v", out)
	}
	if !strings.Contains(note, "minted") {
		t.Fatal(note)
	}
}
