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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ab0oo/gopds/internal/database"
	"github.com/ab0oo/gopds/internal/normalize"
	"github.com/ab0oo/gopds/internal/scanner"
	"github.com/go-chi/chi/v5"
)

// Series lookup works from one book outward: find the book on Wikidata, read
// which series it belongs to, list that series' volumes in order, then match
// those volumes against the local library. Nothing is written until the user
// confirms the proposal through HandleSeriesApply.

const maxSeriesApplyItems = 500

var wikidataIDRe = regexp.MustCompile(`^Q\d+$`)

// seriesLocalMatch is a library book believed to be one volume of a series.
type seriesLocalMatch struct {
	BookID      int    `json:"book_id"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	Series      string `json:"series"`
	SeriesIndex string `json:"series_index"`
}

// seriesEntry is one volume of a series as Wikidata lists it.
type seriesEntry struct {
	ID      string             `json:"id"`
	Title   string             `json:"title"`
	Index   string             `json:"index"`
	Matches []seriesLocalMatch `json:"matches"`
}

type seriesProposal struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Entries []seriesEntry `json:"entries"`
	Matched int           `json:"matched"`
}

type wikidataSearchResponse struct {
	Search []struct {
		ID string `json:"id"`
	} `json:"search"`
}

type sparqlResponse struct {
	Results struct {
		Bindings []map[string]struct {
			Value string `json:"value"`
		} `json:"bindings"`
	} `json:"results"`
}

func runSPARQL(client *http.Client, query string) ([]map[string]string, error) {
	endpoint := "https://query.wikidata.org/sparql?format=json&query=" + url.QueryEscape(query)
	var raw sparqlResponse
	if err := fetchJSON(client, endpoint, &raw); err != nil {
		return nil, err
	}
	rows := make([]map[string]string, 0, len(raw.Results.Bindings))
	for _, b := range raw.Results.Bindings {
		row := make(map[string]string, len(b))
		for k, v := range b {
			row[k] = strings.TrimPrefix(v.Value, "http://www.wikidata.org/entity/")
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// wikidataSeries is a series one specific Wikidata item belongs to.
type wikidataSeries struct {
	BookID string
	ID     string
	Name   string
	// AuthorVerified means Wikidata names an author for the item and it agrees
	// with the local book. Items with no author on record are unverified.
	AuthorVerified bool
}

// findWikidataSeries returns the series the given book belongs to.
//
// A title search alone is ambiguous ("Storm Front" is a Dresden Files novel
// and a Star Trek episode), so items whose recorded author disagrees with the
// local one are dropped, and unverified items are only offered when nothing
// was verified.
func findWikidataSeries(client *http.Client, title, author string) ([]wikidataSeries, error) {
	ids := make([]string, 0, 10)
	seenID := map[string]struct{}{}
	for _, q := range titleVariants(title) {
		endpoint := "https://www.wikidata.org/w/api.php?action=wbsearchentities&format=json&language=en&type=item&limit=10&search=" + url.QueryEscape(q)
		var res wikidataSearchResponse
		if err := fetchJSON(client, endpoint, &res); err != nil {
			return nil, err
		}
		for _, hit := range res.Search {
			if !wikidataIDRe.MatchString(hit.ID) {
				continue
			}
			if _, ok := seenID[hit.ID]; ok {
				continue
			}
			seenID[hit.ID] = struct{}{}
			ids = append(ids, "wd:"+hit.ID)
		}
		if len(ids) > 0 {
			break
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}

	rows, err := runSPARQL(client, `SELECT ?book ?series ?seriesLabel ?authorLabel WHERE {
  VALUES ?book { `+strings.Join(ids, " ")+` }
  ?book wdt:P179 ?series.
  OPTIONAL { ?book wdt:P50 ?author. }
  SERVICE wikibase:label { bd:serviceParam wikibase:language "en". }
}`)
	if err != nil {
		return nil, err
	}

	type key struct{ book, series string }
	type state struct {
		name                 string
		hasAuthor, authorHit bool
	}
	order := []key{}
	found := map[key]*state{}
	for _, row := range rows {
		k := key{row["book"], row["series"]}
		name := strings.TrimSpace(row["seriesLabel"])
		if k.series == "" || name == "" || wikidataIDRe.MatchString(name) {
			continue
		}
		st, ok := found[k]
		if !ok {
			st = &state{name: name}
			found[k] = st
			order = append(order, k)
		}
		if a := strings.TrimSpace(row["authorLabel"]); a != "" {
			st.hasAuthor = true
			if sameAuthor(author, a) {
				st.authorHit = true
			}
		}
	}

	var verified, unverified []wikidataSeries
	for _, k := range order {
		st := found[k]
		ws := wikidataSeries{BookID: k.book, ID: k.series, Name: st.name, AuthorVerified: st.authorHit}
		switch {
		case st.authorHit:
			verified = append(verified, ws)
		case !st.hasAuthor:
			unverified = append(unverified, ws)
		}
	}
	if len(verified) > 0 {
		return verified, nil
	}
	return unverified, nil
}

// fetchWikidataSeriesEntries lists the volumes of a series in reading order.
// Volumes Wikidata gives no position for sort last with an empty index.
func fetchWikidataSeriesEntries(client *http.Client, seriesID string) ([]seriesEntry, error) {
	if !wikidataIDRe.MatchString(seriesID) {
		return nil, fmt.Errorf("invalid series id %q", seriesID)
	}
	rows, err := runSPARQL(client, `SELECT ?m ?mLabel ?ord WHERE {
  ?m p:P179 ?st. ?st ps:P179 wd:`+seriesID+`.
  OPTIONAL { ?st pq:P1545 ?ord. }
  SERVICE wikibase:label { bd:serviceParam wikibase:language "en". }
} LIMIT 1000`)
	if err != nil {
		return nil, err
	}

	byID := map[string]int{}
	entries := make([]seriesEntry, 0, len(rows))
	for _, row := range rows {
		id, label, ord := row["m"], strings.TrimSpace(row["mLabel"]), strings.TrimSpace(row["ord"])
		// An item with no English label comes back labelled with its own id;
		// those are chapters and stubs, not books anyone could match.
		if id == "" || label == "" || wikidataIDRe.MatchString(label) {
			continue
		}
		if i, ok := byID[id]; ok {
			if entries[i].Index == "" {
				entries[i].Index = ord
			}
			continue
		}
		byID[id] = len(entries)
		entries = append(entries, seriesEntry{ID: id, Title: label, Index: ord, Matches: []seriesLocalMatch{}})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		a, aok := parseSeriesIndex(entries[i].Index)
		b, bok := parseSeriesIndex(entries[j].Index)
		if aok != bok {
			return aok
		}
		if aok && a != b {
			return a < b
		}
		return strings.ToLower(entries[i].Title) < strings.ToLower(entries[j].Title)
	})
	return entries, nil
}

func parseSeriesIndex(raw string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	return f, err == nil
}

// titleVariants returns the title plus shorter forms with a subtitle or
// trailing annotation removed, most specific first.
func titleVariants(title string) []string {
	title = strings.TrimSpace(title)
	out := []string{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		for _, have := range out {
			if have == v {
				return
			}
		}
		out = append(out, v)
	}
	add(title)
	if i := strings.Index(title, ":"); i > 0 {
		add(title[:i])
	}
	if i := strings.Index(title, "("); i > 0 {
		add(title[:i])
	}
	return out
}

// sameAuthor reports whether two author strings name the same person. It
// leans on normalize.Match with identical titles so only the author decides.
func sameAuthor(a, b string) bool {
	// Undo "Last, First" first: the matcher compares names token by token.
	a, b = normalize.Author(a).Value, normalize.Author(b).Value
	if a == "" || b == "" {
		return false
	}
	return normalize.Match(
		normalize.MatchInput{Title: "x", Author: a},
		normalize.MatchCandidate{Title: "x", Author: b},
	) == normalize.ConfidenceExact
}

// sameBookTitle reports whether a library title names a series volume,
// tolerating a subtitle present on only one side.
func sameBookTitle(local, volume string) bool {
	for _, l := range titleVariants(local) {
		for _, v := range titleVariants(volume) {
			if normalize.Match(
				normalize.MatchInput{Title: l, Author: "x"},
				normalize.MatchCandidate{Title: v, Author: "x"},
			) == normalize.ConfidenceExact {
				return true
			}
		}
	}
	return false
}

// matchSeriesEntries attaches library books to the series volumes they
// correspond to. Only books by the anchor book's author are considered, which
// keeps a common title by someone else out of the series. The anchor is pinned
// to its own Wikidata volume even if its local title would not have matched.
func matchSeriesEntries(entries []seriesEntry, books []database.Book, anchor database.Book, anchorVolumeID string) int {
	candidates := make([]database.Book, 0, 32)
	for _, b := range books {
		if b.ID != anchor.ID && sameAuthor(anchor.Author, b.Author) {
			candidates = append(candidates, b)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })

	toMatch := func(b database.Book) seriesLocalMatch {
		return seriesLocalMatch{BookID: b.ID, Title: b.Title, Author: b.Author, Series: b.Series, SeriesIndex: b.SeriesIndex}
	}

	anchorPlaced := false
	for i := range entries {
		if entries[i].ID == anchorVolumeID {
			entries[i].Matches = append(entries[i].Matches, toMatch(anchor))
			anchorPlaced = true
		}
	}

	matched := 0
	for i := range entries {
		if !anchorPlaced && sameBookTitle(anchor.Title, entries[i].Title) {
			entries[i].Matches = append(entries[i].Matches, toMatch(anchor))
			anchorPlaced = true
		}
		for _, b := range candidates {
			if sameBookTitle(b.Title, entries[i].Title) {
				entries[i].Matches = append(entries[i].Matches, toMatch(b))
			}
		}
		matched += len(entries[i].Matches)
	}
	return matched
}

// HandleSeriesLookup proposes series assignments for a book and its siblings.
// The optional title and author query parameters override the stored values,
// so unsaved corrections in the editor are used for the lookup.
func (s *Server) HandleSeriesLookup(w http.ResponseWriter, r *http.Request) {
	book, err := s.db.GetBookByID(chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Book not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	anchor := *book
	if v := strings.TrimSpace(r.URL.Query().Get("title")); v != "" {
		anchor.Title = v
	}
	if v := strings.TrimSpace(r.URL.Query().Get("author")); v != "" {
		anchor.Author = v
	}
	if strings.TrimSpace(anchor.Title) == "" {
		http.Error(w, "Book has no title to look up", http.StatusBadRequest)
		return
	}

	client := &http.Client{Timeout: 20 * time.Second}
	found, err := findWikidataSeries(client, anchor.Title, anchor.Author)
	if err != nil {
		log.Printf("[series] wikidata lookup error book_id=%d title=%q err=%v", book.ID, anchor.Title, err)
		http.Error(w, "Series lookup failed: Wikidata is unavailable", http.StatusBadGateway)
		return
	}

	books, err := s.db.GetAllBooks()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	proposals := make([]seriesProposal, 0, len(found))
	seenSeries := map[string]struct{}{}
	for _, ws := range found {
		if _, ok := seenSeries[ws.ID]; ok {
			continue
		}
		seenSeries[ws.ID] = struct{}{}
		entries, err := fetchWikidataSeriesEntries(client, ws.ID)
		if err != nil {
			log.Printf("[series] wikidata members error book_id=%d series=%s err=%v", book.ID, ws.ID, err)
			continue
		}
		matched := matchSeriesEntries(entries, books, anchor, ws.BookID)
		proposals = append(proposals, seriesProposal{ID: ws.ID, Name: ws.Name, Entries: entries, Matched: matched})
	}
	log.Printf("[series] lookup book_id=%d title=%q author=%q series=%d", book.ID, anchor.Title, anchor.Author, len(proposals))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		BookID int              `json:"book_id"`
		Series []seriesProposal `json:"series"`
	}{BookID: book.ID, Series: proposals})
}

type seriesApplyRequest struct {
	Series string `json:"series"`
	Items  []struct {
		BookID      int    `json:"book_id"`
		SeriesIndex string `json:"series_index"`
	} `json:"items"`
}

type seriesApplyResult struct {
	BookID      int    `json:"book_id"`
	OK          bool   `json:"ok"`
	Series      string `json:"series,omitempty"`
	SeriesIndex string `json:"series_index,omitempty"`
	Error       string `json:"error,omitempty"`
}

// HandleSeriesApply writes one series name, and a per-book index, into each
// listed EPUB and the DB cache. Books are independent: a failure on one is
// reported in its result and does not stop the rest.
func (s *Server) HandleSeriesApply(w http.ResponseWriter, r *http.Request) {
	var req seriesApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}
	req.Series = strings.TrimSpace(req.Series)
	if req.Series == "" {
		http.Error(w, "Series name cannot be empty", http.StatusBadRequest)
		return
	}
	if len(req.Items) == 0 {
		http.Error(w, "No books selected", http.StatusBadRequest)
		return
	}
	if len(req.Items) > maxSeriesApplyItems {
		http.Error(w, "Too many books in one request", http.StatusBadRequest)
		return
	}

	results := make([]seriesApplyResult, 0, len(req.Items))
	updated := 0
	for _, item := range req.Items {
		index := strings.TrimSpace(item.SeriesIndex)
		res := seriesApplyResult{BookID: item.BookID}
		if err := s.applySeriesToBook(item.BookID, req.Series, index); err != nil {
			res.Error = err.Error()
			log.Printf("[series] apply failed book_id=%d series=%q err=%v", item.BookID, req.Series, err)
		} else {
			res.OK = true
			res.Series = req.Series
			res.SeriesIndex = index
			updated++
		}
		results = append(results, res)
	}
	log.Printf("[series] applied series=%q updated=%d of %d", req.Series, updated, len(req.Items))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Series  string              `json:"series"`
		Updated int                 `json:"updated"`
		Results []seriesApplyResult `json:"results"`
	}{Series: req.Series, Updated: updated, Results: results})
}

func (s *Server) applySeriesToBook(bookID int, series, index string) error {
	book, err := s.db.GetBookByID(strconv.Itoa(bookID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("book not found")
		}
		return errors.New("database error")
	}
	bookPath, err := s.resolveBookPath(book)
	if err != nil {
		return fmt.Errorf("EPUB not found: %v", err)
	}
	if _, err := scanner.UpdateEPUBSeries(bookPath, series, index); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return errors.New("write permission denied for EPUB file")
		}
		return fmt.Errorf("failed to write EPUB: %v", err)
	}
	info, err := os.Stat(bookPath)
	if err != nil {
		return errors.New("EPUB written but its mod time could not be read")
	}
	if err := s.db.UpdateBookSeries(book.ID, series, index, info.ModTime()); err != nil {
		return errors.New("EPUB written but the DB cache was not updated")
	}
	return nil
}
