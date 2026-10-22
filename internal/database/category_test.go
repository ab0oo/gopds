// SPDX-License-Identifier: MIT

package database

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSaveBookCleansCategoryLabels(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "gopds.db"))
	if err != nil {
		t.Fatal(err)
	}
	for i, sub := range []string{"Fantasy fiction", "FANTASY FICTION", "Fantasy  Fiction"} {
		if _, err := db.SaveBook(Book{Path: filepath.Join("/books", sub, string(rune('a'+i))+".epub"), Title: "T", Category: "fantasy", Subcategory: sub, ModTime: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := db.GetSubcategoryCounts("Fantasy")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{"Fantasy Fiction": 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("subcategories = %v, want %v", got, want)
	}
}

// Rows saved before labels were cleaned are fixed when the database opens,
// since an unchanged file is never re-read by a scan.
func TestNormalizeCategoryLabelsFixesExistingRows(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "gopds.db"))
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range [][2]string{
		{"Fantasy", "Fantasy fiction"},
		{"Fantasy", "Fantasy Fiction"},
		{"fantasy", "fantasy fiction"},
		{"Fantasy", ""},
		{"", ""},
	} {
		if _, err := db.conn.Exec(`INSERT INTO books (path, title, category, subcategory, mod_time) VALUES (?, 'T', ?, ?, ?)`,
			filepath.Join("/books", string(rune('a'+i))+".epub"), row[0], row[1], time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.normalizeCategoryLabels(); err != nil {
		t.Fatal(err)
	}

	cats, err := db.GetCategoryCounts()
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{"Fantasy": 4}; !reflect.DeepEqual(cats, want) {
		t.Errorf("categories = %v, want %v", cats, want)
	}
	subs, err := db.GetSubcategoryCounts("Fantasy")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{"Fantasy Fiction": 3}; !reflect.DeepEqual(subs, want) {
		t.Errorf("subcategories = %v, want %v", subs, want)
	}
}
