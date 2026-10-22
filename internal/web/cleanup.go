// SPDX-License-Identifier: MIT

package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ab0oo/gopds/internal/database"
	"github.com/ab0oo/gopds/internal/normalize"
	"github.com/ab0oo/gopds/internal/scanner"
	"github.com/go-chi/chi/v5"
)

// Series cleanup starts from one book and tidies every library book of its
// series: titles lose "Xanth 03 - " style prefixes and take Wikidata's
// spelling, the author is checked against Wikidata, and the series name and
// position are written into the EPUB. Like the series lookup, it only
// proposes; HandleSeriesCleanupApply writes what the user confirms.

// cleanupFields describes a book. Author is the display form; Authors is the
// full list as the EPUB holds (or should hold) it.
type cleanupFields struct {
	Title       string   `json:"title"`
	Author      string   `json:"author"`
	Authors     []string `json:"authors,omitempty"`
	Series      string   `json:"series"`
	SeriesIndex string   `json:"series_index"`
}

type cleanupItem struct {
	BookID   int           `json:"book_id"`
	Current  cleanupFields `json:"current"`
	Proposed cleanupFields `json:"proposed"`
	Changes  []string      `json:"changes"`
	// Verified means the book matched a Wikidata volume, so the proposed
	// title, author and position come from Wikidata rather than the title.
	Verified bool `json:"verified"`
	Locked   bool `json:"locked"`
}

type cleanupMissing struct {
	Index string `json:"index"`
	Title string `json:"title"`
}

type cleanupProposal struct {
	BookID    int              `json:"book_id"`
	Series    string           `json:"series"`
	Source    string           `json:"source"`
	Items     []cleanupItem    `json:"items"`
	Unchanged int              `json:"unchanged"`
	Missing   []cleanupMissing `json:"missing"`
}

// fetchWikidataVolumeAuthors maps each volume of a series to its authors.
func fetchWikidataVolumeAuthors(client *http.Client, seriesID string) (map[string][]string, error) {
	if !wikidataIDRe.MatchString(seriesID) {
		return nil, fmt.Errorf("invalid series id %q", seriesID)
	}
	rows, err := runSPARQL(client, `SELECT ?m ?authorLabel WHERE {
  ?m wdt:P179 wd:`+seriesID+`.
  ?m wdt:P50 ?author.
  SERVICE wikibase:label { bd:serviceParam wikibase:language "en". }
} LIMIT 2000`)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, row := range rows {
		name := strings.TrimSpace(row["authorLabel"])
		if row["m"] == "" || name == "" || wikidataIDRe.MatchString(name) {
			continue
		}
		dup := false
		for _, have := range out[row["m"]] {
			dup = dup || have == name
		}
		if !dup {
			out[row["m"]] = append(out[row["m"]], name)
		}
	}
	return out, nil
}

// seriesKey reduces a series name to what identifies it, so "The Chronicles
// of Amber" and "Chronicles of Amber Series" compare equal.
func seriesKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case unicode.IsSpace(r) || r == '-':
			b.WriteRune(' ')
		}
	}
	key := strings.Join(strings.Fields(b.String()), " ")
	key = strings.TrimPrefix(key, "the ")
	key = strings.TrimSuffix(key, " series")
	return key
}

