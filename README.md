# GoPDS 📚

A high-performance, lightweight OPDS catalog server written in Go. Designed specifically for NAS environments and large libraries (2,000+ books).

## Features

* **Blistering Fast:** Scans 2,500+ books in under a minute.
* **Automatic Metadata:** Extracts titles, authors, and descriptions directly from EPUB files.
* **Live EPUB Metadata Editor:** Reads metadata directly from each EPUB on demand and writes changes back into the file.
* **Open Library Compare + Apply:** Fetches matching Open Library metadata and lets you apply values field-by-field.
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
      # Use :rw if you want metadata edits to be written back into EPUB files
      - /path/to/your/books:/root/books:rw
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

Web Metadata Editing

    Open http://[NAS-IP]:8880 in a browser.

    Click Edit Metadata on any book.

    The modal loads live metadata directly from the EPUB file (not from DB cache).

    Click Fetch Open Library to load comparison metadata.

    Apply fields one-by-one using the Use button per field.

    Click Save All Fields to EPUB to write updates back into the file.

Important:

    Metadata editing requires write permission to the EPUB files and containing directory.

    If the server cannot write, the API returns a permission error and the file is not changed.

API Endpoints

    GET /opds
        OPDS catalog feed.

    GET /api/books
        Lightweight JSON book list from SQLite cache (id/path/title/author/description/mod_time).

    GET /api/books/{id}/metadata/live
        Reads metadata directly from the EPUB at request time.
        Fields:
        title, author, language, identifier, publisher, date, description, subjects, series, series_index

    PUT /api/books/{id}/metadata
        Writes metadata fields into the EPUB and syncs title/author/description back to DB cache.
        Request body fields:
        title, author, language, identifier, publisher, date, description, subjects[], series, series_index

    GET /api/openlibrary/search?q=<query>
        Fetches normalized metadata from Open Library with comparable fields for side-by-side review.

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
