// SPDX-License-Identifier: MIT

package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ab0oo/gopds/internal/database"
	"github.com/go-chi/chi/v5"
)

// The query service's real 429 page: no Retry-After, the wait is in the text.
const wikidataTooManyBody = `<head>
<meta http-equiv="Content-Type" content="text/html;charset=utf-8"/>
<title>Error 429 Too Many Requests - Please retry in 120 seconds.</title>
</head>
<body><h2>HTTP ERROR 429</h2>
<p>Problem accessing /bigdata/namespace/wdq/sparql. Reason:
<pre>    Too Many Requests - Please retry in 120 seconds.</pre></p><hr><a href="http://eclipse.org/jetty">Powered by Jetty:// 9.4.12.v20180830</a><hr/>`

// stubWikidata resets the pause gate with a movable clock and serves handler.
func stubWikidata(t *testing.T, now *time.Time, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	prev := wikidata
	wikidata = &wikidataGate{now: func() time.Time { return *now }}
	t.Cleanup(func() {
		srv.Close()
		wikidata = prev
	})
	return srv.URL
}

func TestWikidataTooManyRequestsPausesLookups(t *testing.T) {
	var hits atomic.Int32
	now := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	endpoint := stubWikidata(t, &now, func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(wikidataTooManyBody))
			return
		}
		_, _ = w.Write([]byte(`{"search":[]}`))
	})

	var res wikidataSearchResponse
	err := fetchWikidataJSON(http.DefaultClient, endpoint, &res)
	var paused *wikidataPausedError
	if !errors.As(err, &paused) {
		t.Fatalf("got %v, want a wikidataPausedError", err)
	}
	if want := now.Add(120 * time.Second); !paused.Until.Equal(want) {
		t.Fatalf("paused until %s, want %s", paused.Until, want)
	}

	// While paused, nothing is sent.
	now = now.Add(119 * time.Second)
	if err := fetchWikidataJSON(http.DefaultClient, endpoint, &res); !errors.As(err, &paused) {
		t.Fatalf("while paused: got %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("%d requests sent, want 1", n)
	}

	now = now.Add(2 * time.Second)
	if err := fetchWikidataJSON(http.DefaultClient, endpoint, &res); err != nil {
		t.Fatalf("after the pause: %v", err)
	}
}

func TestWikidataRetryAfterHeaderWins(t *testing.T) {
	now := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	endpoint := stubWikidata(t, &now, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(wikidataTooManyBody))
	})

	var res wikidataSearchResponse
	var paused *wikidataPausedError
	if err := fetchWikidataJSON(http.DefaultClient, endpoint, &res); !errors.As(err, &paused) {
		t.Fatalf("got %v", err)
	}
	if want := now.Add(30 * time.Second); !paused.Until.Equal(want) {
		t.Fatalf("paused until %s, want %s", paused.Until, want)
	}
}

func TestWikidataOtherErrorsDoNotPause(t *testing.T) {
	now := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	endpoint := stubWikidata(t, &now, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})

	var res wikidataSearchResponse
	err := fetchWikidataJSON(http.DefaultClient, endpoint, &res)
	var paused *wikidataPausedError
	if err == nil || errors.As(err, &paused) {
		t.Fatalf("got %v, want a plain upstream error", err)
	}
	if wikidata.check() != nil {
		t.Fatal("a 502 must not pause lookups")
	}
}

func TestWriteWikidataPaused(t *testing.T) {
	now := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	stubWikidata(t, &now, func(http.ResponseWriter, *http.Request) {})

	rec := httptest.NewRecorder()
	if writeWikidataPaused(rec, errors.New("boom")) {
		t.Fatal("wrote a response for an unrelated error")
	}

	rec = httptest.NewRecorder()
	if !writeWikidataPaused(rec, &wikidataPausedError{Until: now.Add(90*time.Second + time.Millisecond)}) {
		t.Fatal("did not write the pause")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "91" {
		t.Errorf("Retry-After %q, want 91", got)
	}
	if !strings.Contains(rec.Body.String(), "91 seconds") {
		t.Errorf("body %q", rec.Body.String())
	}
}

// Both series buttons must report the pause rather than an empty result,
// which would read as "Wikidata knows no series for this book".
func TestSeriesHandlersReportWikidataPause(t *testing.T) {
	now := time.Now()
	stubWikidata(t, &now, func(http.ResponseWriter, *http.Request) {
		t.Error("a request was sent while paused")
	})
	_ = wikidata.pause(45 * time.Second)

	db, err := database.New(filepath.Join(t.TempDir(), "gopds.db"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := db.SaveBook(database.Book{Path: "/books/a.epub", Title: "Castle Roogna", Author: "Piers Anthony"})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{db: db}

	for name, handle := range map[string]http.HandlerFunc{
		"lookup":  s.HandleSeriesLookup,
		"cleanup": s.HandleSeriesCleanup,
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/books/"+strconv.Itoa(int(id))+"/series/"+name, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", strconv.Itoa(int(id)))
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		handle(rec, req)
		if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "45" {
			t.Errorf("%s: status %d, Retry-After %q; want 429 and 45", name, rec.Code, rec.Header().Get("Retry-After"))
		}
	}
}
