package sts

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

// SourceOCIPAR labels sessions that include a best-effort OCI Pre-Authenticated Request assist.
const SourceOCIPAR = "oci_par"

// SourceOCISecret labels when a Customer Secret Key was minted via Identity API.
const SourceOCISecret = "oci_secret"

func ociPAREnabled() bool {
	return envFlagTrue("AI_CLOUDHUB_OCI_PAR") || envFlagTrue("AI_CLOUDHUB_ORACLE_PAR")
}

func ociCreateSecretEnabled() bool {
	return envFlagTrue("AI_CLOUDHUB_OCI_CREATE_SECRET") || envFlagTrue("AI_CLOUDHUB_ORACLE_CREATE_SECRET")
}

// OCINamespace returns object storage namespace from env (required for PAR).
func OCINamespace() string {
	return strings.TrimSpace(os.Getenv("AI_CLOUDHUB_OCI_NAMESPACE"))
}

func ociObjectStorageBase(region, namespace string) string {
	if ep := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_OCI_OBJECT_ENDPOINT")); ep != "" {
		return strings.TrimRight(ep, "/")
	}
	// Regional object storage API
	return fmt.Sprintf("https://objectstorage.%s.oraclecloud.com", region)
}

// TryOCICreateCustomerSecretKey POSTs Identity to mint a Customer Secret Key
// for the API-key user. Secret is only returned at create time.
//
//	POST /20160918/users/{userId}/customerSecretKeys
func TryOCICreateCustomerSecretKey(key *OCIAPIKey, displayName string) (accessKey, secretKey string, raw map[string]interface{}, err error) {
	if key == nil || key.PrivateKey == nil {
		return "", "", nil, fmt.Errorf("oci key required")
	}
	if displayName == "" {
		displayName = "ai-cloudhub-" + time.Now().UTC().Format("20060102-150405")
	}
	path := "/20160918/users/" + url.PathEscape(key.UserOCID) + "/customerSecretKeys"
	base := fmt.Sprintf("https://identity.%s.oraclecloud.com", key.Region)
	if ep := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_OCI_IDENTITY_ENDPOINT")); ep != "" {
		base = strings.TrimRight(ep, "/")
	}
	payload, _ := json.Marshal(map[string]string{"displayName": displayName})
	out, err := ociSignedJSON(key, http.MethodPost, base, path, payload)
	if err != nil {
		return "", "", nil, err
	}
	ak, _ := out["id"].(string)
	sk, _ := out["key"].(string)
	if ak == "" {
		ak, _ = out["accessKey"].(string)
	}
	if sk == "" {
		sk, _ = out["secretKey"].(string)
	}
	if ak == "" || sk == "" {
		return "", "", out, fmt.Errorf("oci create secret: response missing id/key")
	}
	return ak, sk, out, nil
}

