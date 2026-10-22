// SPDX-License-Identifier: MIT

package web

import (
	"context"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ab0oo/gopds/internal/database"
	"github.com/go-chi/chi/v5"
)

func TestBookFilename(t *testing.T) {
	cases := []struct {
		book database.Book
		want string
	}{
		{database.Book{Title: "Castle Roogna", Author: "Piers Anthony"}, "Piers Anthony - Castle Roogna.epub"},
		{database.Book{Title: "Crewel Lye: A Caustic Yarn", Author: "Piers Anthony"}, "Piers Anthony - Crewel Lye - A Caustic Yarn.epub"},
		{database.Book{Title: `What If? "Serious" Answers`, Author: "Randall Munroe"}, "Randall Munroe - What If Serious Answers.epub"},
		{database.Book{Title: "AC/DC", Author: "Some One"}, "Some One - AC-DC.epub"},
		{database.Book{Title: "Der Außenseiter", Author: "Gregg Hurwitz"}, "Gregg Hurwitz - Der Außenseiter.epub"},
		{database.Book{Title: "Untitled...", Author: "Unknown Author"}, "Untitled.epub"},
		{database.Book{Title: "", Author: "", Path: "/books/x/hash123.epub"}, "hash123.epub"},
		{database.Book{ID: 7, Title: "", Author: "", Path: ""}, "book-7.epub"},
	}
	for _, c := range cases {
		if got := bookFilename(c.book); got != c.want {
			t.Errorf("bookFilename(%q, %q) = %q, want %q", c.book.Author, c.book.Title, got, c.want)
		}
	}

	long := bookFilename(database.Book{Title: strings.Repeat("Very Long Title ", 30), Author: "A"})
	if n := len([]rune(long)); n > maxBookNameRunes+len(".epub") || !strings.HasSuffix(long, ".epub") {
		t.Errorf("long name not capped: %d runes, %q", n, long)
	}
}

func TestContentDisposition(t *testing.T) {
	for _, name := range []string{
		"Piers Anthony - Castle Roogna.epub",
		"Gregg Hurwitz - Der Außenseiter.epub",
		"Cixin Liu - 三体.epub",
		"Piers Anthony - Demons Don’t Dream.epub",
	} {
		header := contentDisposition(name)
		disposition, params, err := mime.ParseMediaType(header)
		if err != nil || disposition != "attachment" {
			t.Fatalf("%q: %v", header, err)
		}
		if params["filename"] != name {
			t.Errorf("%q decodes to %q, want %q", header, params["filename"], name)
		}
		// The quoted fallback must be plain ASCII for older clients.
		fallback := header[strings.Index(header, `filename="`)+len(`filename="`):]
		fallback = fallback[:strings.Index(fallback, `"`)]
		for _, r := range fallback {
			if r > 127 {
				t.Errorf("fallback %q is not ASCII", fallback)
				break
			}
		}
	}
	if got := contentDisposition("Gregg Hurwitz - Der Außenseiter.epub"); !strings.Contains(got, `filename="Gregg Hurwitz - Der Aussenseiter.epub"`) {
		t.Errorf("fallback not folded: %s", got)
	}
}

func TestHandleDownloadUsesCanonicalName(t *testing.T) {
	dir := t.TempDir()
	db, err := database.New(filepath.Join(dir, "gopds.db"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Piers Anthony - Xanth 03 - Castle Roogna_split.epub")
	if err := os.WriteFile(path, []byte("PK"), 0644); err != nil {
		t.Fatal(err)
	}
	id, err := db.SaveBook(database.Book{Path: path, Title: "Castle Roogna", Author: "Piers Anthony"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/download/"+strconv.Itoa(int(id)), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(int(id)))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	(&Server{db: db}).HandleDownload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	_, params, err := mime.ParseMediaType(rec.Header().Get("Content-Disposition"))
	if err != nil || params["filename"] != "Piers Anthony - Castle Roogna.epub" {
		t.Errorf("Content-Disposition %q (%v)", rec.Header().Get("Content-Disposition"), err)
	}
}
