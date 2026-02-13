package database

import (
	"database/sql"
	"fmt"
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
	mod_time DATETIME
);`

const saveBookSQL = `
	INSERT INTO books (path, title, author, description, category, subcategory, mod_time)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(path) DO UPDATE SET
		title=excluded.title,
		author=excluded.author,
		description=excluded.description,
		category=excluded.category,
		subcategory=excluded.subcategory,
		mod_time=excluded.mod_time`

func New(dbPath string) (*DB, error) {
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
	result, err := db.conn.Exec(saveBookSQL, b.Path, b.Title, b.Author, b.Description, b.Category, b.Subcategory, b.ModTime)
	if err != nil {
		return 0, err
	}

	return result.LastInsertId()
}

func (db *DB) Begin() (*sql.Tx, error) {
	return db.conn.Begin()
}

func (db *DB) SaveBookTx(tx *sql.Tx, b Book) (int64, error) {
	result, err := tx.Exec(saveBookSQL, b.Path, b.Title, b.Author, b.Description, b.Category, b.Subcategory, b.ModTime)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
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
	query := "SELECT id, path, title, author, description, category, subcategory, mod_time FROM books"
	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var books []Book
	for rows.Next() {
		var b Book
		err := rows.Scan(&b.ID, &b.Path, &b.Title, &b.Author, &b.Description, &b.Category, &b.Subcategory, &b.ModTime)
		if err != nil {
			return nil, err
		}
		books = append(books, b)
	}
	return books, nil
}

func (db *DB) GetBookByID(id string) (*Book, error) {
	var b Book
	query := "SELECT id, path, title, author, description, category, subcategory, mod_time FROM books WHERE id = ?"
	err := db.conn.QueryRow(query, id).Scan(&b.ID, &b.Path, &b.Title, &b.Author, &b.Description, &b.Category, &b.Subcategory, &b.ModTime)
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
		"SELECT id, path, title, author, description, category, subcategory, mod_time FROM books WHERE %s ORDER BY author COLLATE NOCASE, title COLLATE NOCASE, id LIMIT ? OFFSET ?",
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
		if err := rows.Scan(&b.ID, &b.Path, &b.Title, &b.Author, &b.Description, &b.Category, &b.Subcategory, &b.ModTime); err != nil {
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

	query := "SELECT id, path, title, author, description, category, subcategory, mod_time FROM books WHERE trim(coalesce(category,'')) = ?"
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
		if err := rows.Scan(&b.ID, &b.Path, &b.Title, &b.Author, &b.Description, &b.Category, &b.Subcategory, &b.ModTime); err != nil {
			return nil, err
		}
		books = append(books, b)
	}
	return books, nil
}
