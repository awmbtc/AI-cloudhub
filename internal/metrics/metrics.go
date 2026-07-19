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
)

// IncHTTP increments HTTP request counter.
func IncHTTP() { HTTPRequests.Add(1) }

// IncSession increments session issue counter.
func IncSession() { SessionsIssued.Add(1) }

// IncJobCreated increments job create counter.
func IncJobCreated() { JobsCreated.Add(1) }

// IncJobClaimed increments job claim counter.
func IncJobClaimed() { JobsClaimed.Add(1) }

// IncRateLimited increments rate-limit counter.
func IncRateLimited() { RateLimited.Add(1) }

// Handler serves Prometheus text exposition (no auth).
func Handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_http_requests_total Authenticated and public HTTP hits tracked\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_http_requests_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_http_requests_total %d\n", HTTPRequests.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_sessions_issued_total Mount STS sessions issued\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_sessions_issued_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_sessions_issued_total %d\n", SessionsIssued.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_created_total BYOC jobs created\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_created_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_created_total %d\n", JobsCreated.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_claimed_total BYOC jobs claimed\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_claimed_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_claimed_total %d\n", JobsClaimed.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_rate_limited_total Rate limited requests\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_rate_limited_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_rate_limited_total %d\n", RateLimited.Load())
}
