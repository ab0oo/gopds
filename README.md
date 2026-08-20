# GoPDS

Obligatory screenshot for the reddit trolls:
<img width="1387" height="1162" alt="image" src="https://github.com/user-attachments/assets/a669e9cd-6161-46a5-955a-a2ab68944b17" />

GoPDS is a lightweight OPDS server for EPUB libraries, with a web UI for live metadata and cover editing.

It builds to a single static binary (pure-Go SQLite, no CGO) and ships as a ~21MB container image.

## Current Capabilities

- OPDS catalog serving with large-library navigation:
  - Root OPDS navigation feed at `/opds`
  - Author-range browsing (`authors=a`, `authors=a-d`) with pagination
  - Category/subcategory browsing at `/opds/categories`
- Public book access:
  - OPDS feeds
  - JSON list (`/api/books`)
  - Book downloads (`/download/{id}`)
- Authenticated admin editing:
  - Live EPUB metadata edit/write
  - Open Library + Google Books compare/apply workflow
  - Cover candidate selection and apply, from inside the EPUB or from online sources
  - Optional write selected cover into EPUB (`write_to_epub`)
  - Rebuild/rescan controls
- Cover behavior:
  - Cache cover writes to `{data dir}/covers/{id}.jpg`
  - When writing to EPUB, also writes sibling `cover.jpg` next to the EPUB file
  - EPUB cover normalization prefers canonical `cover.jpg`
- Scanner modes:
  - Incremental rescan (changed/new books only, based on file mod time)
  - Full rebuild (drop DB cache + clear cover cache + full reindex)

## Configuration

All configuration is via environment variables.

| Variable | Default | Purpose |
| --- | --- | --- |
| `BOOK_PATH` | `./books` | Root of the EPUB library. |
| `DB_PATH` | `./data/gopds.db` | SQLite cache location. Parent directories are created automatically. |
| `ADMIN_USERNAME` | `admin` | Admin username. |
| `ADMIN_PASSWORD` | *(none)* | Required for authenticated features. If empty, all admin endpoints return 401. |
| `CATEGORY_SOURCE` | `none` | How categories are derived: `path`, `subject`, `auto`, or `none`. See below. |
| `ONLINE_COVER_MIN_WIDTH` | `300` | Online cover candidates narrower than this are discarded. |
| `ONLINE_COVER_MIN_HEIGHT` | `420` | Online cover candidates shorter than this are discarded. |
| `ENRICH_RATE_MS` | `1000` | Milliseconds between upstream lookups during background enrichment. |
| `LISTEN_ADDR` | `:8880` | Address the HTTP server binds to. |

The server listens on port `8880` by default (see `LISTEN_ADDR`).

### Category sources

`CATEGORY_SOURCE` selects how each book's category and subcategory are determined during a scan:

- `path` — derived from directory layout under `BOOK_PATH`:
  - category = first folder under `BOOK_PATH`
  - subcategory = second folder under `BOOK_PATH` (optional)
- `subject` — derived from the EPUB's `dc:subject` metadata. Hierarchical subjects
  (`Fiction > Science Fiction`, `Fiction/Science Fiction`) are split into category and
  subcategory; otherwise the first two subjects are used.
- `auto` — try `subject` first, fall back to `path` when the EPUB has no usable subjects.
- `none` — no categories. The "Browse by Category" entry is omitted from the OPDS root.

Two older boolean toggles are still honored for backward compatibility, but only when
`CATEGORY_SOURCE` is unset: `CATEGORY_FROM_SUBJECT` (checked first) and `CATEGORY_FROM_PATH`.
Both accept `1`, `true`, `yes`, or `on`. New deployments should use `CATEGORY_SOURCE`.

### Example `docker-compose.yaml`

```yaml
services:
  gopds:
    image: ghcr.io/ab0oo/gopds:latest
    container_name: gopds
    user: "1000:1000" # Must own the book library if you want metadata/cover writes
    ports:
      - "8880:8880"
    environment:
      - BOOK_PATH=/app/books
      - DB_PATH=/app/data/gopds.db
      - CATEGORY_SOURCE=subject
      - ADMIN_USERNAME=admin
      - ADMIN_PASSWORD=${GOPDS_ADMIN_PASSWORD:?set this in a .env file}
    volumes:
      - /path/to/books:/app/books
      - /path/to/gopds-data:/app/data
    restart: unless-stopped
```

Important:

- The data volume must be writable by the container user, and must **not** live on
  `tmpfs` (e.g. under `/var/run`), or the index and cover cache are lost on every reboot.
  A missing-but-creatable directory is fine; an unwritable one fails at startup with
  `unable to open database file`.
- If you want EPUB metadata/cover writes, the books volume must be writable too.
- If `ADMIN_PASSWORD` is empty, admin-protected editing features are unavailable.
- Keep real passwords out of committed compose files; use a `.env` file or secrets.

## OPDS Endpoints

- `GET /opds`
  - OPDS root navigation feed.
