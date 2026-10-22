# GoPDS

Obligatory screenshot for the reddit trolls:
<img width="1387" height="1162" alt="image" src="https://github.com/user-attachments/assets/a669e9cd-6161-46a5-955a-a2ab68944b17" />

GoPDS is a lightweight OPDS server for EPUB libraries, with a web UI for live metadata and cover editing.

It builds to a single static binary (pure-Go SQLite, no CGO) and ships as a ~21MB container image.

## Current Capabilities

- OPDS catalog serving with large-library navigation:
  - Root OPDS navigation feed at `/opds`
  - Author-range browsing (`authors=a`, `authors=a-d`) with pagination
  - Series browsing at `/opds/series`
  - Category/subcategory browsing at `/opds/categories`
- Public book access:
  - OPDS feeds
  - JSON list (`/api/books`)
  - Book downloads (`/download/{id}`), named `Author - Title.epub` from the book's
    metadata rather than the server's filename
  - Email Book: send a book to a Kindle's Send to Kindle address, when SMTP is configured
- Authenticated admin editing:
  - Live EPUB metadata edit/write
  - Open Library + Google Books compare/apply workflow
  - Series tagging: look one book up on Wikidata, then write the series name and
    each volume's index to every matching book in the library after review
  - Series cleanup: from one book, fix every book of its series at once --
    titles lose `Xanth 03 - ` style prefixes and take Wikidata's spelling,
    authors are checked against Wikidata, and series name and number are written
    into each EPUB. Changes are previewed per book and written only for the books
    you check; co-authors and other contributors are left alone
  - Cover candidate selection and apply, from inside the EPUB or from online sources
  - Optional write selected cover into EPUB (`write_to_epub`)
  - Rebuild/rescan controls
- Cover behavior:
  - Cache cover writes to `data/covers/{id}.jpg` under the working directory
    (`/app/data/covers` in the container), regardless of `DB_PATH`
  - When writing to EPUB, also writes sibling `cover.jpg` next to the EPUB file
  - EPUB cover normalization prefers canonical `cover.jpg`
- Scanner modes:
  - Incremental rescan (changed/new books only, based on file mod time)
  - Full rebuild (re-read every book + clear cover cache + remove books whose files
    are gone). Book IDs are kept, so review decisions, locks and download links
    survive. A rebuild whose scan finds no books at all removes nothing, so an
    unmounted library cannot wipe the index
  - Each book also records its path relative to `BOOK_PATH`. If the library is
    mounted somewhere else, or `BOOK_PATH` changes, books are matched back to their
    existing entries by that relative path and keep their IDs. Files moved to a
    different folder *within* the library are treated as new books

## Configuration

All configuration is via environment variables.

