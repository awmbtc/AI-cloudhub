package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/auth"
	"github.com/awmbtc/AI-cloudhub/internal/config"
	"github.com/awmbtc/AI-cloudhub/internal/crypto/secretbox"
	"github.com/awmbtc/AI-cloudhub/internal/device"
	"github.com/awmbtc/AI-cloudhub/internal/drive"
	"github.com/awmbtc/AI-cloudhub/internal/httpserver"
	"github.com/awmbtc/AI-cloudhub/internal/job"
	"github.com/awmbtc/AI-cloudhub/internal/policy"
	"github.com/awmbtc/AI-cloudhub/internal/provider"
	"github.com/awmbtc/AI-cloudhub/internal/s3store"
	"github.com/awmbtc/AI-cloudhub/internal/store"
	"github.com/awmbtc/AI-cloudhub/internal/sts"
	"github.com/awmbtc/AI-cloudhub/internal/workspace"
)

func main() {
	cfg := config.Load()
	if vr := cfg.Validate(); !vr.OK() {
		for _, e := range vr.Errors {
			log.Printf("config error: %s", e)
		}
		log.Fatalf("config validation failed (set AI_CLOUDHUB_STRICT=0 and fix secrets, or use strong JWT_SECRET / AI_CLOUDHUB_MASTER_KEY)")
	} else {
		for _, w := range vr.Warnings {
			log.Printf("config warning: %s", w)
		}
	}

	st, err := openStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	authSvc := auth.New(cfg.JWTSecret, st)
	authSvc.SetTokenTTL(cfg.TokenTTL)

	box, err := secretbox.FromEnv()
	if err != nil {
		log.Fatalf("master key: %v", err)
	}
	var provSvc *provider.Service
	if box != nil {
		provSvc = provider.NewServiceWithBox(st, box)
		log.Printf("provider secret envelope encryption enabled (AI_CLOUDHUB_MASTER_KEY)")
	} else {
		provider.LogDevModeWarning()
		provSvc = provider.NewService(st)
	}
	driveSvc := drive.NewService(provSvc, st)
	deviceSvc := device.NewService(st)
	jobSvc := job.NewService(st)

	apiBase := os.Getenv("AI_CLOUDHUB_PUBLIC_URL")
	if apiBase == "" {
		apiBase = "http://127.0.0.1" + cfg.HTTPAddr
	}
	stsTTL := 30 * time.Minute
	stsSvc := sts.New(stsTTL, apiBase)
	driveSvc.SetSTS(stsSvc)

	var wsSvc *workspace.Service
	if os.Getenv("ENABLE_PLATFORM_S3") == "1" {
		s3, err := s3store.New(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Region, cfg.S3UseSSL)
		if err != nil {
			log.Fatalf("platform s3: %v", err)
		}
		wsSvc = workspace.NewService(s3, cfg.BucketPrefix, cfg.PresignTTL)
		log.Printf("platform S3 workspace enabled (endpoint=%s)", cfg.S3Endpoint)
	}

	lim, closer := openLimiter()
	if closer != nil {
		defer closer()
	}

	handler := httpserver.New(httpserver.Deps{
		Config:    cfg,
		Auth:      authSvc,
		Workspace: wsSvc,
		Providers: provSvc,
		Drives:    driveSvc,
		Devices:   deviceSvc,
		Jobs:      jobSvc,
		Limiter:   lim,
		Store:     st,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("AI-cloudhub api on %s (store=%s; register=%v; token_ttl=%s; BYOC runner; D-001 no platform pool)",
			cfg.HTTPAddr, storeLabel(cfg.DBPath), cfg.AllowRegister, cfg.TokenTTL)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	sig := <-stop
	log.Printf("shutdown signal: %v", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("graceful shutdown: %v", err)
	}
	log.Printf("server stopped")
}

func openStore(path string) (store.Store, error) {
	if path == "" {
		path = filepath.Join(".", "data", "ai-cloudhub.db")
	}
	if path == "memory" {
		log.Printf("store: in-memory (AI_CLOUDHUB_DB=memory)")
		return store.NewMemory(), nil
	}
	if store.IsPostgresDSN(path) {
		log.Printf("store: postgres")
		return store.OpenPostgres(path)
	}
	log.Printf("store: sqlite %s", path)
	return store.Open(path)
}

func storeLabel(path string) string {
	if path == "memory" {
		return "memory"
	}
	if store.IsPostgresDSN(path) {
		return "postgres"
	}
	return "sqlite"
}

func openLimiter() (policy.RateLimiter, func()) {
	redisURL := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_REDIS"))
	if redisURL == "" {
		log.Printf("ratelimit: in-process token bucket")
		return policy.NewLimiter(20, 40), nil
	}
	limit, window := policy.ParseRedisLimit(
		os.Getenv("AI_CLOUDHUB_REDIS_LIMIT"),
		os.Getenv("AI_CLOUDHUB_REDIS_WINDOW_SEC"),
	)
	rl, err := policy.NewRedisLimiter(redisURL, limit, window)
	if err != nil {
		log.Printf("ratelimit: redis failed (%v), falling back to in-process", err)
		return policy.NewLimiter(20, 40), nil
	}
	log.Printf("ratelimit: redis shared limit=%d window=%s", limit, window)
	return rl, func() { _ = rl.Close() }
}
