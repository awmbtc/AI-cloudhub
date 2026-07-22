# AI-cloudhub — local build helpers (CGO free; pure Go)
export CGO_ENABLED := 0

BIN_DIR := .bin
BINS    := api hubd runner mcp

.PHONY: all build test smoke smoke-agent smoke-objects clean $(BINS)

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

smoke-all: smoke smoke-agent smoke-objects

clean:
	rm -rf $(BIN_DIR)