| Variable | Default | Purpose |
| --- | --- | --- |
| `BOOK_PATH` | `./books` | Root of the EPUB library. |
| `DB_PATH` | `./data/gopds.db` | SQLite cache location. Parent directories are created automatically. |
| `ADMIN_USERNAME` | `admin` | Admin username. |
| `ADMIN_PASSWORD` | *(none)* | Required for authenticated features. If empty, all admin endpoints return 401. |
| `CATEGORY_SOURCE` | `none` | How categories are derived: `path`, `subject`, `auto`, or `none`. See below. |
| `ONLINE_COVER_MIN_WIDTH` | `300` | Online cover candidates narrower than this are discarded. Wikipedia covers use a floor of 200, since fair-use images there are small. |
| `ONLINE_COVER_MIN_HEIGHT` | `420` | Online cover candidates shorter than this are discarded. Wikipedia covers use a floor of 300. |
| `ENRICH_RATE_MS` | `1000` | Milliseconds between upstream lookups during background enrichment. |
| `GOOGLE_BOOKS_API_KEY` | *(none)* | Optional Google Books API key. Without one, lookups share Google's small anonymous quota. |
| `SMTP_SERVER` | *(none)* | SMTP server for Email Book. Email Book appears only when `SMTP_SERVER`, `SMTP_PORT`, `SMTP_USER` and `SMTP_PASS` are all set. |
| `SMTP_PORT` | *(none)* | SMTP port. `465` uses implicit TLS; other ports use STARTTLS when offered. |
| `SMTP_USER` | *(none)* | SMTP login. |
| `SMTP_PASS` | *(none)* | SMTP password. Never sent over an unencrypted connection, except to a server on localhost. |
| `SMTP_FROM` | `SMTP_USER` | Sender address, for providers whose login is not an email address. |
| `EMAIL_ALLOWED_DOMAINS` | `kindle.com,free.kindle.com,kindle.cn` | Recipient domains Email Book accepts, comma-separated. `*` allows any address. |
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
      - GOOGLE_BOOKS_API_KEY=${GOOGLE_BOOKS_API_KEY:-} # optional
      - SMTP_SERVER=${SMTP_SERVER:-}                     # optional, enables Email Book
      - SMTP_PORT=${SMTP_PORT:-587}
      - SMTP_USER=${SMTP_USER:-}
      - SMTP_PASS=${SMTP_PASS:-}
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
- Mount the data volume at `/app/data`. The cover cache always lives in `data/covers`
  under the working directory (`/app`), even if `DB_PATH` points elsewhere.
- If you want EPUB metadata/cover writes, the books volume must be writable too.
- If `ADMIN_PASSWORD` is empty, admin-protected editing features are unavailable.

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
- `GET /opds/series`
- `GET /opds/series?series=Xanth&page=1&limit=100`
  - Series navigation feed, and each series' books in series order.

`GET /` serves the OPDS catalog to OPDS clients and the HTML UI to browsers, based on the
`Accept` header. Append `?opds=1` to force the catalog.

## Public vs Authenticated API

Public:

- `GET /opds`
- `GET /opds/authors`
- `GET /opds/categories`
- `GET /opds/series`
- `GET /api/books`
- `GET /covers/{id}.jpg`
- `GET /download/{id}`
- `GET /api/email/status`
- `POST /api/books/{id}/email`
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
- `GET /api/books/{id}/series/lookup`
- `POST /api/admin/series/apply`
- `GET /api/books/{id}/series/cleanup`
- `POST /api/admin/series/cleanup/apply`
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
- `POST /api/admin/review/{id}/resolve`
- `PUT /api/admin/review/{id}/lock`
- `POST /api/admin/review/{id}/rescore`

Sessions are held in memory with a 12-hour TTL and delivered as an `HttpOnly` cookie.
They do not survive a server restart.

## Metadata Quality

Every scan scores each book 0-100 on metadata completeness (description, cover
size, author form, category, identifier, series) and records the result in a
`book_enrichment` table kept separate from `books` and keyed by book ID, which
stays stable across rescans, rebuilds and library moves.

Two cleanup layers build on that score:

**Deterministic normalization** runs on every scan with no network access. It
canonicalizes `Last, First` author names to `First Last`, repairs ALL-CAPS
names, and lifts series annotations out of titles into proper series fields,
both trailing (`Castle Roogna (Xanth #3)`) and leading (`Xanth 03 - Castle
Roogna`). When an EPUB lists several creators, the first author is used, not
illustrators or translators. Anything it cannot decide with certainty -- multi-author
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
- Rate-limited to one book per second (`ENRICH_RATE_MS` to adjust).
- **Backs off when Google Books runs out of quota.** After a quota response,
  Google Books is skipped until the quota resets (midnight Pacific for the daily
  limit) and lookups continue with Open Library alone. The search and enrichment
  status responses carry a `notice` while this is in effect.

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

`GET /api/admin/quality` reports library-wide health; `GET /api/admin/quality/queue`
returns the worst-scoring books first. The **Metadata Review** button in the web
UI (admin only) presents the same queue with per-book Edit, Cover, Resolve and
Lock actions, plus the enrichment and cover-upgrade controls.

