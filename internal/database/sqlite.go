// SPDX-License-Identifier: MIT

package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Pure Go driver, no CGO needed
)

type Book struct {
	ID          int       `json:"id"`
	Path        string    `json:"path"`
	Title       string    `json:"title"`
	Author      string    `json:"author"`
	Description string    `json:"description"`
	Category    string    `json:"category"`
	Subcategory string    `json:"subcategory"`
	Series      string    `json:"series"`
	SeriesIndex string    `json:"series_index"`
	ModTime     time.Time `json:"mod_time"`
}

type DB struct {
	conn *sql.DB
}

const booksTableDDL = `
CREATE TABLE IF NOT EXISTS books (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	path TEXT UNIQUE,
	title TEXT,
	author TEXT,
	description TEXT,
	category TEXT,
	subcategory TEXT,
	series TEXT,
	series_index TEXT,
	mod_time DATETIME
);`

// book_enrichment records what we know *about* each book's metadata quality,
// separate from the metadata itself. Keeping it in its own table means a full
// rebuild can drop and re-derive `books` without losing human decisions.
const enrichmentTableDDL = `
CREATE TABLE IF NOT EXISTS book_enrichment (
	book_id       INTEGER PRIMARY KEY,
	quality       INTEGER NOT NULL DEFAULT 0,
	review_flags  TEXT NOT NULL DEFAULT '',
	meta_source   TEXT NOT NULL DEFAULT 'epub',
	cover_source  TEXT NOT NULL DEFAULT 'epub',
	locked        INTEGER NOT NULL DEFAULT 0,
	last_checked  DATETIME
);`

const saveEnrichmentSQL = `
	INSERT INTO book_enrichment (book_id, quality, review_flags, meta_source, cover_source, last_checked)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(book_id) DO UPDATE SET
		quality=excluded.quality,
		review_flags=excluded.review_flags,
		last_checked=excluded.last_checked
	WHERE book_enrichment.locked = 0`

// RETURNING id is required for correctness here: on the DO UPDATE path
// LastInsertId() reports the most recent *insert* rowid, not this row's id,
// which previously caused covers to be filed under the wrong book on rescan.
const saveBookSQL = `
	INSERT INTO books (path, title, author, description, category, subcategory, series, series_index, mod_time)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(path) DO UPDATE SET
		title=excluded.title,
		author=excluded.author,
		description=excluded.description,
		category=excluded.category,
		subcategory=excluded.subcategory,
		series=excluded.series,
		series_index=excluded.series_index,
		mod_time=excluded.mod_time
	RETURNING id`

func New(dbPath string) (*DB, error) {
	// SQLite reports a bare "unable to open database file" when the parent
	// directory is missing, so create it up front for a clearer failure mode.
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create database directory %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	// Pragmas tuned for scan-heavy workloads.
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL`); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA temp_store=MEMORY`); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		return nil, err
	}

	if _, err := db.Exec(booksTableDDL); err != nil {
		return nil, err
	}
	if _, err := db.Exec(enrichmentTableDDL); err != nil {
		return nil, err
	}
	if err := ensureBooksColumns(db); err != nil {
		return nil, err
	}

	return &DB{conn: db}, nil
}

// NeedsReScan checks if the file at 'path' has been modified since last scan
func (db *DB) NeedsReScan(path string, currentModTime time.Time) bool {
	var lastMod time.Time
	err := db.conn.QueryRow("SELECT mod_time FROM books WHERE path = ?", path).Scan(&lastMod)
	if err == sql.ErrNoRows {
		return true // New book
	}
	return currentModTime.After(lastMod) // Re-scan if file is newer than DB entry
}