func sameSeriesIndex(a, b string) bool {
	fa, aok := parseSeriesIndex(a)
	fb, bok := parseSeriesIndex(b)
	if aok && bok {
		return fa == fb
	}
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

// buildSeriesCleanup decides which library books belong to the series and
// what each should become. entries and authors come from Wikidata and may be
// empty, in which case only what the titles themselves say is used.
// readFile returns what the book's EPUB currently says, which is what the
// cleanup must change; the cache may already show a cleaned-up title. When it
// is nil or the file cannot be read, the cached values stand in.
//
// A book qualifies when it is the anchor, when it is by the anchor's author
// and either carries the series name or matches a volume, or when it carries
// the series name and matches a volume. The last case is how a book filed
// under the wrong author ("Piers A. Jacob") is found and corrected.
func buildSeriesCleanup(anchor database.Book, anchorVolumeID, seriesName string, entries []seriesEntry, authors map[string][]string, books []database.Book, readFile func(database.Book) *cleanupFields) cleanupProposal {
	anchorLocal := normalize.Title(anchor.Title, anchor.Series, anchor.SeriesIndex)
	if strings.TrimSpace(seriesName) == "" {
		seriesName = anchorLocal.Series
	}
	keys := map[string]bool{}
	for _, name := range []string{seriesName, anchorLocal.Series} {
		if k := seriesKey(name); k != "" {
			keys[k] = true
		}
	}

	prop := cleanupProposal{BookID: anchor.ID, Series: seriesName, Items: []cleanupItem{}, Missing: []cleanupMissing{}}
	matchedEntry := map[string]bool{}

	for _, b := range books {
		if b.ID == anchor.ID {
			b = anchor // carries any unsaved title/author from the editor
		}
		local := normalize.Title(b.Title, b.Series, b.SeriesIndex)
		inSeries := keys[seriesKey(local.Series)]

		var entry *seriesEntry
		for i := range entries {
			if b.ID == anchor.ID && anchorVolumeID != "" {
				if entries[i].ID == anchorVolumeID {
					entry = &entries[i]
					break
				}
				continue
			}
			if sameBookTitle(b.Title, entries[i].Title) {
				entry = &entries[i]
				break
			}
		}

		byAuthor := sameAuthor(anchor.Author, b.Author)
		if b.ID != anchor.ID && !(byAuthor && (inSeries || entry != nil)) && !(inSeries && entry != nil) {
			continue
		}

		inFile := cleanupFields{Title: b.Title, Series: b.Series, SeriesIndex: b.SeriesIndex}
		if readFile != nil {
			if f := readFile(b); f != nil && strings.TrimSpace(f.Title) != "" {
				inFile = *f
			}
		}
		current := inFile.Authors
		if len(current) == 0 && strings.TrimSpace(b.Author) != "" {
			current = []string{strings.TrimSpace(b.Author)}
		}
		proposedAuthors := make([]string, 0, len(current))
		for _, a := range current {
			if v := normalize.Author(a).Value; v != "" {
				proposedAuthors = append(proposedAuthors, v)
			} else {
				proposedAuthors = append(proposedAuthors, a)
			}
		}

		proposed := cleanupFields{
			Title:       local.Title,
			Series:      seriesName,
			SeriesIndex: local.SeriesIndex,
		}
		if entry != nil {
			matchedEntry[entry.ID] = true
			proposed.Title = entry.Title
			if strings.TrimSpace(entry.Index) != "" {
				proposed.SeriesIndex = entry.Index
			}
			// Wikidata's author list wins only when the book names someone
			// Wikidata does not; a co-author missing locally is left alone.
			if volAuthors := authors[entry.ID]; len(volAuthors) > 0 {
				for _, a := range current {
					known := false
					for _, v := range volAuthors {
						known = known || samePerson(a, v)
					}
					if !known {
						proposedAuthors = append([]string(nil), volAuthors...)
						break
					}
				}
			}
		}
		proposed.Authors = proposedAuthors
		proposed.Author = strings.Join(proposedAuthors, " & ")

		cur := cleanupFields{
			Title:       inFile.Title,
			Author:      strings.Join(current, " & "),
			Authors:     current,
			Series:      inFile.Series,
			SeriesIndex: inFile.SeriesIndex,
		}
		changes := []string{}
		if proposed.Title != strings.TrimSpace(cur.Title) {
			changes = append(changes, "title")
		}
		// The cache can disagree with a correct file (older scans kept the
		// last author); rewriting the same list refreshes it.
		cacheStale := len(proposedAuthors) > 0 && strings.TrimSpace(b.Author) != proposedAuthors[0]
		if len(proposedAuthors) > 0 && (!equalStrings(proposedAuthors, current) || cacheStale) {
			if equalStrings(proposedAuthors, current) {
				cur.Author = strings.TrimSpace(b.Author)
			}
			changes = append(changes, "author")
		}
		if proposed.Series != "" && proposed.Series != strings.TrimSpace(cur.Series) {
			changes = append(changes, "series")
		}
		if proposed.SeriesIndex != "" && !sameSeriesIndex(proposed.SeriesIndex, cur.SeriesIndex) {
			changes = append(changes, "series_index")
		}
		if len(changes) == 0 {
			prop.Unchanged++
			continue
		}
		prop.Items = append(prop.Items, cleanupItem{
			BookID:   b.ID,
			Current:  cur,
			Proposed: proposed,
			Changes:  changes,
			Verified: entry != nil,
		})
	}

	for _, e := range entries {
		if !matchedEntry[e.ID] {
			prop.Missing = append(prop.Missing, cleanupMissing{Index: e.Index, Title: e.Title})
		}
	}

	sortCleanupItems(prop.Items)
	return prop
}

// samePerson is sameAuthor loosened to ignore middle names and initials, so
// "Robert Margroff" counts as "Robert E. Margroff". It only decides whether a
// local name is already acceptable, so erring loose just means fewer changes.
func samePerson(a, b string) bool {
	if sameAuthor(a, b) {
		return true
	}
	ta := strings.Fields(strings.ToLower(normalize.Author(a).Value))
	tb := strings.Fields(strings.ToLower(normalize.Author(b).Value))
	if len(ta) < 2 || len(tb) < 2 {
		return false
	}
	return ta[0] == tb[0] && ta[len(ta)-1] == tb[len(tb)-1]
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimSpace(a[i]) != strings.TrimSpace(b[i]) {
			return false
		}
	}
	return true
}

