package provider

import "testing"

func TestResolveR2(t *testing.T) {
	r, err := Resolve(TypeR2, Credentials{
		AccessKey: "ak",
		SecretKey: "sk",
		AccountID: "abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Endpoint != "abc123.r2.cloudflarestorage.com" {
		t.Fatalf("endpoint %s", r.Endpoint)
	}
	if !r.UseSSL {
		t.Fatal("expected ssl")
	}
	if r.Region != "auto" {
		t.Fatalf("region %s", r.Region)
	}
}

func TestResolveMinIO(t *testing.T) {
	r, err := Resolve(TypeMinIO, Credentials{
		AccessKey: "ak",
		SecretKey: "sk",
		Endpoint:  "http://127.0.0.1:9000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Endpoint != "127.0.0.1:9000" {
		t.Fatalf("endpoint %s", r.Endpoint)
	}
	if r.UseSSL {
		t.Fatal("expected no ssl")
	}
	if !r.ForcePathStyle {
		t.Fatal("expected path style")
	}
}

func TestResolveS3Default(t *testing.T) {
	r, err := Resolve(TypeS3, Credentials{AccessKey: "a", SecretKey: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Endpoint != "s3.amazonaws.com" {
		t.Fatalf("endpoint %s", r.Endpoint)
	}
}

func TestResolveB2(t *testing.T) {
	r, err := Resolve(TypeB2, Credentials{
		AccessKey: "b2ak",
		SecretKey: "b2sk",
		Endpoint:  "s3.us-west-000.backblazeb2.com",
		Region:    "us-west-000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Endpoint != "s3.us-west-000.backblazeb2.com" {
		t.Fatalf("endpoint %s", r.Endpoint)
	}
	if !r.ForcePathStyle {
		t.Fatal("expected path style for b2")
	}
	if !r.UseSSL {
		t.Fatal("expected ssl")
	}
	if r.Region != "us-west-000" {
		t.Fatalf("region %s", r.Region)
	}
	if r.AccessKey != "b2ak" || r.SecretKey != "b2sk" {
		t.Fatal("credentials not preserved")
	}
}

func TestResolveB2RequiresEndpoint(t *testing.T) {
	_, err := Resolve(TypeB2, Credentials{AccessKey: "a", SecretKey: "b"})
	if err == nil {
		t.Fatal("expected endpoint error")
	}
}

func TestResolveOSS(t *testing.T) {
	r, err := Resolve(TypeOSS, Credentials{
		AccessKey: "ossak",
		SecretKey: "osssk",
		Endpoint:  "oss-cn-hangzhou.aliyuncs.com",
		Region:    "cn-hangzhou",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Endpoint != "oss-cn-hangzhou.aliyuncs.com" {
		t.Fatalf("endpoint %s", r.Endpoint)
	}
	if r.ForcePathStyle {
		t.Fatal("expected virtual-hosted (force_path_style false) for oss")
	}
	if !r.UseSSL {
		t.Fatal("expected ssl")
	}
	if r.Region != "cn-hangzhou" {
		t.Fatalf("region %s", r.Region)
	}
}

func TestResolveOSSRequiresEndpoint(t *testing.T) {
	_, err := Resolve(TypeOSS, Credentials{AccessKey: "a", SecretKey: "b"})
	if err == nil {
		t.Fatal("expected endpoint error")
	}
}

func TestResolveCOS(t *testing.T) {
	r, err := Resolve(TypeCOS, Credentials{
		AccessKey: "cosak",
		SecretKey: "cossk",
		Endpoint:  "https://cos.ap-guangzhou.myqcloud.com",
		Region:    "ap-guangzhou",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Endpoint != "cos.ap-guangzhou.myqcloud.com" {
		t.Fatalf("endpoint %s", r.Endpoint)
	}
	if r.ForcePathStyle {
		t.Fatal("expected force_path_style false for cos")
	}
	if !r.UseSSL {
		t.Fatal("expected ssl from https")
	}
	if r.Region != "ap-guangzhou" {
		t.Fatalf("region %s", r.Region)
	}
}

func TestResolveCOSRequiresEndpoint(t *testing.T) {
	_, err := Resolve(TypeCOS, Credentials{AccessKey: "a", SecretKey: "b"})
	if err == nil {
		t.Fatal("expected endpoint error")
	}
}

func TestResolveQiniu(t *testing.T) {
	r, err := Resolve(TypeQiniu, Credentials{
		AccessKey: "qak",
		SecretKey: "qsk",
		Endpoint:  "s3-cn-east-1.qiniucs.com",
		Region:    "cn-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Endpoint != "s3-cn-east-1.qiniucs.com" {
		t.Fatalf("endpoint %s", r.Endpoint)
	}
	if !r.ForcePathStyle {
		t.Fatal("expected path style for qiniu")
	}
	if !r.UseSSL {
		t.Fatal("expected ssl")
	}
	if r.Region != "cn-east-1" {
		t.Fatalf("region %s", r.Region)
	}
	if r.AccessKey != "qak" || r.SecretKey != "qsk" {
		t.Fatal("credentials not preserved")
	}
}

func TestResolveQiniuRequiresEndpoint(t *testing.T) {
	_, err := Resolve(TypeQiniu, Credentials{AccessKey: "a", SecretKey: "b"})
	if err == nil {
		t.Fatal("expected endpoint error")
	}
}

func TestResolveOracle(t *testing.T) {
	r, err := Resolve(TypeOracle, Credentials{
		AccessKey: "oak",
		SecretKey: "osk",
		Endpoint:  "https://mynamespace.compat.objectstorage.us-ashburn-1.oraclecloud.com",
		Region:    "us-ashburn-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Endpoint != "mynamespace.compat.objectstorage.us-ashburn-1.oraclecloud.com" {
		t.Fatalf("endpoint %s", r.Endpoint)
	}
	if !r.ForcePathStyle {
		t.Fatal("expected path style for oracle")
	}
	if !r.UseSSL {
		t.Fatal("expected ssl from https")
	}
	if r.Region != "us-ashburn-1" {
		t.Fatalf("region %s", r.Region)
	}
	if r.AccessKey != "oak" || r.SecretKey != "osk" {
		t.Fatal("credentials not preserved")
	}
}

func TestResolveOracleDefaultRegion(t *testing.T) {
	r, err := Resolve(TypeOracle, Credentials{
		AccessKey: "oak",
		SecretKey: "osk",
		Endpoint:  "mynamespace.compat.objectstorage.us-ashburn-1.oraclecloud.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Region != "us-ashburn-1" {
		t.Fatalf("expected default region us-ashburn-1, got %s", r.Region)
	}
	if !r.ForcePathStyle {
		t.Fatal("expected path style for oracle")
	}
}

func TestResolveOracleRequiresEndpoint(t *testing.T) {
	_, err := Resolve(TypeOracle, Credentials{AccessKey: "a", SecretKey: "b"})
	if err == nil {
		t.Fatal("expected endpoint error")
	}
}
