# GoPDS

GoPDS is a lightweight OPDS server for EPUB libraries, with a web UI for live metadata and cover editing.

## Current Capabilities

- OPDS catalog serving with large-library navigation:
  - Root OPDS navigation feed at `/opds`
  - Author-range browsing (`authors=a`, `authors=a-d`) with pagination
  - Category/subcategory browsing at `/opds/categories` (optional path-derived indexing)
- Public book access:
  - OPDS feeds
  - JSON list (`/api/books`)
  - Book downloads (`/download/{id}`)
- Authenticated admin editing:
  - Live EPUB metadata edit/write
  - Open Library + Google Books compare/apply workflow
  - Cover candidate selection and apply
  - Optional write selected cover into EPUB (`write_to_epub`)
  - Rebuild/rescan controls
- Cover behavior:
  - Cache cover writes to `data/covers/{id}.jpg`
  - When writing to EPUB, also writes sibling `cover.jpg` next to the EPUB file
  - EPUB cover normalization prefers canonical `cover.jpg`
- Scanner modes:
  - Incremental rescan (changed/new books only)
  - Full rebuild (drop DB cache + clear cover cache + full reindex)

## Configuration

Environment variables:

- `BOOK_PATH` (default `./books`): Root of EPUB library.
- `DB_PATH` (currently initialized in app as `./data/gopds.db`): SQLite cache location.
- `ADMIN_USERNAME` (default `admin`): Admin username.
- `ADMIN_PASSWORD` (required for authenticated features): Admin password.
- `CATEGORY_FROM_PATH` (default disabled): If `true/1/yes/on`, category/subcategory are inferred from directory layout:
  - category = first folder under `BOOK_PATH`
  - subcategory = second folder under `BOOK_PATH` (optional)

Example `docker-compose.yaml`:

```yaml
services:
  gopds:
    image: ghcr.io/ab0oo/gopds:latest
    container_name: gopds
    ports:
      - "8880:8880"
    environment:
      - BOOK_PATH=/app/books
      - DB_PATH=/app/data/gopds.db
      - CATEGORY_FROM_PATH=true
      - ADMIN_USERNAME=admin
      - ADMIN_PASSWORD=change-this-password
    volumes:
      - /path/to/books:/app/books
      - /path/to/gopds-data:/app/data
    restart: unless-stopped
```

Important:

- If you want EPUB metadata/cover writes, the books volume must be writable.
- If `ADMIN_PASSWORD` is empty, admin-protected editing features are unavailable.

## OPDS Endpoints

- `GET /opds`
  - OPDS root navigation feed.
- `GET /opds?authors=a`
- `GET /opds?authors=a-d&page=1&limit=100`
  - Author-range acquisition feeds (paginated).
- `GET /opds/categories`
- `GET /opds/categories?category=Fiction`
- `GET /opds/categories?category=Fiction&subcategory=SciFi&page=1&limit=100`
  - Category/subcategory navigation + acquisition feeds.

## Public vs Authenticated API

Public:

- `GET /opds`
- `GET /opds/authors`
- `GET /opds/categories`
- `GET /api/books`
- `GET /covers/{id}.jpg`
- `GET /download/{id}`
- `GET /api/openlibrary/search`

Auth/session:

- `GET /api/auth/status`
- `POST /api/auth/login`
- `POST /api/auth/logout`

Admin-protected:

- `GET /api/books/{id}/metadata/live`
- `PUT /api/books/{id}/metadata`
- `GET /api/books/{id}/covers/candidates`
- `GET /api/books/{id}/covers/candidates/{key}`
- `PUT /api/books/{id}/cover`
- `POST /api/admin/rescan`
- `POST /api/admin/rebuild`
- `GET /api/admin/rebuild/status`

## UI Notes

- Browser UI is at `/`.
- Admin login is required to see and use:
  - `Edit Metadata`
  - `Change Cover`
  - `Rescan/Rebuild` controls
- OPDS clients can use `/opds` (or root with OPDS accept headers).

## Build and Run Locally

```bash
go mod tidy
go build -o gopds ./cmd/gopds
./gopds
```

Then open:

- Web UI: `http://localhost:8880/`
- OPDS: `http://localhost:8880/opds`

## CI/CD

GitHub Actions workflow:

- `.github/workflows/docker-build.yml`
- Builds Docker image on push/PR/manual
- Publishes to GHCR on non-PR events:
  - `ghcr.io/<owner>/<repo>:latest` (default branch)
  - ref/tag/sha tags
- Uploads a compressed Docker image tarball artifact for each run

## Security Recommendations

- Use a strong `ADMIN_PASSWORD`.
- Run behind HTTPS reverse proxy for internet exposure.
- Protect `main` with required signed commits.
- Keep GHCR package visibility intentional (public/private).
