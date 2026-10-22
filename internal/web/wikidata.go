// SPDX-License-Identifier: MIT

package web

import (
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// wikidataDefaultPause is how long lookups stop after a 429 that gives no
// retry time.
const wikidataDefaultPause = time.Minute

// wikidataRetryInRe reads the wait from the body of the query service's 429
// page ("Too Many Requests - Please retry in 120 seconds."), for when the
// Retry-After header is missing.
var wikidataRetryInRe = regexp.MustCompile(`(?i)retry in (\d+) seconds`)

// wikidataPausedError reports that Wikidata asked us to slow down. Every
// Wikidata request fails with it until Until, without being sent: the query
// service bans clients that keep going after a 429.
type wikidataPausedError struct {
	Until time.Time
}

func (e *wikidataPausedError) Error() string {
	return "wikidata is rate limiting lookups until " + e.Until.UTC().Format(time.RFC3339)
}

// wikidataGate remembers a Wikidata 429 so later requests wait it out.
type wikidataGate struct {
	mu          sync.Mutex
	pausedUntil time.Time
	now         func() time.Time
}

var wikidata = &wikidataGate{now: time.Now}

// check returns a *wikidataPausedError while lookups are paused.
func (g *wikidataGate) check() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pausedUntil.After(g.now()) {
		return &wikidataPausedError{Until: g.pausedUntil}
	}
	return nil
}

func (g *wikidataGate) pause(wait time.Duration) error {
	g.mu.Lock()
	until := g.now().Add(wait)
	extended := until.After(g.pausedUntil)
	if extended {
		g.pausedUntil = until
	}
	until = g.pausedUntil
	g.mu.Unlock()
	if extended {
		log.Printf("wikidata rate limited; pausing lookups until %s", until.UTC().Format(time.RFC3339))
	}
	return &wikidataPausedError{Until: until}
}

// fetchWikidataJSON is fetchJSON for Wikidata endpoints, honouring the pause.
func fetchWikidataJSON(client *http.Client, endpoint string, target interface{}) error {
	if err := wikidata.check(); err != nil {
		return err
	}
	err := fetchJSON(client, endpoint, target)
	var upstream *upstreamError
	if errors.As(err, &upstream) && upstream.Status == http.StatusTooManyRequests {
		wait := upstream.RetryAfter
		if wait <= 0 {
			if m := wikidataRetryInRe.FindStringSubmatch(upstream.Body); m != nil {
				if secs, convErr := strconv.Atoi(m[1]); convErr == nil && secs > 0 {
					wait = time.Duration(secs) * time.Second
				}
			}
		}
		if wait <= 0 {
			wait = wikidataDefaultPause
		}
		return wikidata.pause(wait)
	}
	return err
}

// writeWikidataPaused answers 429 with a Retry-After when err is a Wikidata
// pause, so the UI can hold its lookup buttons until then. It reports whether
// it wrote the response.
func writeWikidataPaused(w http.ResponseWriter, err error) bool {
	var paused *wikidataPausedError
	if !errors.As(err, &paused) {
		return false
	}
	secs := int(math.Ceil(paused.Until.Sub(wikidata.now()).Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	http.Error(w, fmt.Sprintf("Wikidata is limiting requests. Try again in %d seconds.", secs), http.StatusTooManyRequests)
	return true
}
