# Must include "AS builder" right here
FROM golang:1.24-bookworm AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o gopds ./cmd/gopds

# --- Second Stage ---
FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app

# This "builder" matches the name in the first line
COPY --from=builder /app/gopds .

ENV BOOK_PATH=/app/books
ENV DB_PATH=/app/data/gopds.db
EXPOSE 8880

CMD ["./gopds"]