// sortCleanupItems orders items by proposed position, unnumbered ones last.
func sortCleanupItems(items []cleanupItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, aok := parseSeriesIndex(items[i].Proposed.SeriesIndex)
		b, bok := parseSeriesIndex(items[j].Proposed.SeriesIndex)
		if aok != bok {
			return aok
		}
		if aok && a != b {
			return a < b
		}
		return items[i].BookID < items[j].BookID
	})
}

// HandleSeriesCleanup proposes cleanups for a book's whole series. Like the
// series lookup, title and author query parameters override stored values.
func (s *Server) HandleSeriesCleanup(w http.ResponseWriter, r *http.Request) {
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

	books, err := s.db.GetAllBooks()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	client := &http.Client{Timeout: 20 * time.Second}
	anchorLocal := normalize.Title(anchor.Title, anchor.Series, anchor.SeriesIndex)
	source := "titles"
	var seriesName, anchorVolumeID string
	var entries []seriesEntry
	var authors map[string][]string

	found, err := findWikidataSeries(client, anchor.Title, anchor.Author)
	if err != nil {
		// Still useful without Wikidata: the titles alone can be cleaned.
		log.Printf("[cleanup] wikidata lookup error book_id=%d title=%q err=%v", book.ID, anchor.Title, err)
	}
	if ws := pickCleanupSeries(found, anchorLocal.Series); ws != nil {
		if e, err := fetchWikidataSeriesEntries(client, ws.ID); err == nil {
			entries = e
			seriesName, anchorVolumeID, source = tidySeriesName(ws.Name), ws.BookID, "wikidata"
			if a, err := fetchWikidataVolumeAuthors(client, ws.ID); err == nil {
				authors = a
			} else {
				log.Printf("[cleanup] wikidata authors error series=%s err=%v", ws.ID, err)
			}
		} else {
			log.Printf("[cleanup] wikidata members error series=%s err=%v", ws.ID, err)
		}
	}
	if seriesName == "" && anchorLocal.Series == "" {
		http.Error(w, "No series found: Wikidata lists none for this book and its title does not name one", http.StatusNotFound)
		return
	}

	prop := buildSeriesCleanup(anchor, anchorVolumeID, seriesName, entries, authors, books, s.readCleanupFields)
	prop.Source = source
	for i := range prop.Items {
		if e, err := s.db.GetEnrichment(prop.Items[i].BookID); err == nil && e != nil {
			prop.Items[i].Locked = e.Locked
		}
	}
	log.Printf("[cleanup] proposal book_id=%d series=%q source=%s items=%d unchanged=%d",
		book.ID, prop.Series, source, len(prop.Items), prop.Unchanged)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(prop)
}

// tidySeriesName drops the " series" Wikidata often appends to a label
// ("Tarot series"); readers already show the name as a series.
func tidySeriesName(name string) string {
	name = strings.TrimSpace(name)
	if len(name) > len(" series") && strings.EqualFold(name[len(name)-len(" series"):], " series") {
		return strings.TrimSpace(name[:len(name)-len(" series")])
	}
	return name
}

// readCleanupFields reads a book's identity from its EPUB, or nil if the file
// cannot be read.
func (s *Server) readCleanupFields(b database.Book) *cleanupFields {
	path, err := s.resolveBookPath(&b)
	if err != nil {
		return nil
	}
	meta, err := scanner.ExtractLiveMetadata(path)
	if err != nil {
		return nil
	}
	authors, err := scanner.ReadEPUBAuthors(path)
	if err != nil {
		return nil
	}
	return &cleanupFields{
		Title:       strings.TrimSpace(meta.Title),
		Authors:     authors,
		Series:      strings.TrimSpace(meta.Series),
		SeriesIndex: strings.TrimSpace(meta.SeriesIndex),
	}
}

