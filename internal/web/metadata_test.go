// SPDX-License-Identifier: MIT

package web

import (
	"archive/zip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ab0oo/gopds/internal/database"
	"github.com/go-chi/chi/v5"
)

// Editing a book's subjects must move it to the matching subcategory. The
// save refreshes the stored mod time, so no later scan would fix it.
func TestHandleUpdateMetadataRefreshesCategory(t *testing.T) {
	t.Setenv("CATEGORY_SOURCE", "subject")
	dir := t.TempDir()
	t.Setenv("BOOK_PATH", dir)
	db, err := database.New(filepath.Join(dir, "gopds.db"))
	if err != nil {
		t.Fatal(err)
	}

	epub := filepath.Join(dir, "book.epub")
	f, err := os.Create(epub)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range map[string]string{
		"mimetype":               "application/epub+zip",
		"META-INF/container.xml": `<container><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>`,
		"content.opf": `<package xmlns="http://www.idpf.org/2007/opf" version="2.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">` +
			`<dc:title>Castle Roogna</dc:title><dc:creator>Piers Anthony</dc:creator><dc:subject>Fantasy ficiton</dc:subject>` +
			`</metadata></package>`,
	} {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	_ = f.Close()

	id, err := db.SaveBook(database.Book{Path: epub, Title: "Castle Roogna", Author: "Piers Anthony",
		Category: "Fantasy", Subcategory: "Fantasy ficiton", ModTime: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	body := `{"title":"Castle Roogna","author":"Piers Anthony","subjects":["fantasy fiction"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/books/"+strconv.Itoa(int(id))+"/metadata", strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(int(id)))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	(&Server{db: db}).HandleUpdateMetadata(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	got, err := db.GetSubcategoryCounts("Fantasy")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{"Fantasy Fiction": 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("subcategories = %v, want %v", got, want)
	}
}
