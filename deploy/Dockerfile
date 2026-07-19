# Default image: API control plane only (compose uses this).
# All binaries (api + hubd + runner + mcp): see deploy/Dockerfile.all
# Runtime notes:
#   - hubd: device-side mount agent (needs rclone on host); usually not containerized with api
#   - runner: BYOC worker/agent entry; run on user compute, not platform pool
#   - mcp: stdio MCP-compatible helper; typically local/agent host, not this image
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/api ./cmd/api

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/api /usr/local/bin/api
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]