// pickCleanupSeries prefers the Wikidata series whose name agrees with the
// series in the book's own title, since a book can sit in several (a trilogy
// inside a larger cycle).
func pickCleanupSeries(found []wikidataSeries, localSeries string) *wikidataSeries {
	if len(found) == 0 {
		return nil
	}
	if k := seriesKey(localSeries); k != "" {
		for i := range found {
			if seriesKey(found[i].Name) == k {
				return &found[i]
			}
		}
	}
	return &found[0]
}

type seriesCleanupApplyRequest struct {
	Items []struct {
		BookID int `json:"book_id"`
		cleanupFields
	} `json:"items"`
}

type seriesCleanupResult struct {
	BookID int           `json:"book_id"`
	OK     bool          `json:"ok"`
	Book   cleanupFields `json:"book"`
	Error  string        `json:"error,omitempty"`
}

// HandleSeriesCleanupApply writes confirmed cleanups. Only non-empty fields
// are written, so the client sends just the fields it means to change. Books
// are independent: one failure does not stop the rest.
func (s *Server) HandleSeriesCleanupApply(w http.ResponseWriter, r *http.Request) {
	var req seriesCleanupApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
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

	results := make([]seriesCleanupResult, 0, len(req.Items))
	updated := 0
	for _, item := range req.Items {
		res := seriesCleanupResult{BookID: item.BookID}
		book, err := s.applyCleanupToBook(item.BookID, scanner.CleanupUpdate{
			Title:       strings.TrimSpace(item.Title),
			Authors:     item.Authors,
			Series:      strings.TrimSpace(item.Series),
			SeriesIndex: strings.TrimSpace(item.SeriesIndex),
		})
		if err != nil {
			res.Error = err.Error()
			log.Printf("[cleanup] apply failed book_id=%d err=%v", item.BookID, err)
		} else {
			res.OK = true
			res.Book = book
			updated++
		}
		results = append(results, res)
	}
	log.Printf("[cleanup] applied updated=%d of %d", updated, len(req.Items))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Updated int                   `json:"updated"`
		Results []seriesCleanupResult `json:"results"`
	}{Updated: updated, Results: results})
}

func (s *Server) applyCleanupToBook(bookID int, update scanner.CleanupUpdate) (cleanupFields, error) {
	if update.Title == "" && len(update.Authors) == 0 && update.Series == "" && update.SeriesIndex == "" {
		return cleanupFields{}, errors.New("nothing to change")
	}
	book, err := s.db.GetBookByID(strconv.Itoa(bookID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return cleanupFields{}, errors.New("book not found")
		}
		return cleanupFields{}, errors.New("database error")
	}
	bookPath, err := s.resolveBookPath(book)
	if err != nil {
		return cleanupFields{}, fmt.Errorf("EPUB not found: %v", err)
	}
	meta, err := scanner.UpdateEPUBCleanup(bookPath, update)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return cleanupFields{}, errors.New("write permission denied for EPUB file")
		}
		return cleanupFields{}, fmt.Errorf("failed to write EPUB: %v", err)
	}
	info, err := os.Stat(bookPath)
	if err != nil {
		return cleanupFields{}, errors.New("EPUB written but its mod time could not be read")
	}

	// Cache what the scanner would derive from the rewritten file, so the next
	// rescan finds nothing to change.
	title, author, description := book.Title, book.Author, book.Description
	series, index := book.Series, book.SeriesIndex
	if meta != nil {
		t := normalize.Title(meta.Title, meta.Series, meta.SeriesIndex)
		title, series, index = t.Title, t.Series, t.SeriesIndex
		if a := normalize.Author(meta.Author).Value; a != "" {
			author = a
		}
		description = strings.TrimSpace(meta.Description)
	}
	if err := s.db.UpdateBookMetadata(book.ID, title, author, description, info.ModTime()); err != nil {
		return cleanupFields{}, errors.New("EPUB written but the DB cache was not updated")
	}
	if err := s.db.UpdateBookSeries(book.ID, series, index, info.ModTime()); err != nil {
		return cleanupFields{}, errors.New("EPUB written but the DB cache was not updated")
	}
	return cleanupFields{Title: title, Author: author, Series: series, SeriesIndex: index}, nil
}
