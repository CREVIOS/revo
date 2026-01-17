# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install git for go mod download
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o techy-bot ./cmd/techy

# Runtime stage
FROM alpine:latest

# Install ca-certificates for HTTPS and git for potential future use
RUN apk --no-cache add ca-certificates git tzdata

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/techy-bot .

# Create non-root user
RUN adduser -D -g '' techybot
USER techybot

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Run the binary
CMD ["./techy-bot"]