func (db *DB) SaveBook(b Book) (int64, error) {
	var id int64
	err := db.conn.QueryRow(saveBookSQL, b.Path, b.Title, b.Author, b.Description,
		b.Category, b.Subcategory, b.Series, b.SeriesIndex, b.ModTime).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (db *DB) Begin() (*sql.Tx, error) {
	return db.conn.Begin()
}

func (db *DB) SaveBookTx(tx *sql.Tx, b Book) (int64, error) {
	var id int64
	err := tx.QueryRow(saveBookSQL, b.Path, b.Title, b.Author, b.Description,
		b.Category, b.Subcategory, b.Series, b.SeriesIndex, b.ModTime).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (db *DB) UpdateBookMetadata(id int, title, author, description string, modTime time.Time) error {
	query := `
	UPDATE books
	SET title = ?, author = ?, description = ?, mod_time = ?
	WHERE id = ?`
	_, err := db.conn.Exec(query, title, author, description, modTime, id)
	return err
}

func (db *DB) UpdateBookPath(id int, path string) error {
	query := `
	UPDATE books
	SET path = ?
	WHERE id = ?`
	_, err := db.conn.Exec(query, path, id)
	return err
}

func (db *DB) RebuildBooksTable() error {
	if _, err := db.conn.Exec("DROP TABLE IF EXISTS books"); err != nil {
		return err
	}
	if _, err := db.conn.Exec(booksTableDDL); err != nil {
		return err
	}
	return nil
}

// GetAllBooks retrieves every book stored in the database.
func (db *DB) GetAllBooks() ([]Book, error) {
	query := "SELECT id, path, title, author, description, category, subcategory, coalesce(series,''), coalesce(series_index,''), mod_time FROM books"
	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var books []Book
	for rows.Next() {
		var b Book
		err := rows.Scan(&b.ID, &b.Path, &b.Title, &b.Author, &b.Description, &b.Category, &b.Subcategory, &b.Series, &b.SeriesIndex, &b.ModTime)
		if err != nil {
			return nil, err
		}
		books = append(books, b)
	}
	return books, nil
}

func (db *DB) GetBookByID(id string) (*Book, error) {
	var b Book
	query := "SELECT id, path, title, author, description, category, subcategory, coalesce(series,''), coalesce(series_index,''), mod_time FROM books WHERE id = ?"
	err := db.conn.QueryRow(query, id).Scan(&b.ID, &b.Path, &b.Title, &b.Author, &b.Description, &b.Category, &b.Subcategory, &b.Series, &b.SeriesIndex, &b.ModTime)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func ensureBooksColumns(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(books)")
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := map[string]struct{}{}
	for rows.Next() {
		var cid int
		var name string
		var ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		existing[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}

	if _, ok := existing["category"]; !ok {
		if _, err := db.Exec("ALTER TABLE books ADD COLUMN category TEXT"); err != nil {
			return err
		}
	}
	if _, ok := existing["subcategory"]; !ok {
		if _, err := db.Exec("ALTER TABLE books ADD COLUMN subcategory TEXT"); err != nil {
			return err
		}
	}
	if _, ok := existing["series"]; !ok {
		if _, err := db.Exec("ALTER TABLE books ADD COLUMN series TEXT"); err != nil {
			return err
		}
	}
	if _, ok := existing["series_index"]; !ok {
		if _, err := db.Exec("ALTER TABLE books ADD COLUMN series_index TEXT"); err != nil {
			return err
		}
	}
	return nil
}

const authorInitialExpr = `CASE
	WHEN trim(coalesce(author, '')) = '' THEN '#'
	WHEN upper(substr(trim(author), 1, 1)) GLOB '[A-Z]' THEN upper(substr(trim(author), 1, 1))
	ELSE '#'
END`

func (db *DB) CountBooksByAuthorRange(start, end string, includeOther bool) (int, error) {
	where := fmt.Sprintf("%s BETWEEN ? AND ?", authorInitialExpr)
	args := []any{start, end}
	if includeOther {
		where = fmt.Sprintf("(%s BETWEEN ? AND ? OR %s = ?)", authorInitialExpr, authorInitialExpr)
		args = append(args, "#")
	}

	query := fmt.Sprintf("SELECT COUNT(*) FROM books WHERE %s", where)
	var count int
	if err := db.conn.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (db *DB) GetBooksByAuthorRange(start, end string, includeOther bool, limit, offset int) ([]Book, error) {
	where := fmt.Sprintf("%s BETWEEN ? AND ?", authorInitialExpr)
	args := []any{start, end}
	if includeOther {
		where = fmt.Sprintf("(%s BETWEEN ? AND ? OR %s = ?)", authorInitialExpr, authorInitialExpr)
		args = append(args, "#")
	}

	query := fmt.Sprintf(
		"SELECT id, path, title, author, description, category, subcategory, coalesce(series,''), coalesce(series_index,''), mod_time FROM books WHERE %s ORDER BY author COLLATE NOCASE, title COLLATE NOCASE, id LIMIT ? OFFSET ?",
		where,
	)
	args = append(args, limit, offset)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	books := make([]Book, 0, limit)
	for rows.Next() {
		var b Book
		if err := rows.Scan(&b.ID, &b.Path, &b.Title, &b.Author, &b.Description, &b.Category, &b.Subcategory, &b.Series, &b.SeriesIndex, &b.ModTime); err != nil {
			return nil, err
		}
		books = append(books, b)
	}
	return books, nil
}

func (db *DB) GetCategoryCounts() (map[string]int, error) {
	rows, err := db.conn.Query(`SELECT trim(coalesce(category,'')) AS c, COUNT(*) FROM books WHERE trim(coalesce(category,'')) != '' GROUP BY c ORDER BY c COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var category string
		var count int
		if err := rows.Scan(&category, &count); err != nil {
			return nil, err
		}
		out[category] = count
	}
	return out, nil
}

func (db *DB) GetSubcategoryCounts(category string) (map[string]int, error) {
	rows, err := db.conn.Query(`SELECT trim(coalesce(subcategory,'')) AS s, COUNT(*) FROM books WHERE trim(coalesce(category,'')) = ? AND trim(coalesce(subcategory,'')) != '' GROUP BY s ORDER BY s COLLATE NOCASE`, strings.TrimSpace(category))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var subcategory string
		var count int
		if err := rows.Scan(&subcategory, &count); err != nil {
			return nil, err
		}
		out[subcategory] = count
	}
	return out, nil
}

func (db *DB) CountBooksByCategory(category, subcategory string) (int, error) {
	category = strings.TrimSpace(category)
	subcategory = strings.TrimSpace(subcategory)
	var query string
	var args []any
	if subcategory == "" {
		query = `SELECT COUNT(*) FROM books WHERE trim(coalesce(category,'')) = ?`
		args = []any{category}
	} else {
		query = `SELECT COUNT(*) FROM books WHERE trim(coalesce(category,'')) = ? AND trim(coalesce(subcategory,'')) = ?`
		args = []any{category, subcategory}
	}

	var count int
	if err := db.conn.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (db *DB) GetBooksByCategory(category, subcategory string, limit, offset int) ([]Book, error) {
	category = strings.TrimSpace(category)
	subcategory = strings.TrimSpace(subcategory)

	query := "SELECT id, path, title, author, description, category, subcategory, coalesce(series,''), coalesce(series_index,''), mod_time FROM books WHERE trim(coalesce(category,'')) = ?"
	args := []any{category}
	if subcategory != "" {
		query += " AND trim(coalesce(subcategory,'')) = ?"
		args = append(args, subcategory)
	}
	query += " ORDER BY author COLLATE NOCASE, title COLLATE NOCASE, id LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	books := make([]Book, 0, limit)
	for rows.Next() {
		var b Book
		if err := rows.Scan(&b.ID, &b.Path, &b.Title, &b.Author, &b.Description, &b.Category, &b.Subcategory, &b.Series, &b.SeriesIndex, &b.ModTime); err != nil {
			return nil, err
		}
		books = append(books, b)
	}
	return books, nil
}

// Enrichment is the quality/provenance record for one book.
type Enrichment struct {
	BookID      int       `json:"book_id"`
	Quality     int       `json:"quality"`
	ReviewFlags string    `json:"review_flags"`
	MetaSource  string    `json:"meta_source"`
	CoverSource string    `json:"cover_source"`
	Locked      bool      `json:"locked"`
	LastChecked time.Time `json:"last_checked"`
}

// SaveEnrichmentTx upserts a quality record. Rows the user has locked are left
// alone: a human decision always outranks an automated one.
func (db *DB) SaveEnrichmentTx(tx *sql.Tx, e Enrichment) error {
	_, err := tx.Exec(saveEnrichmentSQL, e.BookID, e.Quality, e.ReviewFlags,
		e.MetaSource, e.CoverSource, e.LastChecked)
	return err
}

// SetEnrichmentLocked marks a book as human-curated (or releases it).
func (db *DB) SetEnrichmentLocked(bookID int, locked bool) error {
	v := 0
	if locked {
		v = 1
	}
	_, err := db.conn.Exec(`
		INSERT INTO book_enrichment (book_id, locked) VALUES (?, ?)
		ON CONFLICT(book_id) DO UPDATE SET locked=excluded.locked`, bookID, v)
	return err
}

// QualitySummary is an aggregate view of library health.
type QualitySummary struct {
	Total       int            `json:"total"`
	Scored      int            `json:"scored"`
	Unscored    int            `json:"unscored"`
	AvgQuality  float64        `json:"avg_quality"`
	Locked      int            `json:"locked"`
	FlagCounts  map[string]int `json:"flag_counts"`
	NeedsReview int            `json:"needs_review"`
}

func (db *DB) GetQualitySummary() (*QualitySummary, error) {
	out := &QualitySummary{FlagCounts: map[string]int{}}

	if err := db.conn.QueryRow(`SELECT COUNT(*) FROM books`).Scan(&out.Total); err != nil {
		return nil, err
	}

	var avg sql.NullFloat64
	if err := db.conn.QueryRow(
		`SELECT COUNT(*), AVG(quality) FROM book_enrichment`,
	).Scan(&out.Scored, &avg); err != nil {
		return nil, err
	}
	// AVG is over scored books only; an unscored library must not report 0/100
	// as though every book were terrible.
	if avg.Valid {
		out.AvgQuality = avg.Float64
	}

	if err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM book_enrichment WHERE locked = 1`).Scan(&out.Locked); err != nil {
		return nil, err
	}

	// Books with no enrichment row have never been scored. This is the normal
	// state after upgrading an existing library: the incremental scan skips
	// unchanged files, so nothing gets scored until a full rebuild runs.
	if err := db.conn.QueryRow(`
		SELECT COUNT(*) FROM books b
		LEFT JOIN book_enrichment e ON e.book_id = b.id
		WHERE e.book_id IS NULL`).Scan(&out.Unscored); err != nil {
		return nil, err
	}

	rows, err := db.conn.Query(
		`SELECT review_flags FROM book_enrichment WHERE trim(review_flags) != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var flags string
		if err := rows.Scan(&flags); err != nil {
			return nil, err
		}
		out.NeedsReview++
		for _, f := range strings.Split(flags, ",") {
			if f = strings.TrimSpace(f); f != "" {
				out.FlagCounts[f]++
			}
		}
	}
	return out, rows.Err()
}

// GetBooksNeedingWork returns the lowest-quality unlocked books first, which is
// the work queue later enrichment tiers consume.
//
// Only scored books are returned. An unscored book has no enrichment row, and
// coalescing its quality to 0 would rank it alongside genuinely bad books --
// so before a scan has scored the library this degenerates into plain book-id
// order, silently processing arbitrary books instead of the worst ones.
func (db *DB) GetBooksNeedingWork(limit int) ([]Book, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.conn.Query(`
		SELECT b.id, b.path, b.title, b.author, b.description, b.category, b.subcategory,
		       coalesce(b.series,''), coalesce(b.series_index,''), b.mod_time
		FROM books b
		JOIN book_enrichment e ON e.book_id = b.id
		WHERE e.locked = 0
		ORDER BY e.quality ASC, b.id ASC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	books := make([]Book, 0, limit)
	for rows.Next() {
		var b Book
		if err := rows.Scan(&b.ID, &b.Path, &b.Title, &b.Author, &b.Description,
			&b.Category, &b.Subcategory, &b.Series, &b.SeriesIndex, &b.ModTime); err != nil {
			return nil, err
		}
		books = append(books, b)
	}
	return books, rows.Err()
}

// SetEnrichmentReview records that a book needs human review, without
// disturbing its quality score or locked state.
func (db *DB) SetEnrichmentReview(bookID int, flag string) error {
	_, err := db.conn.Exec(`
		INSERT INTO book_enrichment (book_id, review_flags) VALUES (?, ?)
		ON CONFLICT(book_id) DO UPDATE SET review_flags=excluded.review_flags
		WHERE book_enrichment.locked = 0`, bookID, flag)
	return err
}

// ReviewItem is a book plus the quality record explaining why it needs work.
type ReviewItem struct {
	Book        Book   `json:"book"`
	Quality     int    `json:"quality"`
	ReviewFlags string `json:"review_flags"`
	Locked      bool   `json:"locked"`
}

// GetReviewQueue returns books needing human attention, worst score first.
// Locked books are excluded: the user has already ruled on them.
func (db *DB) GetReviewQueue(flag string, limit, offset int) ([]ReviewItem, int, error) {
	if limit <= 0 {
		limit = 25
	}
	where := "coalesce(e.locked,0) = 0 AND trim(coalesce(e.review_flags,'')) != ''"
	args := []any{}
	if f := strings.TrimSpace(flag); f != "" {
		where += " AND ',' || e.review_flags || ',' LIKE ?"
		args = append(args, "%,"+f+",%")
	}

	var total int
	countQ := "SELECT COUNT(*) FROM books b JOIN book_enrichment e ON e.book_id = b.id WHERE " + where
	if err := db.conn.QueryRow(countQ, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	q := `SELECT b.id, b.path, b.title, b.author, b.description, b.category, b.subcategory,
	             coalesce(b.series,''), coalesce(b.series_index,''),
	             b.mod_time, e.quality, coalesce(e.review_flags,''), coalesce(e.locked,0)
	      FROM books b JOIN book_enrichment e ON e.book_id = b.id
	      WHERE ` + where + `
	      ORDER BY e.quality ASC, b.id ASC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := db.conn.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]ReviewItem, 0, limit)
	for rows.Next() {
		var it ReviewItem
		var locked int
		if err := rows.Scan(&it.Book.ID, &it.Book.Path, &it.Book.Title, &it.Book.Author,
			&it.Book.Description, &it.Book.Category, &it.Book.Subcategory,
			&it.Book.Series, &it.Book.SeriesIndex, &it.Book.ModTime,
			&it.Quality, &it.ReviewFlags, &locked); err != nil {
			return nil, 0, err
		}
		it.Locked = locked == 1
		out = append(out, it)
	}
	return out, total, rows.Err()
}

// ClearEnrichmentReview removes review flags for a book, marking it resolved.
func (db *DB) ClearEnrichmentReview(bookID int) error {
	_, err := db.conn.Exec(
		`UPDATE book_enrichment SET review_flags='' WHERE book_id = ?`, bookID)
	return err
}
