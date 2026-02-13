package web

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ab0oo/gopds/internal/database"
	"github.com/ab0oo/gopds/internal/scanner"
	"github.com/go-chi/chi/middleware"
	"github.com/go-chi/chi/v5"
)

type Server struct {
	db   *database.DB
	uiFS embed.FS
}

type metadataRequest struct {
	Title       string   `json:"title"`
	Author      string   `json:"author"`
	Language    string   `json:"language"`
	Identifier  string   `json:"identifier"`
	Publisher   string   `json:"publisher"`
	Date        string   `json:"date"`
	Description string   `json:"description"`
	Subjects    []string `json:"subjects"`
	Series      string   `json:"series"`
	SeriesIndex string   `json:"series_index"`
}

type metadataCandidate struct {
	Source      string   `json:"source"`
	Title       string   `json:"title"`
	Author      string   `json:"author"`
	Language    string   `json:"language"`
	Identifier  string   `json:"identifier"`
	Publisher   string   `json:"publisher"`
	Date        string   `json:"date"`
	Description string   `json:"description"`
	Subjects    []string `json:"subjects"`
	Series      string   `json:"series"`
	SeriesIndex string   `json:"series_index"`
	Key         string   `json:"key"`
}

type metadataSearchPayload struct {
	NumFound int                 `json:"num_found"`
	Query    string              `json:"query"`
	Results  []metadataCandidate `json:"results"`
}

type openLibrarySearchResponse struct {
	NumFound int `json:"numFound"`
	Docs     []struct {
		Key              string   `json:"key"`
		Title            string   `json:"title"`
		AuthorName       []string `json:"author_name"`
		Language         []string `json:"language"`
		ISBN             []string `json:"isbn"`
		Publisher        []string `json:"publisher"`
		FirstPublishYear int      `json:"first_publish_year"`
		Subject          []string `json:"subject"`
	} `json:"docs"`
}

type openLibraryEditionResponse struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	PublishDate string   `json:"publish_date"`
	Publishers  []string `json:"publishers"`
	ISBN10      []string `json:"isbn_10"`
	ISBN13      []string `json:"isbn_13"`
	Subjects    []string `json:"subjects"`
	ByStatement string   `json:"by_statement"`
	Description flexText `json:"description"`
	Works       []struct {
		Key string `json:"key"`
	} `json:"works"`
	Languages []openLibraryKeyRef `json:"languages"`
}

type openLibraryKeyRef struct {
	Key string `json:"key"`
}

type openLibraryWorkResponse struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Description flexText `json:"description"`
	Subjects    []string `json:"subjects"`
}

type googleBooksResponse struct {
	Items []struct {
		ID         string `json:"id"`
		VolumeInfo struct {
			Title               string   `json:"title"`
			Authors             []string `json:"authors"`
			Publisher           string   `json:"publisher"`
			PublishedDate       string   `json:"publishedDate"`
			Description         string   `json:"description"`
			Language            string   `json:"language"`
			Categories          []string `json:"categories"`
			IndustryIdentifiers []struct {
				Type       string `json:"type"`
				Identifier string `json:"identifier"`
			} `json:"industryIdentifiers"`
		} `json:"volumeInfo"`
	} `json:"items"`
}

type flexText struct {
	Value string
}

func (f *flexText) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		f.Value = strings.TrimSpace(s)
		return nil
	}
	var obj struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		f.Value = strings.TrimSpace(obj.Value)
		return nil
	}
	f.Value = ""
	return nil
}

func NewServer(db *database.DB, uiFS embed.FS) *Server {
	return &Server{db: db, uiFS: uiFS}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	publicFS, err := fs.Sub(s.uiFS, "web/ui")
	if err != nil {
		log.Fatalf("FS Sub Error: %v", err)
	}

	r.Get("/opds", s.HandleCatalog)
	r.Get("/api/books", s.HandleBooksJSON)
	r.Get("/api/books/{id}/metadata/live", s.HandleLiveMetadata)
	r.Put("/api/books/{id}/metadata", s.HandleUpdateMetadata)
	r.Get("/api/openlibrary/search", s.HandleOpenLibrarySearch)
	r.Get("/covers/{id}.jpg", s.HandleCover)
	r.Get("/download/{id}", s.HandleDownload)

	r.Handle("/*", http.FileServer(http.FS(publicFS)))
	return r
}

