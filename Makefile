# AI-cloudhub — local build helpers (CGO free; pure Go)
export CGO_ENABLED := 0

BIN_DIR := .bin
BINS    := api hubd runner mcp

.PHONY: all build test help clean docker-api docker-all release-binaries \
	api hubd runner mcp \
	smoke smoke-agent smoke-objects smoke-minio smoke-policy smoke-job smoke-mcp \
	smoke-quickstart-agent smoke-golden smoke-golden-minio smoke-stage-c smoke-byoc smoke-sts prod-preflight smoke-prod-preflight smoke-all
all: build

build: $(BINS)

api hubd runner mcp:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$@ ./cmd/$@

test:
	go test ./...

docker-api:
	docker build -f deploy/Dockerfile -t ai-cloudhub:api .

docker-all:
	docker build -f deploy/Dockerfile.all -t ai-cloudhub:all .

# Cross-compile release tarballs into dist/ (see scripts/release-build.sh).
# VERSION defaults to git describe; override: make release-binaries VERSION=0.2.0
release-binaries:
	@chmod +x scripts/release-build.sh
	VERSION="$(VERSION)" ./scripts/release-build.sh $(VERSION)

help:
	@echo "AI-cloudhub targets (CGO_ENABLED=0):"
	@echo "  make build          Build binaries into $(BIN_DIR)/ (api hubd runner mcp)"
	@echo "  make test           go test ./..."
	@echo "  make docker-api     Multi-stage distroless API image (deploy/Dockerfile)"
	@echo "  make docker-all     Multi-binary alpine image (deploy/Dockerfile.all)"
	@echo "  make release-binaries  Multi-arch archives -> dist/ (+ checksums.txt)"
	@echo "  make clean          Remove $(BIN_DIR)/"
	@echo ""
	@echo "Smoke targets (need local services as documented per script):"
	@echo "  make smoke          P0 smoke (scripts/smoke-p0.sh)"
	@echo "  make smoke-agent    Agent smoke (scripts/smoke-agent.sh)"
	@echo "  make smoke-objects  Objects/drive smoke (scripts/smoke-objects.sh)"
	@echo "  make smoke-minio    Live MinIO inventory/snapshot (optional; skips if MinIO unavailable)"
	@echo "  make smoke-policy   Policy smoke (scripts/smoke-policy.sh)"
	@echo "  make smoke-job      Job smoke (scripts/smoke-job.sh)"
	@echo "  make smoke-mcp      MCP jobs smoke (scripts/smoke-mcp-jobs.sh)"
	@echo "  make smoke-quickstart-agent  QUICKSTART-AGENT.md end-to-end"
	@echo "  make smoke-golden  Product golden path (D-003 / docs/GOLDEN-PATH.md)"
	@echo "  make smoke-golden-minio  Live MinIO golden path (optional; soft-skip if MinIO unavailable)"
	@echo "  make smoke-stage-c  Stage C memory/marketplace/connectors"
	@echo "  make smoke-byoc     BYOC git/pg/mysql materialize (local)"
	@echo "  make smoke-sts      Offline STS path selection + unit tests"
	@echo "  make prod-preflight Production env checklist (scripts/prod-preflight.sh)"
	@echo "  make smoke-prod-preflight  prod-preflight self-test (weak fail / strong pass)"
	@echo "  make smoke-all      Run all smokes except live MinIO targets"
	@echo "  (CI: test + smoke-all + smoke-minio + docker image on main push/PR)"

smoke: build
	./scripts/smoke-p0.sh

smoke-agent: build
	./scripts/smoke-agent.sh

smoke-objects: build
	./scripts/smoke-objects.sh

# Live MinIO hard-assert: inventory + snapshot include_objects (auto-starts MinIO if needed).
# Skips with exit 0 if MinIO cannot start; set AI_CLOUDHUB_SMOKE_MINIO_REQUIRE=1 to fail hard.
smoke-minio: build
	./scripts/smoke-minio-inventory.sh

smoke-policy: build
	./scripts/smoke-policy.sh

smoke-job: build
	./scripts/smoke-job.sh

smoke-mcp: build
	./scripts/smoke-mcp-jobs.sh

smoke-quickstart-agent: build
	./scripts/smoke-quickstart-agent.sh

smoke-golden: build
	./scripts/smoke-golden.sh

# Live MinIO golden path: session + objects inventory + BYOC job (auto-starts MinIO if needed).
# Soft-skips with exit 0 if MinIO cannot start; set AI_CLOUDHUB_SMOKE_MINIO_REQUIRE=1 to fail hard.
# Not part of smoke-all (optional, like smoke-minio).
smoke-golden-minio: build
	@chmod +x scripts/smoke-golden-minio.sh
	./scripts/smoke-golden-minio.sh

smoke-stage-c: build
	./scripts/smoke-stage-c.sh

smoke-byoc: build
	@chmod +x scripts/smoke-byoc-connectors.sh
	./scripts/smoke-byoc-connectors.sh

smoke-sts: build
	@chmod +x scripts/smoke-sts.sh
	./scripts/smoke-sts.sh

prod-preflight:
	@chmod +x scripts/prod-preflight.sh
	./scripts/prod-preflight.sh

smoke-prod-preflight:
	@chmod +x scripts/smoke-prod-preflight.sh scripts/prod-preflight.sh
	./scripts/smoke-prod-preflight.sh

# Full local smoke suite without live MinIO (use smoke-minio / smoke-golden-minio separately).
smoke-all: smoke smoke-agent smoke-objects smoke-policy smoke-job smoke-mcp smoke-quickstart-agent smoke-golden smoke-stage-c smoke-byoc smoke-sts smoke-prod-preflight

clean:
	rm -rf $(BIN_DIR)
