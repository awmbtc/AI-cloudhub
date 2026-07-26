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
	JobsCompleted  atomic.Uint64
	JobsCancelled  atomic.Uint64
	RateLimited    atomic.Uint64
	// STS source breakdown (best-effort).
	STSEmbedded atomic.Uint64
	STSRefresh  atomic.Uint64
	STSMinio    atomic.Uint64
	STSAWS      atomic.Uint64
	STSS3       atomic.Uint64 // s3_sts: S3-compatible AssumeRole (non-minio / non-aws)
	STSAliyun   atomic.Uint64 // aliyun_sts: Aliyun RAM STS
	STSTencent  atomic.Uint64 // tencent_sts: Tencent CAM STS
	STSQiniu         atomic.Uint64 // qiniu_sts (S3-compat)
	STSOracle        atomic.Uint64 // oracle_sts (S3-compat)
	STSQiniuDownload atomic.Uint64 // qiniu_download (native HMAC token)
	STSOCIIAM        atomic.Uint64 // oci_iam (API-key validate)
	STSOCIPAR        atomic.Uint64 // oci_par (Pre-Authenticated Request assist)
	STSOCISecret     atomic.Uint64 // oci_secret (Customer Secret Key mint)
	Snapshots        atomic.Uint64
	// Stage C control-plane product signals.
	MarketplaceInstalls   atomic.Uint64
	MarketplaceCheckouts  atomic.Uint64
	MarketplacePaid       atomic.Uint64
	ConnectorsCreated     atomic.Uint64
	MemoryPuts            atomic.Uint64
	MemorySearches        atomic.Uint64
	JobsWithConnector     atomic.Uint64
	JobsCompletedConn     atomic.Uint64
	// Job ops (lease / timeout / heartbeat / webhook).
	JobsTimeout       atomic.Uint64
	JobsLeaseReclaim  atomic.Uint64
	JobsMaxAttempts   atomic.Uint64
	JobsHeartbeat     atomic.Uint64
	JobsWebhookOK     atomic.Uint64
	JobsWebhookFail   atomic.Uint64
	JobsWebhookDead   atomic.Uint64
	JobsWebhookPurged atomic.Uint64
	// Webhook outbox queue depth gauges (refreshed on /metrics scrape when jobs service is wired).
	JobsWebhookPendingGauge   atomic.Uint64
	JobsWebhookDeliveredGauge atomic.Uint64
	JobsWebhookDeadGauge      atomic.Uint64
	// Job status gauges (scrape-time).
	JobsRunningGauge     atomic.Uint64
	JobsPendingGauge     atomic.Uint64
	JobsDispatchedGauge  atomic.Uint64
	JobsSucceededGauge   atomic.Uint64
	JobsFailedGauge      atomic.Uint64
	JobsCancelledGauge   atomic.Uint64
	JobsPurged           atomic.Uint64
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
	case "qiniu_download":
		STSQiniuDownload.Add(1)
	case "oci_iam":
		STSOCIIAM.Add(1)
	case "oci_par":
		STSOCIPAR.Add(1)
	case "oci_secret":
		STSOCISecret.Add(1)
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

// IncJobCompleted increments job complete counter.
func IncJobCompleted() { JobsCompleted.Add(1) }

// IncJobCancelled increments job cancel counter.
func IncJobCancelled() { JobsCancelled.Add(1) }

// IncRateLimited increments rate-limit counter.
func IncRateLimited() { RateLimited.Add(1) }

// IncSnapshot increments snapshot create counter.
func IncSnapshot() { Snapshots.Add(1) }

// IncMarketplaceInstall increments successful marketplace install.
func IncMarketplaceInstall() { MarketplaceInstalls.Add(1) }

// IncMarketplaceCheckout increments successful checkout.
func IncMarketplaceCheckout() { MarketplaceCheckouts.Add(1) }

// IncMarketplacePaid increments purchase marked paid (pay stub or Stripe webhook).
func IncMarketplacePaid() { MarketplacePaid.Add(1) }

// IncConnectorCreated increments connector registration.
func IncConnectorCreated() { ConnectorsCreated.Add(1) }

