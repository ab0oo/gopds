// SPDX-License-Identifier: MIT

package web

import (
	"archive/zip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ab0oo/gopds/internal/database"
	"github.com/ab0oo/gopds/internal/mailer"
	"github.com/go-chi/chi/v5"
)

func newEmailTestServer(t *testing.T) (*Server, int, *[]string) {
	t.Helper()
	dir := t.TempDir()
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
		"content.opf":            `<package><metadata><dc:title>Castle Roogna</dc:title></metadata></package>`,
		"ch1.xhtml":              `<html><body><p>Hi</p></body></html>`,
	} {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	_ = f.Close()

	id, err := db.SaveBook(database.Book{Path: epub, Title: "Castle Roogna", Author: "Piers Anthony"})
	if err != nil {
		t.Fatal(err)
	}

	var sent []string
	s := &Server{db: db}
	s.email = &emailState{
		cfg:     mailer.Config{Server: "smtp.example.com", Port: 587, From: "books@example.com"},
		enabled: true,
		domains: defaultEmailDomains,
		recent:  map[string][]time.Time{},
		slots:   make(chan struct{}, emailMaxConcurrent),
		send: func(cfg mailer.Config, to string, msg []byte) error {
			sent = append(sent, to+"\n"+string(msg))
			return nil
		},
	}
	s.emailOnce.Do(func() {})
	return s, int(id), &sent
}

func postEmail(s *Server, id int, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/books/"+strconv.Itoa(id)+"/email", strings.NewReader(body))
	req.RemoteAddr = "192.0.2.10:5555"
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(id))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.HandleEmailBook(rec, req)
	return rec
}

func TestHandleEmailBook(t *testing.T) {
	s, id, sent := newEmailTestServer(t)

	rec := postEmail(s, id, `{"to":"reader@kindle.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var resp emailBookResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Filename != "Piers_Anthony_-_Castle_Roogna.epub" || len(resp.Fixes) == 0 {
		t.Errorf("response %+v", resp)
	}
	if len(*sent) != 1 || !strings.HasPrefix((*sent)[0], "reader@kindle.com\n") ||
		!strings.Contains((*sent)[0], "application/epub+zip") {
		t.Fatalf("sent %d messages", len(*sent))
	}

	for body, want := range map[string]int{
		`{"to":"someone@gmail.com"}`: http.StatusBadRequest, // not a Kindle address
		`{"to":"not an address"}`:    http.StatusBadRequest,
		`{"to":`:                     http.StatusBadRequest,
	} {
		if rec := postEmail(s, id, body); rec.Code != want {
			t.Errorf("%s: status %d, want %d", body, rec.Code, want)
		}
	}
	if rec := postEmail(s, id+100, `{"to":"reader@kindle.com"}`); rec.Code != http.StatusNotFound {
		t.Errorf("missing book: status %d", rec.Code)
	}
	if len(*sent) != 1 {
		t.Errorf("rejected requests sent mail: %d messages", len(*sent))
	}
}

func TestHandleEmailBookRateLimit(t *testing.T) {
	s, id, _ := newEmailTestServer(t)
	for i := 0; i < emailSendsPerHour; i++ {
		if rec := postEmail(s, id, `{"to":"reader@kindle.com"}`); rec.Code != http.StatusOK {
			t.Fatalf("send %d: status %d", i, rec.Code)
		}
	}
	if rec := postEmail(s, id, `{"to":"reader@kindle.com"}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over the limit: status %d", rec.Code)
	}
}

func TestHandleEmailBookDisabled(t *testing.T) {
	s, id, _ := newEmailTestServer(t)
	s.email.enabled = false
	if rec := postEmail(s, id, `{"to":"reader@kindle.com"}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestParseEmailDomains(t *testing.T) {
	if got := parseEmailDomains(""); len(got) != len(defaultEmailDomains) {
		t.Errorf("default = %v", got)
	}
	if got := parseEmailDomains("*"); got != nil {
		t.Errorf("* = %v, want nil (any)", got)
	}
	if got := parseEmailDomains(" Kindle.com, @kobo.example "); len(got) != 2 || got[1] != "kobo.example" {
		t.Errorf("list = %v", got)
	}
}