func (s *Server) HandleCatalog(w http.ResponseWriter, r *http.Request) {
	books, err := s.db.GetAllBooks()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><feed xmlns="http://www.w3.org/2005/Atom"><title>GoPDS Library</title>`)
	for _, b := range books {
		safeTitle := html.EscapeString(b.Title)
		safeAuthor := html.EscapeString(b.Author)
		fmt.Fprintf(w, `
    <entry>
        <title>%s</title>
        <id>%d</id>
        <author><name>%s</name></author>
        <link rel="http://opds-spec.org/image" href="/covers/%d.jpg" type="image/jpeg"/>
        <link rel="http://opds-spec.org/acquisition" href="/download/%d" type="application/epub+zip"/>
    </entry>`, safeTitle, b.ID, safeAuthor, b.ID, b.ID)
	}
	fmt.Fprint(w, `</feed>`)
}

func (s *Server) HandleBooksJSON(w http.ResponseWriter, r *http.Request) {
	books, err := s.db.GetAllBooks()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(books); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (s *Server) HandleLiveMetadata(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	book, err := s.db.GetBookByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Book not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	meta, err := scanner.ExtractLiveMetadata(book.Path)
	if err != nil {
		http.Error(w, "Failed to read EPUB metadata", http.StatusUnprocessableEntity)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(meta)
}

func (s *Server) HandleOpenLibrarySearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	isbn := normalizeISBN(r.URL.Query().Get("isbn"))

	if q == "" {
		title := strings.TrimSpace(r.URL.Query().Get("title"))
		author := strings.TrimSpace(r.URL.Query().Get("author"))
		q = strings.TrimSpace(strings.Join([]string{title, author}, " "))
	}

	if q == "" && isbn == "" {
		http.Error(w, "Query or ISBN is required", http.StatusBadRequest)
		return
	}

	client := &http.Client{Timeout: 12 * time.Second}
	results := make([]metadataCandidate, 0, 20)

	if isbn != "" {
		if olByISBN, err := s.fetchOpenLibraryByISBN(client, isbn); err == nil && olByISBN != nil {
			results = append(results, *olByISBN)
		} else if err != nil {
			log.Printf("open library isbn lookup failed (%s): %v", isbn, err)
		}

		gbByISBN, err := s.fetchGoogleBooks(client, "isbn:"+isbn, 4, "googlebooks:isbn")
		if err == nil {
			results = append(results, gbByISBN...)
		} else {
			log.Printf("google books isbn lookup failed (%s): %v", isbn, err)
		}
	}

	if q != "" {
		olSearch, err := s.searchOpenLibrary(client, q, 8)
		if err == nil {
			results = append(results, olSearch...)
		} else {
			log.Printf("open library search failed (%s): %v", q, err)
		}

		gbSearch, err := s.fetchGoogleBooks(client, q, 6, "googlebooks:search")
		if err == nil {
			results = append(results, gbSearch...)
		} else {
			log.Printf("google books search failed (%s): %v", q, err)
		}
	}

	results = dedupeAndMergeCandidates(results)
	if len(results) > 20 {
		results = results[:20]
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metadataSearchPayload{
		NumFound: len(results),
		Query:    q,
		Results:  results,
	})
}

func (s *Server) searchOpenLibrary(client *http.Client, q string, limit int) ([]metadataCandidate, error) {
	if limit <= 0 {
		limit = 8
	}
	openLibraryURL := "https://openlibrary.org/search.json?limit=" + strconv.Itoa(limit) + "&q=" + url.QueryEscape(q)

	var decoded openLibrarySearchResponse
	if err := fetchJSON(client, openLibraryURL, &decoded); err != nil {
		return nil, err
	}

	results := make([]metadataCandidate, 0, len(decoded.Docs))
	for _, d := range decoded.Docs {
		pubYear := ""
		if d.FirstPublishYear > 0 {
			pubYear = strconv.Itoa(d.FirstPublishYear)
		}
		subjects := uniqueClean(d.Subject)
		if len(subjects) > 12 {
			subjects = subjects[:12]
		}

		candidate := metadataCandidate{
			Source:      "openlibrary:search",
			Title:       strings.TrimSpace(d.Title),
			Author:      firstNonEmpty(d.AuthorName),
			Language:    firstLanguageCode(d.Language),
			Identifier:  normalizeISBN(firstNonEmpty(d.ISBN)),
			Publisher:   firstNonEmpty(d.Publisher),
			Date:        pubYear,
			Description: "",
			Subjects:    subjects,
			Series:      "",
			SeriesIndex: "",
			Key:         d.Key,
		}

		if strings.TrimSpace(d.Key) != "" {
			if work, err := s.fetchOpenLibraryWork(client, d.Key); err == nil && work != nil {
				if strings.TrimSpace(candidate.Description) == "" {
					candidate.Description = strings.TrimSpace(work.Description.Value)
				}
				if len(candidate.Subjects) == 0 {
					candidate.Subjects = uniqueClean(work.Subjects)
				}
			}
		}

		results = append(results, candidate)
	}
	return results, nil
}

func (s *Server) fetchOpenLibraryByISBN(client *http.Client, isbn string) (*metadataCandidate, error) {
	isbn = normalizeISBN(isbn)
	if isbn == "" {
		return nil, fmt.Errorf("invalid isbn")
	}

	editionURL := "https://openlibrary.org/isbn/" + url.PathEscape(isbn) + ".json"
	var edition openLibraryEditionResponse
	if err := fetchJSON(client, editionURL, &edition); err != nil {
		return nil, err
	}

	candidate := &metadataCandidate{
		Source:      "openlibrary:isbn",
		Title:       strings.TrimSpace(edition.Title),
		Author:      strings.TrimSpace(edition.ByStatement),
		Language:    languageFromEdition(edition.Languages),
		Identifier:  pickISBN(edition.ISBN13, edition.ISBN10, isbn),
		Publisher:   firstNonEmpty(edition.Publishers),
		Date:        strings.TrimSpace(edition.PublishDate),
		Description: strings.TrimSpace(edition.Description.Value),
		Subjects:    uniqueClean(edition.Subjects),
		Series:      "",
		SeriesIndex: "",
		Key:         strings.TrimSpace(edition.Key),
	}

	if len(edition.Works) > 0 {
		if work, err := s.fetchOpenLibraryWork(client, edition.Works[0].Key); err == nil && work != nil {
			if strings.TrimSpace(candidate.Title) == "" {
				candidate.Title = strings.TrimSpace(work.Title)
			}
			if strings.TrimSpace(candidate.Description) == "" {
				candidate.Description = strings.TrimSpace(work.Description.Value)
			}
			candidate.Subjects = mergeSubjects(candidate.Subjects, work.Subjects)
		}
	}

	return candidate, nil
}

func (s *Server) fetchOpenLibraryWork(client *http.Client, workKey string) (*openLibraryWorkResponse, error) {
	workKey = strings.TrimSpace(workKey)
	if workKey == "" {
		return nil, fmt.Errorf("empty work key")
	}
	if !strings.HasPrefix(workKey, "/works/") {
		if strings.HasPrefix(workKey, "works/") {
			workKey = "/" + workKey
		} else {
			workKey = "/works/" + strings.TrimPrefix(workKey, "/")
		}
	}

	workURL := "https://openlibrary.org" + workKey + ".json"
	var work openLibraryWorkResponse
	if err := fetchJSON(client, workURL, &work); err != nil {
		return nil, err
	}
	return &work, nil
}

func (s *Server) fetchGoogleBooks(client *http.Client, query string, maxResults int, source string) ([]metadataCandidate, error) {
	if maxResults <= 0 {
		maxResults = 6
	}
	googleURL := "https://www.googleapis.com/books/v1/volumes?maxResults=" + strconv.Itoa(maxResults) + "&q=" + url.QueryEscape(query)

	var decoded googleBooksResponse
	if err := fetchJSON(client, googleURL, &decoded); err != nil {
		return nil, err
	}

	results := make([]metadataCandidate, 0, len(decoded.Items))
	for _, item := range decoded.Items {
		identifier := ""
		for _, ident := range item.VolumeInfo.IndustryIdentifiers {
			if strings.EqualFold(ident.Type, "ISBN_13") {
				identifier = normalizeISBN(ident.Identifier)
				break
			}
		}
		if identifier == "" {
			for _, ident := range item.VolumeInfo.IndustryIdentifiers {
				if strings.EqualFold(ident.Type, "ISBN_10") {
					identifier = normalizeISBN(ident.Identifier)
					break
				}
			}
		}
		if identifier == "" {
			for _, ident := range item.VolumeInfo.IndustryIdentifiers {
				if strings.TrimSpace(ident.Identifier) != "" {
					identifier = strings.TrimSpace(ident.Identifier)
					break
				}
			}
		}

		results = append(results, metadataCandidate{
			Source:      source,
			Title:       strings.TrimSpace(item.VolumeInfo.Title),
			Author:      firstNonEmpty(item.VolumeInfo.Authors),
			Language:    strings.TrimSpace(item.VolumeInfo.Language),
			Identifier:  identifier,
			Publisher:   strings.TrimSpace(item.VolumeInfo.Publisher),
			Date:        strings.TrimSpace(item.VolumeInfo.PublishedDate),
			Description: strings.TrimSpace(item.VolumeInfo.Description),
			Subjects:    uniqueClean(item.VolumeInfo.Categories),
			Series:      "",
			SeriesIndex: "",
			Key:         strings.TrimSpace(item.ID),
		})
	}

	return results, nil
}

func fetchJSON(client *http.Client, endpoint string, target interface{}) error {
	resp, err := client.Get(endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = "upstream returned an error"
		}
		return fmt.Errorf("%s (%d)", msg, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return err
	}
	return nil
}

func dedupeAndMergeCandidates(in []metadataCandidate) []metadataCandidate {
	if len(in) == 0 {
		return in
	}
	out := make([]metadataCandidate, 0, len(in))
	index := make(map[string]int)

	for _, c := range in {
		key := dedupeKey(c)
		if i, ok := index[key]; ok {
			out[i] = mergeCandidates(out[i], c)
			continue
		}
		index[key] = len(out)
		out = append(out, c)
	}
	return out
}

func dedupeKey(c metadataCandidate) string {
	identifier := normalizeISBN(c.Identifier)
	if identifier == "" {
		identifier = strings.ToLower(strings.TrimSpace(c.Identifier))
	}
	title := strings.ToLower(strings.TrimSpace(c.Title))
	author := strings.ToLower(strings.TrimSpace(c.Author))
	if identifier != "" {
		return identifier + "|" + title
	}
	return title + "|" + author
}

func mergeCandidates(base, incoming metadataCandidate) metadataCandidate {
	if len(strings.TrimSpace(incoming.Description)) > len(strings.TrimSpace(base.Description)) {
		base.Description = incoming.Description
	}
	if strings.TrimSpace(base.Language) == "" {
		base.Language = incoming.Language
	}
	if strings.TrimSpace(base.Publisher) == "" {
		base.Publisher = incoming.Publisher
	}
	if strings.TrimSpace(base.Date) == "" {
		base.Date = incoming.Date
	}
	if strings.TrimSpace(base.Identifier) == "" {
		base.Identifier = incoming.Identifier
	}
	if len(base.Subjects) == 0 {
		base.Subjects = incoming.Subjects
	}
	if strings.TrimSpace(base.Author) == "" {
		base.Author = incoming.Author
	}
	if strings.TrimSpace(base.Title) == "" {
		base.Title = incoming.Title
	}
	if strings.TrimSpace(base.Key) == "" {
		base.Key = incoming.Key
	}
	if strings.HasPrefix(base.Source, "openlibrary") && strings.HasPrefix(incoming.Source, "googlebooks") {
		return base
	}
	if strings.HasPrefix(base.Source, "googlebooks") && strings.HasPrefix(incoming.Source, "openlibrary") {
		base.Source = incoming.Source
	}
	return base
}

func firstNonEmpty(values []string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstLanguageCode(values []string) string {
	if len(values) == 0 {
		return ""
	}
	v := strings.TrimSpace(values[0])
	if strings.HasPrefix(v, "/languages/") {
		return strings.TrimPrefix(v, "/languages/")
	}
	return v
}

func languageFromEdition(values []openLibraryKeyRef) string {
	if len(values) == 0 {
		return ""
	}
	v := strings.TrimSpace(values[0].Key)
	if strings.HasPrefix(v, "/languages/") {
		return strings.TrimPrefix(v, "/languages/")
	}
	return v
}

func pickISBN(isbn13 []string, isbn10 []string, fallback string) string {
	if v := normalizeISBN(firstNonEmpty(isbn13)); v != "" {
		return v
	}
	if v := normalizeISBN(firstNonEmpty(isbn10)); v != "" {
		return v
	}
	return normalizeISBN(fallback)
}

func normalizeISBN(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.ToUpper(raw)
	clean := strings.Builder{}
	for i, r := range raw {
		if r >= '0' && r <= '9' {
			clean.WriteRune(r)
			continue
		}
		if r == 'X' && i == len(raw)-1 {
			clean.WriteRune(r)
		}
	}
	v := clean.String()
	if len(v) == 10 || len(v) == 13 {
		return v
	}
	if len(v) > 13 {
		return v[:13]
	}
	return v
}

func uniqueClean(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func mergeSubjects(a []string, b []string) []string {
	combined := append([]string{}, a...)
	combined = append(combined, b...)
	merged := uniqueClean(combined)
	if len(merged) > 15 {
		merged = merged[:15]
	}
	return merged
}

func (s *Server) HandleUpdateMetadata(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	book, err := s.db.GetBookByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Book not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	var req metadataRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	req.Title = strings.TrimSpace(req.Title)
	req.Author = strings.TrimSpace(req.Author)
	req.Language = strings.TrimSpace(req.Language)
	req.Identifier = strings.TrimSpace(req.Identifier)
	req.Publisher = strings.TrimSpace(req.Publisher)
	req.Date = strings.TrimSpace(req.Date)
	req.Description = strings.TrimSpace(req.Description)
	req.Series = strings.TrimSpace(req.Series)
	req.SeriesIndex = strings.TrimSpace(req.SeriesIndex)

	if req.Title == "" {
		http.Error(w, "Title cannot be empty", http.StatusBadRequest)
		return
	}
	if req.Author == "" {
		req.Author = "Unknown Author"
	}

	meta, err := scanner.UpdateEPUBMetadata(book.Path, scanner.MetadataUpdate{
		Title:       req.Title,
		Creator:     req.Author,
		Language:    req.Language,
		Identifier:  req.Identifier,
		Publisher:   req.Publisher,
		Date:        req.Date,
		Description: req.Description,
		Subjects:    req.Subjects,
		Series:      req.Series,
		SeriesIndex: req.SeriesIndex,
	})
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			http.Error(w, "Write permission denied for EPUB file", http.StatusForbidden)
			return
		}
		if errors.Is(err, scanner.ErrMetadataTagNotFound()) {
			http.Error(w, "Unable to locate metadata tags in EPUB", http.StatusUnprocessableEntity)
			return
		}
		log.Printf("metadata update error for %s: %v", book.Path, err)
		http.Error(w, "Failed to update EPUB metadata", http.StatusUnprocessableEntity)
		return
	}

	info, statErr := os.Stat(book.Path)
	if statErr != nil {
		http.Error(w, "Metadata saved but failed to read file mod time", http.StatusInternalServerError)
		return
	}

	title := req.Title
	author := req.Author
	description := req.Description
	if meta != nil {
		if strings.TrimSpace(meta.Title) != "" {
			title = strings.TrimSpace(meta.Title)
		}
		if strings.TrimSpace(meta.Author) != "" {
			author = strings.TrimSpace(meta.Author)
		}
		description = strings.TrimSpace(meta.Description)
	}

	if err := s.db.UpdateBookMetadata(book.ID, title, author, description, info.ModTime()); err != nil {
		http.Error(w, "Failed to update metadata cache", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if meta == nil {
		meta = &scanner.EPUBMetadata{}
	}
	_ = json.NewEncoder(w).Encode(struct {
		BookID int `json:"book_id"`
		*scanner.EPUBMetadata
	}{
		BookID:       book.ID,
		EPUBMetadata: meta,
	})
}

func (s *Server) HandleCover(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	coverPath := fmt.Sprintf("data/covers/%s.jpg", id)
	http.ServeFile(w, r, coverPath)
}

func (s *Server) HandleDownload(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	book, err := s.db.GetBookByID(id)
	if err != nil {
		log.Printf("Download error (ID %s): %v", id, err)
		http.Error(w, "Book not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.epub\"", book.Title))
	w.Header().Set("Content-Type", "application/epub+zip")
	http.ServeFile(w, r, book.Path)
}
