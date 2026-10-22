// SPDX-License-Identifier: MIT

package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const googleDailyQuotaBody = `{
  "error": {
    "code": 429,
    "message": "Quota exceeded for quota metric 'Queries' and limit 'Queries per day' of service 'books.googleapis.com' for consumer 'project_number:1'.",
    "errors": [{"domain": "global", "reason": "rateLimitExceeded"}],
    "status": "RESOURCE_EXHAUSTED"
  }
}`

// stubGoogleBooks points lookups at handler and resets the pause gate, with the
// clock pinned to now.
func stubGoogleBooks(t *testing.T, now time.Time, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	prevEndpoint, prevGate := googleBooksEndpoint, googleBooks
	googleBooksEndpoint = srv.URL
	googleBooks = &googleBooksGate{now: func() time.Time { return now }}
	t.Cleanup(func() {
		srv.Close()
		googleBooksEndpoint, googleBooks = prevEndpoint, prevGate
	})
}

func TestGoogleBooksDailyQuotaPausesUntilPacificMidnight(t *testing.T) {
	var hits atomic.Int32
	now := time.Date(2026, 10, 7, 3, 46, 18, 0, time.UTC) // 20:46 PDT on Oct 6
	stubGoogleBooks(t, now, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(googleDailyQuotaBody))
	})

	var decoded googleBooksResponse
	err := fetchGoogleBooksVolumes(http.DefaultClient, "mitosis", 5, &decoded)
	if !errors.Is(err, errGoogleBooksPaused) {
		t.Fatalf("first call: got %v, want errGoogleBooksPaused", err)
	}

	want := time.Date(2026, 10, 7, 7, 0, 0, 0, time.UTC) // midnight PDT
	if got := googleBooks.PausedUntil(); !got.Equal(want) {
		t.Fatalf("paused until %s, want %s", got.UTC(), want)
	}
	if googleBooks.Notice() == "" {
		t.Fatal("expected a notice while paused")
	}

	if err := fetchGoogleBooksVolumes(http.DefaultClient, "mitosis", 5, &decoded); !errors.Is(err, errGoogleBooksPaused) {
		t.Fatalf("second call: got %v, want errGoogleBooksPaused", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hit %d times, want 1", got)
	}
}

func TestGoogleBooksResumesAfterPause(t *testing.T) {
	var hits atomic.Int32
	now := time.Date(2026, 10, 7, 3, 0, 0, 0, time.UTC)
	stubGoogleBooks(t, now, func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"Quota exceeded for limit 'Queries per minute'"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	})

	var decoded googleBooksResponse
	if err := fetchGoogleBooksVolumes(http.DefaultClient, "q", 5, &decoded); !errors.Is(err, errGoogleBooksPaused) {
		t.Fatalf("got %v, want errGoogleBooksPaused", err)
	}
	if got, want := googleBooks.PausedUntil(), now.Add(30*time.Second); !got.Equal(want) {
		t.Fatalf("paused until %s, want %s", got, want)
	}

	googleBooks.now = func() time.Time { return now.Add(31 * time.Second) }
	if err := fetchGoogleBooksVolumes(http.DefaultClient, "q", 5, &decoded); err != nil {
		t.Fatalf("after pause: %v", err)
	}
	if googleBooks.Notice() != "" {
		t.Fatal("notice should clear once the pause has passed")
	}
}

func TestGoogleBooksOtherErrorsDoNotPause(t *testing.T) {
	stubGoogleBooks(t, time.Now(), func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	var decoded googleBooksResponse
	err := fetchGoogleBooksVolumes(http.DefaultClient, "q", 5, &decoded)
	if err == nil || errors.Is(err, errGoogleBooksPaused) {
		t.Fatalf("got %v, want a plain upstream error", err)
	}
	if !googleBooks.PausedUntil().IsZero() {
		t.Fatal("a 500 must not pause lookups")
	}
}

func TestGoogleBooksAPIKeySentAsHeader(t *testing.T) {
	t.Setenv("GOOGLE_BOOKS_API_KEY", "secret-key")
	stubGoogleBooks(t, time.Now(), func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Goog-Api-Key"); got != "secret-key" {
			t.Errorf("X-Goog-Api-Key = %q, want secret-key", got)
		}
		if r.URL.Query().Has("key") {
			t.Error("api key must not be sent in the query string")
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	})

	var decoded googleBooksResponse
	if err := fetchGoogleBooksVolumes(http.DefaultClient, "q", 5, &decoded); err != nil {
		t.Fatal(err)
	}
}
