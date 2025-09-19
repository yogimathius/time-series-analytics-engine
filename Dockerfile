# Multi-stage build for Go time-series analytics engine
FROM golang:1.22-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -o time-series-engine .

# Production stage
FROM scratch

# Copy CA certificates and timezone data
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy the binary
COPY --from=builder /app/time-series-engine /time-series-engine

# Copy configuration
COPY --from=builder /app/config.json /config.json

# Create directories for data storage
COPY --from=builder /app/data /data

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/time-series-engine", "-health-check"]

# Expose port
EXPOSE 8080

# Run the application
ENTRYPOINT ["/time-series-engine"]