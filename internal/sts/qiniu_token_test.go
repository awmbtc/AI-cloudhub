package sts

import (
	"strings"
	"testing"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

func TestQiniuDownloadTokenShape(t *testing.T) {
	tok, err := QiniuDownloadToken("AKID", "secret", "https://cdn.example.com/path/to/obj", time.Now().Add(time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "AKID:") {
		t.Fatal(tok)
	}
	parts := strings.Split(tok, ":")
	if len(parts) != 2 || parts[1] == "" {
		t.Fatal(tok)
	}
}

func TestQiniuSignedDownloadURL(t *testing.T) {
	u, deadline, err := QiniuSignedDownloadURL("AK", "SK", "https://host.example/bucket/key", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "e=") || !strings.Contains(u, "token=") {
		t.Fatal(u)
	}
	if deadline <= time.Now().Unix() {
		t.Fatal(deadline)
	}
}

func TestQiniuUploadToken(t *testing.T) {
	tok, err := QiniuUploadToken("AK", "SK", "mybucket:prefix/", time.Now().Add(time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ":")
	if len(parts) != 3 {
		t.Fatal(tok)
	}
}

func TestQiniuObjectDownloadBase(t *testing.T) {
	// S3-compat host includes bucket in path
	u := QiniuObjectDownloadBase("s3-cn-east-1.qiniucs.com", true, "mybucket", "a/b.txt")
	if u != "https://s3-cn-east-1.qiniucs.com/mybucket/a/b.txt" {
		t.Fatal(u)
	}
	// CDN-style: key only
	u2 := QiniuObjectDownloadBase("cdn.example.com", true, "mybucket", "a/b.txt")
	if u2 != "https://cdn.example.com/a/b.txt" {
		t.Fatal(u2)
	}
}

func TestQiniuObjectSignedGet(t *testing.T) {
	u, deadline, err := QiniuObjectSignedGet("AK", "SK", "cdn.example.com", true, "b", "k/obj", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "token=") || !strings.Contains(u, "e=") {
		t.Fatal(u)
	}
	if deadline <= time.Now().Unix() {
		t.Fatal(deadline)
	}
}

func TestApplyOptionalQiniuDownloadToken(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_QINIU_STS", "0")
	t.Setenv("AI_CLOUDHUB_S3_STS", "0")
	t.Setenv("AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN", "1")
	r := &provider.Resolved{
		Type:      provider.TypeQiniu,
		AccessKey: "AK",
		SecretKey: "SK",
		Endpoint:  "cdn.example.com",
		UseSSL:    true,
	}
	out, source, note := applyOptionalQiniuSTS(r, time.Hour, SourceEmbedded)
	if source != SourceQiniuDownload {
		t.Fatalf("source=%s", source)
	}
	if !strings.Contains(note, "qiniu_download") {
		t.Fatalf("note=%s", note)
	}
	if out.AccessKey != "AK" {
		t.Fatal(out.AccessKey)
	}
}