// TryOCICreateObjectPAR creates a time-limited ObjectRead PAR for one object name.
// Requires AI_CLOUDHUB_OCI_NAMESPACE. Best-effort Stage C.
//
//	POST /n/{namespace}/b/{bucket}/p/
func TryOCICreateObjectPAR(key *OCIAPIKey, namespace, bucket, objectName string, ttl time.Duration) (accessURI string, raw map[string]interface{}, err error) {
	if key == nil || key.PrivateKey == nil {
		return "", nil, fmt.Errorf("oci key required")
	}
	namespace = strings.TrimSpace(namespace)
	bucket = strings.TrimSpace(bucket)
	objectName = strings.TrimLeft(strings.TrimSpace(objectName), "/")
	if namespace == "" || bucket == "" || objectName == "" {
		return "", nil, fmt.Errorf("namespace, bucket, objectName required")
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	if ttl > 24*time.Hour {
		ttl = 24 * time.Hour
	}
	timeExpires := time.Now().UTC().Add(ttl).Format(time.RFC3339)
	path := fmt.Sprintf("/n/%s/b/%s/p/", url.PathEscape(namespace), url.PathEscape(bucket))
	base := ociObjectStorageBase(key.Region, namespace)
	body := map[string]interface{}{
		"name":        "ai-cloudhub-" + time.Now().UTC().Format("150405"),
		"accessType":  "ObjectRead",
		"timeExpires": timeExpires,
		"objectName":  objectName,
	}
	payload, _ := json.Marshal(body)
	out, err := ociSignedJSON(key, http.MethodPost, base, path, payload)
	if err != nil {
		return "", nil, err
	}
	// accessUri is relative; full URI often accessUri prefixed with object storage host
	uri, _ := out["accessUri"].(string)
	if uri == "" {
		uri, _ = out["fullPath"].(string)
	}
	if uri != "" && !strings.HasPrefix(uri, "http") {
		uri = base + uri
	}
	if uri == "" {
		return "", out, fmt.Errorf("oci par: no accessUri in response")
	}
	return uri, out, nil
}

// ociSignedJSON signs and executes a JSON REST call against OCI.
func ociSignedJSON(key *OCIAPIKey, method, base, pathAndQuery string, body []byte) (map[string]interface{}, error) {
	full := strings.TrimRight(base, "/") + pathAndQuery
	var rdr io.Reader
	if len(body) > 0 {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, full, rdr)
	if err != nil {
		return nil, err
	}
	host := req.URL.Host
	reqTarget := req.URL.RequestURI()
	date := time.Now().UTC().Format(http.TimeFormat)
	auth, err := OCISignHeaders(key, method, host, reqTarget, date, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("date", date)
	req.Header.Set("host", host)
	req.Header.Set("Authorization", auth)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
		sum := sha256.Sum256(body)
		req.Header.Set("x-content-sha256", base64.StdEncoding.EncodeToString(sum[:]))
	}
	client := &http.Client{Timeout: 20 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("oci HTTP %d: %s", res.StatusCode, truncateSTS(string(raw), 240))
	}
	var out map[string]interface{}
	if len(raw) == 0 {
		return map[string]interface{}{}, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("oci json: %w", err)
	}
	return out, nil
}

// applyOCIStageCAssists runs optional secret mint + PAR after IAM validate path.
// Mutates note/source/out credentials when successful.
func applyOCIStageCAssists(key *OCIAPIKey, resolved *provider.Resolved, duration time.Duration, source, note string) (out *provider.Resolved, src, n string) {
	out = resolved
	src = source
	n = note
	if key == nil {
		return out, src, n
	}

	// Mint Customer Secret Key when enabled and provider has no AK.
	if ociCreateSecretEnabled() && (out == nil || out.AccessKey == "") {
		ak, sk, _, err := TryOCICreateCustomerSecretKey(key, "")
		if err != nil {
			n = appendNote(n, "OCI create Customer Secret Key failed: "+err.Error())
		} else {
			cp := *out
			cp.AccessKey = ak
			cp.SecretKey = sk
			out = &cp
			src = SourceOCISecret
			n = appendNote(n, "OCI Customer Secret Key minted (source=oci_secret); store securely — secret shown only once by OCI")
		}
	} else if ociCreateSecretEnabled() && out != nil && out.AccessKey != "" {
		n = appendNote(n, "OCI create-secret skipped: provider already has access_key")
	}

	// Object PAR sample for a placeholder object under bucket (namespace required).
	if ociPAREnabled() {
		ns := OCINamespace()
		if ns == "" {
			n = appendNote(n, "OCI PAR enabled but AI_CLOUDHUB_OCI_NAMESPACE unset")
		} else if out != nil {
			// Bucket not on Resolved — use env sample bucket or skip
			bucket := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_OCI_PAR_BUCKET"))
			obj := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_OCI_PAR_OBJECT"))
			if obj == "" {
				obj = "__sample_key__"
			}
			if bucket == "" {
				n = appendNote(n, "OCI PAR enabled but AI_CLOUDHUB_OCI_PAR_BUCKET unset (sample PAR skipped)")
			} else {
				uri, _, err := TryOCICreateObjectPAR(key, ns, bucket, obj, duration)
				if err != nil {
					n = appendNote(n, "OCI PAR create failed: "+err.Error())
				} else {
					max := 120
					if len(uri) < max {
						max = len(uri)
					}
					n = appendNote(n, fmt.Sprintf("OCI PAR ObjectRead sample (source=oci_par): %s…", uri[:max]))
					if src == SourceOCIIAM || src == SourceEmbedded || src == SourceRefresh {
						src = SourceOCIPAR
					}
				}
			}
		}
	}
	return out, src, n
}

func appendNote(note, extra string) string {
	extra = strings.TrimSpace(extra)
	if extra == "" {
		return note
	}
	if note == "" {
		return extra
	}
	return note + " | " + extra
}
