# AI-cloudhub — local build helpers (CGO free; pure Go)
export CGO_ENABLED := 0

BIN_DIR := .bin
BINS    := api hubd runner mcp

.PHONY: all build test smoke smoke-agent smoke-objects smoke-minio smoke-policy clean $(BINS)

all: build

build: $(BINS)

api hubd runner mcp:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$@ ./cmd/$@

test:
	go test ./...

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

smoke-all: smoke smoke-agent smoke-objects smoke-policy

clean:
	rm -rf $(BIN_DIR)
