package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/s3store"
)

// HealthResult is the outcome of a provider connectivity probe.
type HealthResult struct {
	OK          bool   `json:"ok"`
	ProviderID  string `json:"provider_id"`
	Type        Type   `json:"type"`
	Endpoint    string `json:"endpoint"`
	LatencyMS   int64  `json:"latency_ms"`
	BucketCount *int   `json:"bucket_count,omitempty"`
	Message     string `json:"message,omitempty"`
}

// DefaultHealthTimeout is the max time spent on an outbound probe.
const DefaultHealthTimeout = 8 * time.Second

// HealthProbe verifies the provider credentials can reach the object store.
// Uses ListBuckets (no mutations). Returns ok=false with message on soft failure
// rather than only Go errors for network/auth failures.
func (s *Service) HealthProbe(ctx context.Context, userID, id string) (*HealthResult, error) {
	resolved, rec, err := s.ResolveRecord(userID, id)
	if err != nil {
		return nil, err
	}
	out := &HealthResult{
		ProviderID: rec.ID,
		Type:       rec.Type,
		Endpoint:   resolved.Endpoint,
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultHealthTimeout)
		defer cancel()
	}

	start := time.Now()
	store, err := s3store.New(resolved.Endpoint, resolved.AccessKey, resolved.SecretKey, resolved.Region, resolved.UseSSL)
	if err != nil {
		out.OK = false
		out.LatencyMS = time.Since(start).Milliseconds()
		out.Message = fmt.Sprintf("client: %v", err)
		return out, nil
	}
	n, err := store.Probe(ctx)
	out.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		out.OK = false
		out.Message = err.Error()
		return out, nil
	}
	out.OK = true
	out.BucketCount = &n
	out.Message = "list_buckets ok"
	return out, nil
}
