// SPDX-License-Identifier: MIT

package web

import (
	"database/sql"
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

	"github.com/ab0oo/gopds/internal/database"
	"github.com/ab0oo/gopds/internal/normalize"
	"github.com/ab0oo/gopds/internal/scanner"
	"github.com/go-chi/chi/v5"
)

// enrichDefaultRateLimit is the pause between upstream lookups. Open Library
// and Google Books are free services being used by a hobby project; one request
// per second is deliberately gentle and keeps us far from any rate limit.
const enrichDefaultRateLimit = time.Second

// enrichStatus is the observable state of a background enrichment run.
type enrichStatus struct {
	Running     bool      `json:"running"`
	Phase       string    `json:"phase"`
	Message     string    `json:"message"`
	DryRun      bool      `json:"dry_run"`
	Processed   int       `json:"processed"`
	Total       int       `json:"total"`
	Applied     int       `json:"applied"`
	Queued      int       `json:"queued_for_review"`
	NoMatch     int       `json:"no_match"`
	Errors      int       `json:"errors"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	Error       string    `json:"error,omitempty"`
}

// enrichProposal records a single proposed change, for dry-run reporting and
// for the review queue.
type enrichProposal struct {
	BookID     int    `json:"book_id"`
	Field      string `json:"field"`
	Old        string `json:"old"`
	New        string `json:"new"`
	Source     string `json:"source"`
	Confidence string `json:"confidence"`
}

type enricher struct {
	mu        sync.Mutex
	status    enrichStatus
	proposals []enrichProposal
	cancel    chan struct{}
}

func (s *Server) enrichState() *enricher {
	s.enrichOnce.Do(func() {
		s.enricher = &enricher{}
	})
	return s.enricher
}

// HandleEnrichStart kicks off a background enrichment pass.
//
// It defaults to dry-run: callers must pass apply=true to write anything. That
// default is deliberate for a tool that mutates a library in bulk.
func (s *Server) HandleEnrichStart(w http.ResponseWriter, r *http.Request) {
	apply := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("apply")), "true")
	limit := parseIntDefault(r.URL.Query().Get("limit"), 100)
	if limit < 1 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}

	e := s.enrichState()
	e.mu.Lock()
	if e.status.Running {
		status := e.status
		e.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(status)
		return
	}
	e.status = enrichStatus{
		Running:   true,
		Phase:     "queued",
		Message:   "Enrichment queued.",
		DryRun:    !apply,
		StartedAt: time.Now().UTC(),
	}
	e.proposals = nil
	e.cancel = make(chan struct{})
	status := e.status
	cancel := e.cancel
	e.mu.Unlock()

	go s.runEnrichment(limit, apply, cancel)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(status)
}

// HandleEnrichStatus reports progress of the current or last enrichment run.
func (s *Server) HandleEnrichStatus(w http.ResponseWriter, r *http.Request) {
	e := s.enrichState()
	e.mu.Lock()
	status := e.status
	e.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// HandleEnrichProposals returns what the last run changed or would change.
func (s *Server) HandleEnrichProposals(w http.ResponseWriter, r *http.Request) {
	e := s.enrichState()
	e.mu.Lock()
	out := make([]enrichProposal, len(e.proposals))
	copy(out, e.proposals)
	dry := e.status.DryRun
	e.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		DryRun    bool             `json:"dry_run"`
		Count     int              `json:"count"`
		Proposals []enrichProposal `json:"proposals"`
	}{DryRun: dry, Count: len(out), Proposals: out})
}

// HandleEnrichStop asks a running enrichment pass to stop after the current book.
func (s *Server) HandleEnrichStop(w http.ResponseWriter, r *http.Request) {
	e := s.enrichState()
	e.mu.Lock()
	if e.status.Running && e.cancel != nil {
		close(e.cancel)
		e.cancel = nil
		e.status.Message = "Stopping after current book..."
	}
	status := e.status
	e.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func (s *Server) runEnrichment(limit int, apply bool, cancel chan struct{}) {
	e := s.enrichState()

	books, err := s.db.GetBooksNeedingWork(limit)
	if err != nil {
		e.finish(fmt.Sprintf("Failed to build work queue: %v", err))
		return
	}

	e.mu.Lock()
	e.status.Total = len(books)
	e.status.Phase = "running"
	e.status.Message = fmt.Sprintf("Processing %d books...", len(books))
	e.mu.Unlock()

	rate := enrichDefaultRateLimit
	if v := strings.TrimSpace(os.Getenv("ENRICH_RATE_MS")); v != "" {
		if ms, convErr := strconv.Atoi(v); convErr == nil && ms > 0 {
			rate = time.Duration(ms) * time.Millisecond
		}
	}
	client := &http.Client{Timeout: 15 * time.Second}
	ticker := time.NewTicker(rate)
	defer ticker.Stop()

	for _, book := range books {
		select {
		case <-cancel:
			e.finishOK("Stopped by request.")
			return
		case <-ticker.C:
		}

		s.enrichOneBook(client, book, apply)

		e.mu.Lock()
		e.status.Processed++
		e.mu.Unlock()
	}

	e.finishOK("Enrichment complete.")
}

// enrichOneBook looks up a single book and either applies a high-confidence
// match or records it for human review.
func (s *Server) enrichOneBook(client *http.Client, book database.Book, apply bool) {
	e := s.enrichState()

	bookPath, err := s.resolveBookPath(&book)
	if err != nil {
		e.bump(func(st *enrichStatus) { st.Errors++ })
		return
	}

	live, err := scanner.ExtractLiveMetadata(bookPath)
	if err != nil {
		e.bump(func(st *enrichStatus) { st.Errors++ })
		return
	}

	isbn := scanner.ISBNFromString(live.Identifier)
	local := normalize.MatchInput{
		Title:      book.Title,
		Author:     book.Author,
		Identifier: isbn,
	}

	best, bestConf := s.bestCandidate(client, local, isbn)
	if best == nil || bestConf == normalize.ConfidenceNone {
		e.bump(func(st *enrichStatus) { st.NoMatch++ })
		return
	}

	// Only fill gaps. Enrichment never overwrites metadata the book already has:
	// the local EPUB is treated as more authoritative than a remote guess.
	proposals := gapFillProposals(book, live, *best, bestConf)
	if len(proposals) == 0 {
		e.bump(func(st *enrichStatus) { st.NoMatch++ })
		return
	}

	e.mu.Lock()
	e.proposals = append(e.proposals, proposals...)
	e.mu.Unlock()

	if bestConf != normalize.ConfidenceExact {
		// Medium/low confidence is never auto-applied; it goes to the queue.
		e.bump(func(st *enrichStatus) { st.Queued++ })
		_ = s.db.SetEnrichmentReview(book.ID, "needs_review:"+bestConf.String())
		return
	}

	if !apply {
		e.bump(func(st *enrichStatus) { st.Applied++ }) // would-apply, dry run
		return
	}

	if err := s.applyEnrichment(book, proposals); err != nil {
		log.Printf("enrich apply failed for book %d: %v", book.ID, err)
		e.bump(func(st *enrichStatus) { st.Errors++ })
		return
	}
	e.bump(func(st *enrichStatus) { st.Applied++ })
}

// bestCandidate queries upstream sources and returns the strongest match.
func (s *Server) bestCandidate(client *http.Client, local normalize.MatchInput, isbn string) (*metadataCandidate, normalize.Confidence) {
	var results []metadataCandidate

	if isbn != "" {
		if c, err := s.fetchOpenLibraryByISBN(client, isbn); err == nil && c != nil {
			results = append(results, *c)
		}
		if gb, err := s.fetchGoogleBooks(client, "isbn:"+isbn, 3, "googlebooks:isbn"); err == nil {
			results = append(results, gb...)
		}
	}

	query := strings.TrimSpace(local.Title + " " + local.Author)
	if query != "" {
		if ol, err := s.searchOpenLibrary(client, query, 5); err == nil {
			results = append(results, ol...)
		}
		if gb, err := s.fetchGoogleBooks(client, query, 5, "googlebooks:search"); err == nil {
			results = append(results, gb...)
		}
	}

	var best *metadataCandidate
	bestConf := normalize.ConfidenceNone
	for i := range results {
		c := results[i]
		conf := normalize.Match(local, normalize.MatchCandidate{
			Title:      c.Title,
			Author:     c.Author,
			Identifier: scanner.ISBNFromString(c.Identifier),
		})
		if conf > bestConf {
			bestConf = conf
			best = &results[i]
		}
	}
	return best, bestConf
}

// gapFillProposals builds the list of fields that are empty locally and
// populated remotely. Non-empty local values are never proposed for change.
func gapFillProposals(book database.Book, live *scanner.EPUBMetadata, cand metadataCandidate, conf normalize.Confidence) []enrichProposal {
	var out []enrichProposal
	add := func(field, oldVal, newVal string) {
		oldVal = strings.TrimSpace(oldVal)
		newVal = strings.TrimSpace(newVal)
		if oldVal != "" || newVal == "" {
			return
		}
		out = append(out, enrichProposal{
			BookID:     book.ID,
			Field:      field,
			Old:        oldVal,
			New:        newVal,
			Source:     cand.Source,
			Confidence: conf.String(),
		})
	}

	add("description", firstNonEmptyStr(book.Description, live.Description), cand.Description)
	add("identifier", live.Identifier, cand.Identifier)
	add("publisher", live.Publisher, cand.Publisher)
	add("date", live.Date, cand.Date)
	add("language", live.Language, cand.Language)
	if len(live.Subjects) == 0 && len(cand.Subjects) > 0 {
		out = append(out, enrichProposal{
			BookID:     book.ID,
			Field:      "subjects",
			Old:        "",
			New:        strings.Join(cand.Subjects, ", "),
			Source:     cand.Source,
			Confidence: conf.String(),
		})
	}
	return out
}

func firstNonEmptyStr(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// applyEnrichment writes accepted proposals to the database cache only.
// EPUB files are intentionally left untouched by the automated pass.
func (s *Server) applyEnrichment(book database.Book, proposals []enrichProposal) error {
	description := book.Description
	for _, p := range proposals {
		if p.Field == "description" {
			description = p.New
		}
	}
	if strings.TrimSpace(description) == strings.TrimSpace(book.Description) {
		return nil
	}
	return s.db.UpdateBookMetadata(book.ID, book.Title, book.Author, description, book.ModTime)
}

func (e *enricher) bump(fn func(*enrichStatus)) {
	e.mu.Lock()
	fn(&e.status)
	e.mu.Unlock()
}

func (e *enricher) finishOK(msg string) {
	e.mu.Lock()
	e.status.Running = false
	e.status.Phase = "complete"
	e.status.Message = msg
	e.status.CompletedAt = time.Now().UTC()
	e.cancel = nil
	e.mu.Unlock()
}

func (e *enricher) finish(errMsg string) {
	e.mu.Lock()
	e.status.Running = false
	e.status.Phase = "failed"
	e.status.Message = "Enrichment failed."
	e.status.Error = errMsg
	e.status.CompletedAt = time.Now().UTC()
	e.cancel = nil
	e.mu.Unlock()
}

// lockRequest toggles the human-curated lock on a book.
type lockRequest struct {
	Locked bool `json:"locked"`
}

// HandleReviewQueue lists books needing human attention, worst score first.
func (s *Server) HandleReviewQueue(w http.ResponseWriter, r *http.Request) {
	flag := strings.TrimSpace(r.URL.Query().Get("flag"))
	limit := parseIntDefault(r.URL.Query().Get("limit"), 25)
	if limit < 1 {
		limit = 25
	}
	if limit > 200 {
		limit = 200
	}
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	if page < 1 {
		page = 1
	}

	items, total, err := s.db.GetReviewQueue(flag, limit, (page-1)*limit)
	if err != nil {
		log.Printf("review queue error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Total int                   `json:"total"`
		Page  int                   `json:"page"`
		Limit int                   `json:"limit"`
		Items []database.ReviewItem `json:"items"`
	}{Total: total, Page: page, Limit: limit, Items: items})
}

// HandleReviewResolve clears a book's review flags, optionally locking it so no
// future automated pass will touch it.
func (s *Server) HandleReviewResolve(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	bookID, err := strconv.Atoi(strings.TrimSpace(id))
	if err != nil {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}

	var req lockRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if err := s.db.ClearEnrichmentReview(bookID); err != nil {
		log.Printf("review resolve error for %d: %v", bookID, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if req.Locked {
		if err := s.db.SetEnrichmentLocked(bookID, true); err != nil {
			log.Printf("review lock error for %d: %v", bookID, err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		OK     bool `json:"ok"`
		BookID int  `json:"book_id"`
		Locked bool `json:"locked"`
	}{OK: true, BookID: bookID, Locked: req.Locked})
}

// HandleReviewLock sets or clears the curated lock without touching flags.
func (s *Server) HandleReviewLock(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	bookID, err := strconv.Atoi(strings.TrimSpace(id))
	if err != nil {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}

	var req lockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if err := s.db.SetEnrichmentLocked(bookID, req.Locked); err != nil {
		log.Printf("lock error for %d: %v", bookID, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		OK     bool `json:"ok"`
		BookID int  `json:"book_id"`
		Locked bool `json:"locked"`
	}{OK: true, BookID: bookID, Locked: req.Locked})
}

// coverUpgradeResult reports what a cover upgrade pass did for one book.
type coverUpgradeResult struct {
	BookID   int    `json:"book_id"`
	Title    string `json:"title"`
	OldSize  string `json:"old_size"`
	NewSize  string `json:"new_size"`
	Source   string `json:"source"`
	Applied  bool   `json:"applied"`
	Rejected string `json:"rejected,omitempty"`
}

// HandleCoverUpgrade finds better artwork online for books whose covers are
// missing or below bookstore quality.
//
// Like metadata enrichment this is dry-run by default, and it only ever
// replaces a cover with a strictly better one: a candidate that is not clearly
// an improvement is rejected rather than swapped in.
func (s *Server) HandleCoverUpgrade(w http.ResponseWriter, r *http.Request) {
	apply := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("apply")), "true")
	limit := parseIntDefault(r.URL.Query().Get("limit"), 25)
	if limit < 1 {
		limit = 25
	}
	if limit > 200 {
		limit = 200
	}

	items, _, err := s.db.GetReviewQueue("", 500, 0)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	client := &http.Client{Timeout: 20 * time.Second}
	results := make([]coverUpgradeResult, 0, limit)

	for _, item := range items {
		if len(results) >= limit {
			break
		}
		flags := "," + item.ReviewFlags + ","
		if !strings.Contains(flags, ","+normalize.ReviewThinCover+",") &&
			!strings.Contains(flags, ","+normalize.ReviewNoCover+",") &&
			!strings.Contains(flags, ","+normalize.ReviewBadCover+",") {
			continue
		}

		res := s.upgradeOneCover(client, item.Book, apply)
		if res != nil {
			results = append(results, *res)
		}
		time.Sleep(enrichDefaultRateLimit)
	}

	applied := 0
	for _, r := range results {
		if r.Applied {
			applied++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		DryRun   bool                 `json:"dry_run"`
		Examined int                  `json:"examined"`
		Applied  int                  `json:"applied"`
		Results  []coverUpgradeResult `json:"results"`
	}{DryRun: !apply, Examined: len(results), Applied: applied, Results: results})
}

func (s *Server) upgradeOneCover(client *http.Client, book database.Book, apply bool) *coverUpgradeResult {
	out := &coverUpgradeResult{BookID: book.ID, Title: book.Title}

	curW, curH := normalize.CoverDimensions(fmt.Sprintf("./data/covers/%d.jpg", book.ID))
	out.OldSize = fmt.Sprintf("%dx%d", curW, curH)
	curScore := normalize.CoverScore("cover.jpg", curW, curH)

	bookPath, err := s.resolveBookPath(&book)
	if err != nil {
		out.Rejected = "book file not found"
		return out
	}
	live, _ := scanner.ExtractLiveMetadata(bookPath)

	title, author, isbn := book.Title, book.Author, ""
	if live != nil {
		if strings.TrimSpace(live.Title) != "" {
			title = live.Title
		}
		if strings.TrimSpace(live.Author) != "" {
			author = live.Author
		}
		isbn = scanner.ISBNFromString(live.Identifier)
	}

	candidates := s.gatherOnlineCovers(client, book.ID, title, author, isbn)
	if len(candidates) == 0 {
		out.Rejected = "no online candidates"
		return out
	}

	best := candidates[0]
	bestScore := normalize.CoverScore("cover.jpg", best.Width, best.Height)
	out.NewSize = fmt.Sprintf("%dx%d", best.Width, best.Height)
	out.Source = best.Source

	// Require a clear improvement, not a lateral move.
	if bestScore <= curScore*11/10 {
		out.Rejected = "no clear improvement"
		return out
	}

	if !apply {
		return out
	}

	raw, err := fetchAllowedRemoteImage(best.ImageURL)
	if err != nil {
		out.Rejected = fmt.Sprintf("fetch failed: %v", err)
		return out
	}
	jpg, err := scanner.ConvertImageToJPEG(raw)
	if err != nil {
		out.Rejected = fmt.Sprintf("convert failed: %v", err)
		return out
	}
	if err := os.MkdirAll("./data/covers", 0755); err != nil {
		out.Rejected = "cache dir unavailable"
		return out
	}
	if err := os.WriteFile(fmt.Sprintf("./data/covers/%d.jpg", book.ID), jpg, 0644); err != nil {
		out.Rejected = "cache write failed"
		return out
	}

	out.Applied = true
	return out
}

// gatherOnlineCovers reuses the interactive cover search, returning candidates
// already filtered and ranked by the existing online-cover pipeline.
func (s *Server) gatherOnlineCovers(client *http.Client, bookID int, title, author, isbn string) []coverCandidate {
	candidates := make([]coverCandidate, 0, 8)
	seen := map[string]struct{}{}

	if isbn != "" {
		ol := fmt.Sprintf("https://covers.openlibrary.org/b/isbn/%s-L.jpg?default=false", url.PathEscape(isbn))
		if remoteImageReachable(client, ol) {
			candidates = append(candidates, makeRemoteCoverCandidate(ol, "Open Library ISBN "+isbn, "openlibrary"))
			seen[ol] = struct{}{}
		}
	}

	query := strings.TrimSpace(title + " " + author)
	if query != "" {
		if gb, err := fetchGoogleBookCoverCandidates(client, query, isbn, 6); err == nil {
			for _, c := range gb {
				if _, dup := seen[c.ImageURL]; dup {
					continue
				}
				seen[c.ImageURL] = struct{}{}
				candidates = append(candidates, c)
			}
		}
		if ol, err := fetchOpenLibrarySearchCoverCandidates(client, query, 6); err == nil {
			for _, c := range ol {
				if _, dup := seen[c.ImageURL]; dup {
					continue
				}
				seen[c.ImageURL] = struct{}{}
				candidates = append(candidates, c)
			}
		}
	}

	return rankAndFilterOnlineCovers(client, candidates)
}

// HandleReviewRescore re-evaluates a single book's quality after metadata or
// cover changes have been applied, and returns the fresh score so the review
// row can be updated in place.
func (s *Server) HandleReviewRescore(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	bookID, err := strconv.Atoi(strings.TrimSpace(id))
	if err != nil {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}

	book, err := s.db.GetBookByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Book not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// A locked book has been ruled on by a human; rescoring it would overwrite
	// that decision with an automated one.
	if existing, err := s.db.GetEnrichment(bookID); err == nil && existing != nil && existing.Locked {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			BookID  int    `json:"book_id"`
			Quality int    `json:"quality"`
			Flags   string `json:"review_flags"`
			Locked  bool   `json:"locked"`
			Skipped string `json:"skipped,omitempty"`
		}{
			BookID: bookID, Quality: existing.Quality, Flags: existing.ReviewFlags,
			Locked: true, Skipped: "book is locked",
		})
		return
	}

	bookPath, err := s.resolveBookPath(book)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to locate EPUB: %v", err), http.StatusUnprocessableEntity)
		return
	}

	q := scanner.RescoreBook(*book, bookPath)
	flags := normalize.FlagsString(q.Flags)
	if err := s.db.SaveEnrichmentScore(bookID, q.Score, flags); err != nil {
		log.Printf("rescore save failed for %d: %v", bookID, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		BookID  int    `json:"book_id"`
		Quality int    `json:"quality"`
		Flags   string `json:"review_flags"`
		Locked  bool   `json:"locked"`
	}{BookID: bookID, Quality: q.Score, Flags: flags, Locked: false})
}
