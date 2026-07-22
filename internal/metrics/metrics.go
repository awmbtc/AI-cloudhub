package metrics

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

// Counters for lightweight Prometheus text format.
var (
	HTTPRequests   atomic.Uint64
	SessionsIssued atomic.Uint64
	JobsCreated    atomic.Uint64
	JobsClaimed    atomic.Uint64
	RateLimited    atomic.Uint64
	// STS source breakdown (best-effort).
	STSEmbedded atomic.Uint64
	STSRefresh  atomic.Uint64
	STSMinio    atomic.Uint64
	STSAWS      atomic.Uint64
	STSS3       atomic.Uint64 // s3_sts: S3-compatible AssumeRole (non-minio / non-aws)
	STSAliyun   atomic.Uint64 // aliyun_sts: Aliyun RAM STS
	STSTencent  atomic.Uint64 // tencent_sts: Tencent CAM STS
	STSQiniu    atomic.Uint64 // qiniu_sts
	STSOracle   atomic.Uint64 // oracle_sts
	Snapshots   atomic.Uint64
)

// IncHTTP increments HTTP request counter.
func IncHTTP() { HTTPRequests.Add(1) }

// IncSession increments session issue counter.
func IncSession() { SessionsIssued.Add(1) }

// IncSTSSource tracks STS credential source label from session.Issue.
func IncSTSSource(source string) {
	switch source {
	case "minio_sts":
		STSMinio.Add(1)
	case "aws_sts":
		STSAWS.Add(1)
	case "s3_sts":
		STSS3.Add(1)
	case "aliyun_sts":
		STSAliyun.Add(1)
	case "tencent_sts":
		STSTencent.Add(1)
	case "qiniu_sts":
		STSQiniu.Add(1)
	case "oracle_sts":
		STSOracle.Add(1)
	case "refresh":
		STSRefresh.Add(1)
	default:
		STSEmbedded.Add(1)
	}
}

// IncJobCreated increments job create counter.
func IncJobCreated() { JobsCreated.Add(1) }

// IncJobClaimed increments job claim counter.
func IncJobClaimed() { JobsClaimed.Add(1) }

// IncRateLimited increments rate-limit counter.
func IncRateLimited() { RateLimited.Add(1) }

// IncSnapshot increments snapshot create counter.
func IncSnapshot() { Snapshots.Add(1) }

// Handler serves Prometheus text exposition (no auth).
func Handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_http_requests_total Authenticated and public HTTP hits tracked\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_http_requests_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_http_requests_total %d\n", HTTPRequests.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_sessions_issued_total Mount STS sessions issued\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_sessions_issued_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_sessions_issued_total %d\n", SessionsIssued.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_sts_source_total Sessions by STS source\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_sts_source_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_sts_source_total{source=\"embedded\"} %d\n", STSEmbedded.Load())
	_, _ = fmt.Fprintf(w, "aicloudhub_sts_source_total{source=\"refresh\"} %d\n", STSRefresh.Load())
	_, _ = fmt.Fprintf(w, "aicloudhub_sts_source_total{source=\"minio_sts\"} %d\n", STSMinio.Load())
	_, _ = fmt.Fprintf(w, "aicloudhub_sts_source_total{source=\"aws_sts\"} %d\n", STSAWS.Load())
	_, _ = fmt.Fprintf(w, "aicloudhub_sts_source_total{source=\"s3_sts\"} %d\n", STSS3.Load())
	_, _ = fmt.Fprintf(w, "aicloudhub_sts_source_total{source=\"aliyun_sts\"} %d\n", STSAliyun.Load())
	_, _ = fmt.Fprintf(w, "aicloudhub_sts_source_total{source=\"tencent_sts\"} %d\n", STSTencent.Load())
	_, _ = fmt.Fprintf(w, "aicloudhub_sts_source_total{source=\"qiniu_sts\"} %d\n", STSQiniu.Load())
	_, _ = fmt.Fprintf(w, "aicloudhub_sts_source_total{source=\"oracle_sts\"} %d\n", STSOracle.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_created_total BYOC jobs created\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_created_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_created_total %d\n", JobsCreated.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_claimed_total BYOC jobs claimed\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_claimed_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_claimed_total %d\n", JobsClaimed.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_rate_limited_total Rate limited requests\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_rate_limited_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_rate_limited_total %d\n", RateLimited.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_snapshots_created_total Metadata snapshots created\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_snapshots_created_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_snapshots_created_total %d\n", Snapshots.Load())
}
