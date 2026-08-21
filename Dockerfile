# ==============================================================================
# Build Stage: Multi-stage Pure-Go CGO-free builder
# ==============================================================================
FROM golang:1.27-alpine AS builder

WORKDIR /app

# Cache module dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code and build statically linked executable
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/aegis-server ./cmd/server

# ==============================================================================
# Runtime Stage: Minimal lightweight production container
# ==============================================================================
FROM alpine:latest

# Install standard CA certificates for outbound HTTPS (LLM APIs) and tzdata
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/aegis-server /app/aegis-server

# Configuration and persistence
EXPOSE 8085
VOLUME /app/data

ENV AEGIS_PORT=8085 \
    AEGIS_DB_PATH=/app/data/aegis.db \
    AEGIS_SCRATCH_DIR=/app/scratch

ENTRYPOINT ["/app/aegis-server"]