// IncMemoryPut increments successful memory put.
func IncMemoryPut() { MemoryPuts.Add(1) }

// IncMemorySearch increments successful vector memory search.
func IncMemorySearch() { MemorySearches.Add(1) }

// IncJobWithConnector increments job create when connector_id is set.
func IncJobWithConnector() { JobsWithConnector.Add(1) }

// IncJobCompletedWithConnector increments job complete when job had connector_id.
func IncJobCompletedWithConnector() { JobsCompletedConn.Add(1) }

// IncJobTimeout increments hard-timeout terminal fails.
func IncJobTimeout() { JobsTimeout.Add(1) }

// IncJobLeaseReclaim increments lease-expired release to pending.
func IncJobLeaseReclaim() { JobsLeaseReclaim.Add(1) }

// IncJobMaxAttempts increments fail after max_attempts on lease expiry.
func IncJobMaxAttempts() { JobsMaxAttempts.Add(1) }

// IncJobHeartbeat increments successful lease heartbeats.
func IncJobHeartbeat() { JobsHeartbeat.Add(1) }

// IncJobWebhook increments successful terminal job webhook deliveries.
func IncJobWebhook() { JobsWebhookOK.Add(1) }

// IncJobWebhookFail increments failed delivery attempts (will retry if under max).
func IncJobWebhookFail() { JobsWebhookFail.Add(1) }

// IncJobWebhookDead increments outbox rows moved to dead after max attempts.
func IncJobWebhookDead() { JobsWebhookDead.Add(1) }

// AddJobWebhookPurged adds deleted delivered/dead outbox rows.
func AddJobWebhookPurged(n uint64) {
	if n > 0 {
		JobsWebhookPurged.Add(n)
	}
}

// SetWebhookOutboxGauges sets current outbox queue depth gauges (pending/delivered/dead).
// Call from /metrics scrape path when store counts are available.
func SetWebhookOutboxGauges(pending, delivered, dead uint64) {
	JobsWebhookPendingGauge.Store(pending)
	JobsWebhookDeliveredGauge.Store(delivered)
	JobsWebhookDeadGauge.Store(dead)
}

// SetJobStatusGauges sets current BYOC job counts by status (global).
func SetJobStatusGauges(pending, running, succeeded, failed, cancelled uint64) {
	JobsPendingGauge.Store(pending)
	JobsRunningGauge.Store(running)
	JobsSucceededGauge.Store(succeeded)
	JobsFailedGauge.Store(failed)
	JobsCancelledGauge.Store(cancelled)
}

// SetJobDispatchedGauge sets current dispatched count.
func SetJobDispatchedGauge(n uint64) { JobsDispatchedGauge.Store(n) }

