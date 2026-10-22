// SPDX-License-Identifier: MIT

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// googleBooksEndpoint is a variable so tests can point it at a local server.
var googleBooksEndpoint = "https://www.googleapis.com/books/v1/volumes"

// googleBooksShortPause is how long lookups stop after a rate-limit response
// that is not a daily quota and carries no Retry-After.
const googleBooksShortPause = time.Minute

// errGoogleBooksPaused is returned without contacting Google while lookups are
// paused after a quota or rate-limit response.
var errGoogleBooksPaused = errors.New("google books lookups are paused")

// googleBooksGate remembers that Google Books refused us for quota reasons, so
// we stop spending requests on answers we already know will be 429.
type googleBooksGate struct {
	mu          sync.Mutex
	pausedUntil time.Time
	now         func() time.Time
}

var googleBooks = &googleBooksGate{now: time.Now}

// PausedUntil reports when lookups resume, or the zero time if they are not paused.
func (g *googleBooksGate) PausedUntil() time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.pausedUntil.After(g.now()) {
		return time.Time{}
	}
	return g.pausedUntil
}

// Notice is a short human-readable explanation for API responses, or "" when
// Google Books is available.
func (g *googleBooksGate) Notice() string {
	until := g.PausedUntil()
	if until.IsZero() {
		return ""
	}
	return "Google Books quota exhausted; results exclude Google Books until " + until.UTC().Format("2006-01-02 15:04 MST") + "."
}

func (g *googleBooksGate) pause(until time.Time, reason string) {
	g.mu.Lock()
	extended := until.After(g.pausedUntil)
	if extended {
		g.pausedUntil = until
	}
	g.mu.Unlock()
	if extended {
		log.Printf("google books rate limited; pausing lookups until %s: %s", until.UTC().Format(time.RFC3339), reason)
	}
}

// fetchGoogleBooksVolumes runs one volumes query, honouring the pause gate and
// the optional GOOGLE_BOOKS_API_KEY.
func fetchGoogleBooksVolumes(client *http.Client, query string, maxResults int, target *googleBooksResponse) error {
	if !googleBooks.PausedUntil().IsZero() {
		return errGoogleBooksPaused
	}

	endpoint := googleBooksEndpoint + "?maxResults=" + strconv.Itoa(maxResults) + "&q=" + url.QueryEscape(query)
	var headers map[string]string
	// The key travels in a header rather than the query string so it cannot
	// end up in logged URLs.
	if key := strings.TrimSpace(os.Getenv("GOOGLE_BOOKS_API_KEY")); key != "" {
		headers = map[string]string{"X-Goog-Api-Key": key}
	}

	err := fetchJSONWithHeaders(client, endpoint, headers, target)
	var upstream *upstreamError
	if errors.As(err, &upstream) && upstream.isGoogleQuotaError() {
		reason := upstream.googleMessage()
		googleBooks.pause(googleBooksResumeTime(googleBooks.now(), upstream, reason), reason)
		return fmt.Errorf("%w: %s", errGoogleBooksPaused, reason)
	}
	return err
}

// googleBooksResumeTime decides how long to back off. Daily quotas reset at
// midnight Pacific time; anything else is a short-lived rate limit.
func googleBooksResumeTime(now time.Time, upstream *upstreamError, reason string) time.Time {
	if strings.Contains(strings.ToLower(reason), "per day") {
		if loc, err := time.LoadLocation("America/Los_Angeles"); err == nil {
			local := now.In(loc)
			return time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, loc)
		}
		return now.Add(time.Hour)
	}
	if upstream.RetryAfter > 0 {
		return now.Add(upstream.RetryAfter)
	}
	return now.Add(googleBooksShortPause)
}

// isGoogleQuotaError reports whether Google refused the request for quota or
// rate reasons. Those are 429 today, but have historically also been 403.
func (e *upstreamError) isGoogleQuotaError() bool {
	if e.Status == http.StatusTooManyRequests {
		return true
	}
	if e.Status != http.StatusForbidden {
		return false
	}
	body := strings.ToLower(e.Body)
	for _, marker := range []string{"ratelimitexceeded", "dailylimitexceeded", "quotaexceeded", "resource_exhausted"} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

// googleMessage extracts error.message from a Google API error body, falling
// back to the status when the body is not the expected JSON.
func (e *upstreamError) googleMessage() string {
	var decoded struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(e.Body), &decoded); err == nil {
		if msg := strings.TrimSpace(decoded.Error.Message); msg != "" {
			return msg
		}
	}
	return fmt.Sprintf("upstream returned %d", e.Status)
}
