# AI-cloudhub — local build helpers (CGO free; pure Go)
export CGO_ENABLED := 0

BIN_DIR := .bin
BINS    := api hubd runner mcp

.PHONY: all build test help clean $(BINS) \
	smoke smoke-agent smoke-objects smoke-minio smoke-policy smoke-job smoke-mcp smoke-all

all: build

build: $(BINS)

api hubd runner mcp:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$@ ./cmd/$@

test:
	go test ./...

help:
	@echo "AI-cloudhub targets (CGO_ENABLED=0):"
	@echo "  make build          Build binaries into $(BIN_DIR)/ (api hubd runner mcp)"
	@echo "  make test           go test ./..."
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
	@echo "  make smoke-all      Run all smokes except smoke-minio (smoke + agent + objects + policy + job + mcp)"

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

# Full local smoke suite without live MinIO (use smoke-minio separately).
smoke-all: smoke smoke-agent smoke-objects smoke-policy smoke-job smoke-mcp

clean:
	rm -rf $(BIN_DIR)