// AddJobsPurged adds deleted terminal jobs from TTL purge.
func AddJobsPurged(n uint64) {
	if n > 0 {
		JobsPurged.Add(n)
	}
}

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
	_, _ = fmt.Fprintf(w, "aicloudhub_sts_source_total{source=\"qiniu_download\"} %d\n", STSQiniuDownload.Load())
	_, _ = fmt.Fprintf(w, "aicloudhub_sts_source_total{source=\"oci_iam\"} %d\n", STSOCIIAM.Load())
	_, _ = fmt.Fprintf(w, "aicloudhub_sts_source_total{source=\"oci_par\"} %d\n", STSOCIPAR.Load())
	_, _ = fmt.Fprintf(w, "aicloudhub_sts_source_total{source=\"oci_secret\"} %d\n", STSOCISecret.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_created_total BYOC jobs created\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_created_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_created_total %d\n", JobsCreated.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_claimed_total BYOC jobs claimed\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_claimed_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_claimed_total %d\n", JobsClaimed.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_completed_total BYOC jobs completed\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_completed_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_completed_total %d\n", JobsCompleted.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_cancelled_total BYOC jobs cancelled\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_cancelled_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_cancelled_total %d\n", JobsCancelled.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_rate_limited_total Rate limited requests\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_rate_limited_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_rate_limited_total %d\n", RateLimited.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_snapshots_created_total Metadata snapshots created\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_snapshots_created_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_snapshots_created_total %d\n", Snapshots.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_marketplace_installs_total Marketplace installs succeeded\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_marketplace_installs_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_marketplace_installs_total %d\n", MarketplaceInstalls.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_marketplace_checkouts_total Marketplace checkouts created\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_marketplace_checkouts_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_marketplace_checkouts_total %d\n", MarketplaceCheckouts.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_marketplace_paid_total Marketplace purchases marked paid\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_marketplace_paid_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_marketplace_paid_total %d\n", MarketplacePaid.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_connectors_created_total Connector bindings registered\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_connectors_created_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_connectors_created_total %d\n", ConnectorsCreated.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_memory_puts_total Memory Kernel puts\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_memory_puts_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_memory_puts_total %d\n", MemoryPuts.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_memory_searches_total Memory vector searches\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_memory_searches_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_memory_searches_total %d\n", MemorySearches.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_with_connector_created_total Jobs created with connector_id\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_with_connector_created_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_with_connector_created_total %d\n", JobsWithConnector.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_completed_with_connector_total Jobs completed that had connector_id\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_completed_with_connector_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_completed_with_connector_total %d\n", JobsCompletedConn.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_timeout_total BYOC jobs failed by hard timeout\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_timeout_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_timeout_total %d\n", JobsTimeout.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_lease_reclaim_total Running jobs released to pending after lease expiry\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_lease_reclaim_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_lease_reclaim_total %d\n", JobsLeaseReclaim.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_max_attempts_total Jobs failed after max_attempts on lease expiry\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_max_attempts_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_max_attempts_total %d\n", JobsMaxAttempts.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_heartbeat_total Successful job lease heartbeats\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_heartbeat_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_heartbeat_total %d\n", JobsHeartbeat.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_webhook_ok_total Terminal job webhooks delivered (HTTP <300)\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_webhook_ok_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_webhook_ok_total %d\n", JobsWebhookOK.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_webhook_fail_total Terminal job webhook delivery attempts that failed\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_webhook_fail_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_webhook_fail_total %d\n", JobsWebhookFail.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_webhook_dead_total Terminal job webhooks moved to dead after max attempts\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_webhook_dead_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_webhook_dead_total %d\n", JobsWebhookDead.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_webhook_purged_total Terminal job webhook outbox rows deleted by TTL purge\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_webhook_purged_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_webhook_purged_total %d\n", JobsWebhookPurged.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_webhook_pending Job webhook outbox rows currently pending delivery\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_webhook_pending gauge\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_webhook_pending %d\n", JobsWebhookPendingGauge.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_webhook_delivered Job webhook outbox rows currently delivered (not yet purged)\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_webhook_delivered gauge\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_webhook_delivered %d\n", JobsWebhookDeliveredGauge.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_webhook_dead Job webhook outbox rows currently dead-lettered (not yet purged)\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_webhook_dead gauge\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_webhook_dead %d\n", JobsWebhookDeadGauge.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_pending Current BYOC jobs in pending status\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_pending gauge\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_pending %d\n", JobsPendingGauge.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_dispatched Current BYOC jobs in dispatched status\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_dispatched gauge\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_dispatched %d\n", JobsDispatchedGauge.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_running Current BYOC jobs in running status\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_running gauge\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_running %d\n", JobsRunningGauge.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_succeeded Current BYOC jobs in succeeded status\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_succeeded gauge\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_succeeded %d\n", JobsSucceededGauge.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_failed Current BYOC jobs in failed status\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_failed gauge\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_failed %d\n", JobsFailedGauge.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_cancelled Current BYOC jobs in cancelled status\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_cancelled gauge\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_cancelled %d\n", JobsCancelledGauge.Load())
	_, _ = fmt.Fprintf(w, "# HELP aicloudhub_jobs_purged_total Terminal jobs deleted by TTL purge\n")
	_, _ = fmt.Fprintf(w, "# TYPE aicloudhub_jobs_purged_total counter\n")
	_, _ = fmt.Fprintf(w, "aicloudhub_jobs_purged_total %d\n", JobsPurged.Load())
}