- `GET /opds?authors=a`
- `GET /opds?authors=a-d&page=1&limit=100`
  - Author-range acquisition feeds (paginated). `authors=other` covers non-alphabetic
    authors. `limit` defaults to 100 and is capped at 250.
- `GET /opds/authors`
  - Same behavior as `/opds`; accepts the same `authors` selector.
- `GET /opds/categories`
- `GET /opds/categories?category=Fiction`
- `GET /opds/categories?category=Fiction&subcategory=SciFi&page=1&limit=100`
  - Category/subcategory navigation + acquisition feeds.

`GET /` serves the OPDS catalog to OPDS clients and the HTML UI to browsers, based on the
`Accept` header. Append `?opds=1` to force the catalog.

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
- `GET /api/books/{id}/covers/online`
- `GET /api/books/{id}/covers/candidates/{key}`
- `PUT /api/books/{id}/cover`
- `POST /api/admin/rescan`
- `POST /api/admin/rebuild`
- `GET /api/admin/rebuild/status`
- `GET /api/admin/quality`
- `GET /api/admin/quality/queue`
- `POST /api/admin/covers/upgrade`
- `POST /api/admin/enrich`
- `POST /api/admin/enrich/stop`
- `GET /api/admin/enrich/status`
- `GET /api/admin/enrich/proposals`
- `GET /api/admin/review`

Sessions are held in memory with a 12-hour TTL and delivered as an `HttpOnly` cookie.
They do not survive a server restart.

## Metadata Quality

Every scan scores each book 0-100 on metadata completeness (description, cover
size, author form, category, identifier, series) and records the result in a
`book_enrichment` table kept separate from `books`, so a full rebuild never
discards human decisions.

Two cleanup layers build on that score:

**Deterministic normalization** runs on every scan with no network access. It
canonicalizes `Last, First` author names to `First Last`, repairs ALL-CAPS
names, and lifts trailing `(Series Name Book 3)` annotations out of titles into
proper series fields. Anything it cannot decide with certainty -- multi-author
strings, `writing as` pseudonyms, series text embedded in the author field -- is
flagged for review rather than rewritten on a guess.

**Background enrichment** (`POST /api/admin/enrich`) fills gaps from Open
Library and Google Books. It is deliberately conservative:

- **Dry run by default.** Pass `?apply=true` to write anything. Inspect the
  result with `GET /api/admin/enrich/proposals` first.
- **Gaps only.** A field that already has a local value is never overwritten;
  the EPUB is treated as more authoritative than a remote guess.
- **Only exact matches auto-apply.** A match needs an agreeing ISBN or a very
  strong title *and* author agreement. Weaker matches are queued for review.
- **Database only.** The automated pass does not modify EPUB files.
- **Locked books are skipped**, so manual edits are never clobbered.
- Rate-limited to one upstream request per second (`ENRICH_RATE_MS` to adjust).

**Cover selection** picks the best artwork an EPUB actually contains, judged on
shape and resolution rather than filename order. Book covers are portrait and
roughly 2:3, so landscape and square images are rejected outright -- this is what
stops a large publisher logo from being chosen over the real cover. A sibling
`cover.jpg` next to the EPUB is used only when it beats the embedded artwork,
since those files are often one shared image copied across an author's whole
directory tree.

**Cover upgrade** (`POST /api/admin/covers/upgrade`) looks for better artwork
online for books whose covers are missing or below bookstore quality. It is
dry-run by default like enrichment, only replaces a cover when the replacement
is clearly better, and writes to the cover cache rather than to EPUB files.

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

## Utilities

`nester.go` is a standalone helper that reorganizes a flat directory of EPUBs into
`Title/Title.epub` subfolders, using each book's own metadata to name them (handy for
libraries full of hash-named files):

```bash
go run nester.go "/path/to/Some Author"
```

It is not part of the server build. Delete any stale shared `cover.jpg` in the directory
before rescanning.

## CI/CD

GitHub Actions workflow:

- `.github/workflows/docker-build.yml`
- Builds the Docker image on push to `main`, tags, releases, PRs, and manual dispatch
- Publishes to GHCR on `v*` tags and published releases:
  - `ghcr.io/<owner>/<repo>:latest` (default branch)
  - ref/tag/sha tags
- Uploads a compressed Docker image tarball artifact for each run

## Security Recommendations

- Use a strong `ADMIN_PASSWORD`, supplied via environment or secrets rather than a
  committed file.
- Run behind an HTTPS reverse proxy for internet exposure. The session cookie is only
  marked `Secure` when the server itself terminates TLS.
- Note that `/api/books` and `/download/{id}` are unauthenticated: anyone who can reach
  the server can enumerate and download the library.
- Outbound cover fetches are restricted to an allowlist of Open Library, Google Books,
  and Wikimedia hosts.
- Protect `main` with required signed commits.
- Keep GHCR package visibility intentional (public/private).

## License

This project is licensed under the MIT License. See `LICENSE`.