## Email Book (Send to Kindle)

With SMTP configured, each book card gets an **Email Book** button that sends
the EPUB to a Kindle's Send to Kindle address (`name@kindle.com`). Amazon
converts EPUBs sent this way and delivers them to the reader's devices.

Setup:

1. Set all of `SMTP_SERVER`, `SMTP_PORT`, `SMTP_USER` and `SMTP_PASS` (and
   `SMTP_FROM` if the login is not an email address). Until all four are set the
   button is not shown; if `SMTP_SERVER`, `SMTP_USER` or `SMTP_PASS` is set
   without the others, the startup log names the missing ones.
2. Each reader adds the sender address to **Amazon > Manage Your Content and
   Devices > Preferences > Personal Document Settings > Approved Personal Document
   E-mail List**. Amazon silently discards mail from any other sender. The Email
   Book dialog shows which address to approve.

Each book is repaired for Send to Kindle on the way out, following
[kindle-epub-fix](https://github.com/innocenat/kindle-epub-fix). The library file
itself is never changed:

- pages without an encoding declaration get a UTF-8 one (Amazon otherwise
  assumes ISO-8859-1 and garbles accented characters);
- links to a page's `<body>` id are pointed at the page itself;
- image tags with no source are removed;
- a missing or undetermined (`und`) language becomes `en`, since Kindle refuses
  books without one, and free-text values like `US English (en-us)` are reduced
  to their language code. Languages Kindle does not support are left alone and
  reported back.

The attachment is sent as `application/epub+zip` under a plain-ASCII form of
the download name (`Piers_Anthony_-_Castle_Roogna.epub`), since accented
filenames are a known cause of rejection. Send to Kindle accepts
at most 50 MB per email, so larger books are refused with a message; your SMTP
provider may have a lower message limit (base64 adds about a third).

## UI Notes

- Browser UI is at `/`.
- `Group series` collapses each multi-book series into one stacked card; click it to
  expand the volumes.
- Admin login is required to see and use:
  - `Edit Metadata`, which also holds the series tools: `Find Series Books` (tag a
    series from Wikidata) and `Clean Up Series` (fix titles, authors and numbering
    for a whole series)
  - `Change Cover`
  - `Metadata Review` (quality queue, enrichment and cover upgrade)
  - `Rescan/Rebuild` controls
- OPDS clients can use `/opds` (or root with OPDS accept headers).

## Build and Run Locally

```bash
go build -o gopds ./cmd/gopds
./gopds
```

Run the checks CI runs with `go vet ./... && go test ./...`.

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
- Runs on push to `main`, `v*` tags, published releases, PRs, and manual dispatch
- Runs `go vet ./...` and `go test ./...` first; the image is only built if both pass
- Publishes to GHCR only for `v*` tags and published releases, tagged:
  - `latest`
  - the tag name
  - the commit SHA
- Uploads a compressed Docker image tarball artifact for every run (kept 14 days)

## Security Recommendations

- Use a strong `ADMIN_PASSWORD`, supplied via environment or secrets rather than a
  committed file.
- Run behind an HTTPS reverse proxy for internet exposure. The session cookie is only
  marked `Secure` when the server itself terminates TLS.
- Note that `/api/books` and `/download/{id}` are unauthenticated: anyone who can reach
  the server can enumerate and download the library.
- Email Book is unauthenticated too, like downloads. It can only send to the
  `EMAIL_ALLOWED_DOMAINS` (Kindle addresses by default), is limited to 20 emails an
  hour per client address, and runs at most two sends at once. Behind a reverse
  proxy all visitors share the proxy's address, and therefore that limit.
- Outbound cover fetches are restricted to an allowlist of Open Library, Google Books,
  Wikipedia and Wikimedia hosts.
- Protect `main` with required signed commits.
- Keep GHCR package visibility intentional (public/private).

## License

This project is licensed under the MIT License. See `LICENSE`.
