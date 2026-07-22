// Command minio-seed ensures a bucket and puts sample objects for live smoke.
// Usage:
//
//	go run ./scripts/minio-seed -endpoint 127.0.0.1:9000 -bucket testbucket -prefix ws/ \
//	  -ak minioadmin -sk minioadmin file1:hello file2:world
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/s3store"
)

func main() {
	endpoint := flag.String("endpoint", "127.0.0.1:9000", "host:port (no scheme)")
	accessKey := flag.String("ak", "minioadmin", "access key")
	secretKey := flag.String("sk", "minioadmin", "secret key")
	bucket := flag.String("bucket", "testbucket", "bucket name")
	prefix := flag.String("prefix", "ws/", "key prefix (drive prefix)")
	region := flag.String("region", "us-east-1", "region")
	ssl := flag.Bool("ssl", false, "use HTTPS")
	flag.Parse()

	objs := flag.Args()
	if len(objs) == 0 {
		objs = []string{"smoke-a.txt:smoke-body-a", "nested/smoke-b.txt:smoke-body-b"}
	}

	st, err := s3store.New(*endpoint, *accessKey, *secretKey, *region, *ssl)
	if err != nil {
		fail("client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := st.EnsureBucket(ctx, *bucket); err != nil {
		fail("EnsureBucket: %v", err)
	}

	pfx := *prefix
	if pfx != "" && !strings.HasSuffix(pfx, "/") {
		pfx += "/"
	}

	for _, spec := range objs {
		name, body, ok := strings.Cut(spec, ":")
		if !ok || name == "" {
			fail("object spec must be name:body, got %q", spec)
		}
		key := pfx + strings.TrimLeft(name, "/")
		if err := st.Put(ctx, *bucket, key, strings.NewReader(body), int64(len(body)), "text/plain"); err != nil {
			fail("Put %s: %v", key, err)
		}
		fmt.Printf("put s3://%s/%s (%d bytes)\n", *bucket, key, len(body))
	}
}

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "minio-seed: "+format+"\n", args...)
	os.Exit(1)
}
