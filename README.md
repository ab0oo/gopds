# GoPDS 📚

A high-performance, lightweight OPDS catalog server written in Go. Designed specifically for NAS environments and large libraries (2,000+ books).

## Features

* **Blistering Fast:** Scans 2,500+ books in under a minute.
* **Automatic Metadata:** Extracts titles, authors, and descriptions directly from EPUB files.
* **Aggressive Cover Extraction:** Multi-strategy logic to find and serve book covers.
* **Docker Ready:** Tiny footprint (~20MB container) with graceful shutdown support.
* **Standard Compliant:** Works with Moon+ Reader, KyBook, Aldiko, and more.

## Quick Start (Docker Compose)

The easiest way to run GoPDS is with Docker.

```yaml
services:
  gopds:
    build: .
    container_name: gopds
    ports:
      - "8880:8880"
    volumes:
      - /path/to/your/books:/root/books:ro
      - ./data:/root/data
    environment:
      - BOOK_PATH=/root/books
    restart: unless-stopped
```

Setup & Configuration

    Clone the repo: git clone https://github.com/ab0oo/gopds.git

    Build the container: docker compose up -d --build

    Connect your Reader:

        Open your favorite OPDS app.

        Add a new catalog URL: http://[NAS-IP]:8880/opds

Development
Prerequisites

    Go 1.24+

    SQLite3 (CGO enabled)

Local Build
Bash

go mod tidy
go build -o gopds ./cmd/gopds
./gopds

Project Structure

    cmd/gopds: Entry point and server initialization.

    internal/scanner: EPUB parsing and cover extraction logic.

    internal/database: SQLite persistence layer.

    internal/web: Chi-based router and OPDS XML generation.

Built with ❤️ by ab0oo
