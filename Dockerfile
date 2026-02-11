# Stage 1: Build
FROM golang:1.24-alpine AS builder
RUN apk add --no-network --no-cache gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO_ENABLED=1 is required for the SQLite driver
RUN CGO_ENABLED=1 GOOS=linux go build -o gopds ./cmd/gopds

# Stage 2: Run
FROM alpine:latest
RUN apk add --no-cache ca-certificates
WORKDIR /root/
# Copy binary from builder
COPY --from=builder /app/gopds .
# Create data directory for DB and covers
RUN mkdir -p ./data/covers
# Expose the port
EXPOSE 8880
CMD ["./gopds"]
