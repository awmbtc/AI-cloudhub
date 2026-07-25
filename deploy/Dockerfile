# Default image: API control plane only (compose uses this).
# All binaries: deploy/Dockerfile.all
#
# Build:
#   docker build -f deploy/Dockerfile -t ai-cloudhub:api .
# Runtime notes:
#   - hubd needs rclone/FUSE on the host — not this image
#   - runner is BYOC on user compute (D-001: no platform pool)
#   - mcp is stdio helper for agent hosts
ARG GO_VERSION=1.22
FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=0.2.0
# Override: docker build --build-arg VERSION=0.2.1
RUN CGO_ENABLED=0 GOOS=linux go build \
	-trimpath \
	-ldflags="-s -w -X github.com/awmbtc/AI-cloudhub/internal/version.Version=${VERSION}" \
	-o /out/api ./cmd/api

# Minimal runtime: ca-certs + static binary (no shell).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
