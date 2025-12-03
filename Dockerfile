# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /zfs-provisioner ./cmd/provisioner

# Runtime stage
FROM alpine:3.20

# Install ZFS userspace tools and coreutils for GNU stat
RUN apk add --no-cache zfs coreutils

# Copy binary
COPY --from=builder /zfs-provisioner /usr/local/bin/zfs-provisioner

ENTRYPOINT ["/usr/local/bin/zfs-provisioner"]
