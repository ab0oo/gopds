package database

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite" // Pure Go driver, no CGO needed
)

type Book struct {
	ID          int       `json:"id"`
	Path        string    `json:"path"`
	Title       string    `json:"title"`
	Author      string    `json:"author"`
	Description string    `json:"description"`
	ModTime     time.Time `json:"mod_time"`
}

type DB struct {
	conn *sql.DB
}

func New(dbPath string) (*DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	// Create table if it doesn't exist
	query := `
	CREATE TABLE IF NOT EXISTS books (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		path TEXT UNIQUE,
		title TEXT,
		author TEXT,
		description TEXT,
		mod_time DATETIME
	);`

	if _, err := db.Exec(query); err != nil {
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
	query := `
	INSERT INTO books (path, title, author, description, mod_time)
	VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(path) DO UPDATE SET
		title=excluded.title,
		author=excluded.author,
		description=excluded.description,
		mod_time=excluded.mod_time`

	result, err := db.conn.Exec(query, b.Path, b.Title, b.Author, b.Description, b.ModTime)
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

// GetAllBooks retrieves every book stored in the database.
func (db *DB) GetAllBooks() ([]Book, error) {
	query := "SELECT id, path, title, author, description, mod_time FROM books"
	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var books []Book
	for rows.Next() {
		var b Book
		err := rows.Scan(&b.ID, &b.Path, &b.Title, &b.Author, &b.Description, &b.ModTime)
		if err != nil {
			return nil, err
		}
		books = append(books, b)
	}
	return books, nil
}

func (db *DB) GetBookByID(id string) (*Book, error) {
	var b Book
	query := "SELECT id, path, title, author, description, mod_time FROM books WHERE id = ?"
	err := db.conn.QueryRow(query, id).Scan(&b.ID, &b.Path, &b.Title, &b.Author, &b.Description, &b.ModTime)
	if err != nil {
		return nil, err
	}
	return &b, nil
}
