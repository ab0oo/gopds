// SPDX-License-Identifier: MIT

package scanner

import (
	"archive/zip"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/ab0oo/gopds/internal/database"
)

// writeTestEPUB writes the smallest EPUB the scanner can read.
func writeTestEPUB(t *testing.T, path, title string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	files := map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?><container><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>`,
		"content.opf": `<?xml version="1.0"?><package><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">` +
			`<dc:title>` + title + `</dc:title><dc:creator>Piers Anthony</dc:creator></metadata></package>`,
	}
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

// idsByRelPath maps each book's path under root to its id.
func idsByRelPath(t *testing.T, db *database.DB, root string) map[string]int {
	t.Helper()
	books, err := db.GetAllBooks()
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, b := range books {
		rel, ok := database.RelativeLibraryPath(real, b.Path)
		if !ok {
			t.Fatalf("book %d path %q is outside %q", b.ID, b.Path, real)
		}
		out[rel] = b.ID
	}
	return out
}

// rebuild does what the admin Rebuild does to the books table.
func rebuild(t *testing.T, db *database.DB, root string) {
	t.Helper()
	if err := db.MarkAllForRescan(); err != nil {
		t.Fatal(err)
	}
	sc := New(db)
	if err := sc.Start(root); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PruneMissingBooks(sc.SeenPaths()); err != nil {
		t.Fatal(err)
	}
}

func TestBookIDsSurviveRebuildAndLibraryMove(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir) // covers are cached under ./data/covers
	db, err := database.New(filepath.Join(dir, "db", "gopds.db"))
	if err != nil {
		t.Fatal(err)
	}

	libA := filepath.Join(dir, "mnt-a", "books")
	writeTestEPUB(t, filepath.Join(libA, "Piers Anthony", "Castle Roogna", "book.epub"), "Xanth 03 - Castle Roogna")
	writeTestEPUB(t, filepath.Join(libA, "Piers Anthony", "Centaur Aisle", "book.epub"), "Xanth 04 - Centaur Aisle")
	writeTestEPUB(t, filepath.Join(libA, "flat.epub"), "Ogre, Ogre")

	if err := New(db).Start(libA); err != nil {
		t.Fatal(err)
	}
	before := idsByRelPath(t, db, libA)
	if len(before) != 3 {
		t.Fatalf("indexed %d books, want 3", len(before))
	}
	roogna := before["Piers Anthony/Castle Roogna/book.epub"]
	if err := db.SetEnrichmentLocked(roogna, true); err != nil {
		t.Fatal(err)
	}

	// Rebuild with one book deleted and one added.
	if err := os.Remove(filepath.Join(libA, "flat.epub")); err != nil {
		t.Fatal(err)
	}
	writeTestEPUB(t, filepath.Join(libA, "Piers Anthony", "Night Mare", "book.epub"), "Xanth 06 - Night Mare")
	rebuild(t, db, libA)

	after := idsByRelPath(t, db, libA)
	if _, ok := after["flat.epub"]; ok {
		t.Error("deleted book survived the rebuild")
	}
	for _, rel := range []string{"Piers Anthony/Castle Roogna/book.epub", "Piers Anthony/Centaur Aisle/book.epub"} {
		if after[rel] != before[rel] {
			t.Errorf("%s: id %d after rebuild, was %d", rel, after[rel], before[rel])
		}
	}
	nightMare := after["Piers Anthony/Night Mare/book.epub"]
	for _, id := range before {
		if nightMare == id {
			t.Errorf("new book reused id %d of a previous book", id)
		}
	}
	if e, err := db.GetEnrichment(roogna); err != nil || e == nil || !e.Locked {
		t.Fatalf("lock lost on rebuild: %+v, %v", e, err)
	}

	// Mount the same library somewhere else: an older row without a relative
	// path (as saved before this column existed) must be relinked too.
	if err := clearRelPaths(filepath.Join(dir, "db", "gopds.db")); err != nil {
		t.Fatal(err)
	}
	if err := New(db).Start(libA); err != nil { // backfills rel_path
		t.Fatal(err)
	}
	libB := filepath.Join(dir, "srv", "library")
	if err := os.MkdirAll(filepath.Dir(libB), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(libA, libB); err != nil {
		t.Fatal(err)
	}
	rebuild(t, db, libB)

	moved := idsByRelPath(t, db, libB)
	if len(moved) != 3 {
		t.Fatalf("after move: %d books, want 3 (%v)", len(moved), moved)
	}
	for rel, id := range after {
		if moved[rel] != id {
			t.Errorf("%s: id %d after move, was %d", rel, moved[rel], id)
		}
	}
	if e, err := db.GetEnrichment(roogna); err != nil || e == nil || !e.Locked {
		t.Fatalf("lock lost on move: %+v, %v", e, err)
	}
}

func TestPruneRefusesEmptyScan(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	db, err := database.New(filepath.Join(dir, "gopds.db"))
	if err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(dir, "books")
	writeTestEPUB(t, filepath.Join(lib, "a.epub"), "A")
	if err := New(db).Start(lib); err != nil {
		t.Fatal(err)
	}

	// An unmounted library looks like an empty directory.
	empty := filepath.Join(dir, "unmounted")
	if err := os.MkdirAll(empty, 0755); err != nil {
		t.Fatal(err)
	}
	sc := New(db)
	if err := sc.Start(empty); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PruneMissingBooks(sc.SeenPaths()); err == nil {
		t.Fatal("pruning after an empty scan should be refused")
	}
	if books, _ := db.GetAllBooks(); len(books) != 1 {
		t.Fatalf("books = %d, want 1", len(books))
	}
}

// clearRelPaths makes the database look like one saved before rel_path was
// recorded.
func clearRelPaths(dbPath string) error {
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Exec(`UPDATE books SET rel_path = NULL`)
	return err
}
