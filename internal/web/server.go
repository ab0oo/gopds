package web

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ab0oo/gopds/internal/database"
	"github.com/ab0oo/gopds/internal/scanner"
	"github.com/go-chi/chi/middleware"
	"github.com/go-chi/chi/v5"
)

type Server struct {
	db   *database.DB
	uiFS embed.FS

	rebuildMu    sync.Mutex
	rebuildState rebuildStatus

	adminUser string
	adminPass string

	sessionMu sync.Mutex
	sessions  map[string]authSession
}

type authSession struct {
	Username  string
	ExpiresAt time.Time
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authStatusPayload struct {
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username,omitempty"`
}

const (
	sessionCookieName = "gopds_session"
	sessionTTL        = 12 * time.Hour
)

type rebuildStatus struct {
	Running     bool      `json:"running"`
	Operation   string    `json:"operation"`
	Phase       string    `json:"phase"`
	Message     string    `json:"message"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	Count       int       `json:"count"`
	Error       string    `json:"error,omitempty"`
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

type coverCandidate struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	MediaType  string `json:"media_type"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	IsCurrent  bool   `json:"is_current"`
	PreviewURL string `json:"preview_url"`
	Source     string `json:"source"`
	Remote     bool   `json:"remote"`
	ImageURL   string `json:"image_url,omitempty"`
}

type updateCoverRequest struct {
	Key         string `json:"key"`
	WriteToEPUB bool   `json:"write_to_epub"`
	ImageURL    string `json:"image_url,omitempty"`
}

type openLibrarySearchResponse struct {
	NumFound int `json:"numFound"`
	Docs     []struct {
		Key              string   `json:"key"`
		Title            string   `json:"title"`
		AuthorName       []string `json:"author_name"`
		Language         []string `json:"language"`
		ISBN             []string `json:"isbn"`
		CoverI           int      `json:"cover_i"`
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
			ImageLinks struct {
				SmallThumbnail string `json:"smallThumbnail"`
				Thumbnail      string `json:"thumbnail"`
				Small          string `json:"small"`
				Medium         string `json:"medium"`
				Large          string `json:"large"`
				ExtraLarge     string `json:"extraLarge"`
			} `json:"imageLinks"`
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
	adminUser := strings.TrimSpace(os.Getenv("ADMIN_USERNAME"))
	if adminUser == "" {
		adminUser = "admin"
	}
	adminPass := os.Getenv("ADMIN_PASSWORD")
	if strings.TrimSpace(adminPass) == "" {
		log.Printf("warning: ADMIN_PASSWORD is empty; authenticated features are disabled until it is set")
	}

	return &Server{
		db:        db,
		uiFS:      uiFS,
		adminUser: adminUser,
		adminPass: adminPass,
		sessions:  make(map[string]authSession),
	}
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
	r.Get("/opds/authors", s.HandleAuthorsCatalog)
	r.Get("/opds/categories", s.HandleCategoriesCatalog)
	r.Get("/", s.HandleRoot)
	r.Get("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	r.Get("/api/auth/status", s.HandleAuthStatus)
	r.Post("/api/auth/login", s.HandleAuthLogin)
	r.Post("/api/auth/logout", s.HandleAuthLogout)
	r.Get("/api/books", s.HandleBooksJSON)
	r.Get("/api/books/{id}/metadata/live", s.requireAuth(s.HandleLiveMetadata))
	r.Put("/api/books/{id}/metadata", s.requireAuth(s.HandleUpdateMetadata))
	r.Get("/api/books/{id}/covers/candidates", s.requireAuth(s.HandleCoverCandidates))
	r.Get("/api/books/{id}/covers/online", s.requireAuth(s.HandleOnlineCoverCandidates))
	r.Get("/api/books/{id}/covers/candidates/{key}", s.requireAuth(s.HandleCoverCandidateImage))
	r.Put("/api/books/{id}/cover", s.requireAuth(s.HandleUpdateCover))
	r.Post("/api/admin/rebuild", s.requireAuth(s.HandleRebuildLibrary))
	r.Post("/api/admin/rescan", s.requireAuth(s.HandleRescanLibrary))
	r.Get("/api/admin/rebuild/status", s.requireAuth(s.HandleRebuildStatus))
	r.Get("/api/openlibrary/search", s.HandleOpenLibrarySearch)
	r.Get("/covers/{id}.jpg", s.HandleCover)
	r.Get("/download/{id}", s.HandleDownload)

	r.Handle("/*", http.FileServer(http.FS(publicFS)))
	return r
}

type authorRangeBucket struct {
	Label    string
	Selector string
	Start    string
	End      string
}

var defaultAuthorBuckets = []authorRangeBucket{
	{Label: "A-D", Selector: "a-d", Start: "A", End: "D"},
	{Label: "E-H", Selector: "e-h", Start: "E", End: "H"},
	{Label: "I-L", Selector: "i-l", Start: "I", End: "L"},
	{Label: "M-P", Selector: "m-p", Start: "M", End: "P"},
	{Label: "Q-T", Selector: "q-t", Start: "Q", End: "T"},
	{Label: "U-Z", Selector: "u-z", Start: "U", End: "Z"},
	{Label: "Other", Selector: "other", Start: "#", End: "#"},
}

func (s *Server) HandleCatalog(w http.ResponseWriter, r *http.Request) {
	selector := strings.TrimSpace(r.URL.Query().Get("authors"))
	if selector != "" {
		s.handleAuthorRangeFeed(w, r, selector)
		return
	}
	s.handleCatalogNavigation(w, r)
}

func (s *Server) HandleAuthorsCatalog(w http.ResponseWriter, r *http.Request) {
	selector := strings.TrimSpace(r.URL.Query().Get("authors"))
	if selector != "" {
		s.handleAuthorRangeFeed(w, r, selector)
		return
	}
	s.handleCatalogNavigation(w, r)
}

func (s *Server) HandleCategoriesCatalog(w http.ResponseWriter, r *http.Request) {
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	subcategory := strings.TrimSpace(r.URL.Query().Get("subcategory"))
	if category == "" {
		s.handleCategoryNavigation(w, r)
		return
	}
	if subcategory != "" {
		s.handleCategoryBooksFeed(w, r, category, subcategory)
		return
	}
	subCounts, err := s.db.GetSubcategoryCounts(category)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if len(subCounts) == 0 {
		s.handleCategoryBooksFeed(w, r, category, "")
		return
	}
	s.handleSubcategoryNavigation(w, r, category, subCounts)
}

func (s *Server) handleCatalogNavigation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog;kind=navigation;charset=utf-8")
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><feed xmlns="http://www.w3.org/2005/Atom">`)
	fmt.Fprint(w, `<title>GoPDS Library</title><id>gopds:catalog:root</id>`)
	fmt.Fprintf(w, `<updated>%s</updated>`, time.Now().UTC().Format(time.RFC3339))
	fmt.Fprint(w, `<link rel="self" href="/opds" type="application/atom+xml;profile=opds-catalog;kind=navigation"/>`)

	for _, b := range defaultAuthorBuckets {
		count, err := s.db.CountBooksByAuthorRange(b.Start, b.End, false)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		href := fmt.Sprintf("/opds?authors=%s&page=1&limit=100", url.QueryEscape(b.Selector))
		fmt.Fprintf(w, `
    <entry>
        <title>Authors %s (%d)</title>
        <id>gopds:authors:%s</id>
        <link rel="subsection" href="%s" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>
    </entry>`, html.EscapeString(b.Label), count, html.EscapeString(b.Selector), html.EscapeString(href))
	}
	categoryCounts, err := s.db.GetCategoryCounts()
	if err == nil && len(categoryCounts) > 0 {
		total := 0
		for _, c := range categoryCounts {
			total += c
		}
		fmt.Fprintf(w, `
    <entry>
        <title>Browse by Category (%d)</title>
        <id>gopds:categories</id>
        <link rel="subsection" href="/opds/categories" type="application/atom+xml;profile=opds-catalog;kind=navigation"/>
    </entry>`, total)
	}
	fmt.Fprint(w, `</feed>`)
}

func (s *Server) handleAuthorRangeFeed(w http.ResponseWriter, r *http.Request, selector string) {
	start, end, label, err := parseAuthorRangeSelector(selector)
	if err != nil {
		http.Error(w, "Invalid authors selector. Use authors=a or authors=a-d", http.StatusBadRequest)
		return
	}

	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	if page < 1 {
		page = 1
	}
	limit := parseIntDefault(r.URL.Query().Get("limit"), 100)
	if limit < 1 {
		limit = 100
	}
	if limit > 250 {
		limit = 250
	}

	total, err := s.db.CountBooksByAuthorRange(start, end, false)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	lastPage := 1
	if total > 0 {
		lastPage = (total + limit - 1) / limit
	}
	if page > lastPage {
		page = lastPage
	}
	offset := (page - 1) * limit

	books, err := s.db.GetBooksByAuthorRange(start, end, false, limit, offset)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	base := fmt.Sprintf("/opds?authors=%s&limit=%d", url.QueryEscape(strings.ToLower(selector)), limit)
	self := fmt.Sprintf("%s&page=%d", base, page)
	first := fmt.Sprintf("%s&page=1", base)
	last := fmt.Sprintf("%s&page=%d", base, lastPage)

	w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog;kind=acquisition;charset=utf-8")
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><feed xmlns="http://www.w3.org/2005/Atom">`)
	fmt.Fprintf(w, `<title>GoPDS Library - Authors %s (%d)</title>`, html.EscapeString(label), total)
	fmt.Fprintf(w, `<id>gopds:authors:%s:page:%d</id>`, html.EscapeString(strings.ToLower(selector)), page)
	fmt.Fprintf(w, `<updated>%s</updated>`, time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(w, `<link rel="self" href="%s" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>`, html.EscapeString(self))
	fmt.Fprint(w, `<link rel="start" href="/opds" type="application/atom+xml;profile=opds-catalog;kind=navigation"/>`)
	fmt.Fprint(w, `<link rel="up" href="/opds" type="application/atom+xml;profile=opds-catalog;kind=navigation"/>`)
	fmt.Fprintf(w, `<link rel="first" href="%s" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>`, html.EscapeString(first))
	fmt.Fprintf(w, `<link rel="last" href="%s" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>`, html.EscapeString(last))
	if page > 1 {
		prev := fmt.Sprintf("%s&page=%d", base, page-1)
		fmt.Fprintf(w, `<link rel="previous" href="%s" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>`, html.EscapeString(prev))
	}
	if page < lastPage {
		next := fmt.Sprintf("%s&page=%d", base, page+1)
		fmt.Fprintf(w, `<link rel="next" href="%s" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>`, html.EscapeString(next))
	}

	for _, b := range books {
		writeOPDSEntry(w, b)
	}
	fmt.Fprint(w, `</feed>`)
}

func (s *Server) handleCategoryNavigation(w http.ResponseWriter, r *http.Request) {
	counts, err := s.db.GetCategoryCounts()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog;kind=navigation;charset=utf-8")
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><feed xmlns="http://www.w3.org/2005/Atom">`)
	fmt.Fprint(w, `<title>GoPDS Library - Categories</title><id>gopds:categories</id>`)
	fmt.Fprintf(w, `<updated>%s</updated>`, time.Now().UTC().Format(time.RFC3339))
	fmt.Fprint(w, `<link rel="self" href="/opds/categories" type="application/atom+xml;profile=opds-catalog;kind=navigation"/>`)
	fmt.Fprint(w, `<link rel="start" href="/opds" type="application/atom+xml;profile=opds-catalog;kind=navigation"/>`)

	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return strings.ToLower(keys[i]) < strings.ToLower(keys[j]) })

	for _, category := range keys {
		count := counts[category]
		href := "/opds/categories?category=" + url.QueryEscape(category)
		fmt.Fprintf(w, `
    <entry>
        <title>%s (%d)</title>
        <id>gopds:category:%s</id>
        <link rel="subsection" href="%s" type="application/atom+xml;profile=opds-catalog;kind=navigation"/>
    </entry>`,
			html.EscapeString(category), count, html.EscapeString(strings.ToLower(category)), html.EscapeString(href))
	}

	fmt.Fprint(w, `</feed>`)
}

func (s *Server) handleSubcategoryNavigation(w http.ResponseWriter, r *http.Request, category string, subCounts map[string]int) {
	w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog;kind=navigation;charset=utf-8")
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><feed xmlns="http://www.w3.org/2005/Atom">`)
	fmt.Fprintf(w, `<title>GoPDS Library - %s</title>`, html.EscapeString(category))
	fmt.Fprintf(w, `<id>gopds:category:%s</id>`, html.EscapeString(strings.ToLower(category)))
	fmt.Fprintf(w, `<updated>%s</updated>`, time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(w, `<link rel="self" href="/opds/categories?category=%s" type="application/atom+xml;profile=opds-catalog;kind=navigation"/>`, url.QueryEscape(category))
	fmt.Fprint(w, `<link rel="up" href="/opds/categories" type="application/atom+xml;profile=opds-catalog;kind=navigation"/>`)

	keys := make([]string, 0, len(subCounts))
	for k := range subCounts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return strings.ToLower(keys[i]) < strings.ToLower(keys[j]) })

	totalHref := fmt.Sprintf("/opds/categories?category=%s&page=1&limit=100", url.QueryEscape(category))
	totalCount, _ := s.db.CountBooksByCategory(category, "")
	fmt.Fprintf(w, `
    <entry>
        <title>All in %s (%d)</title>
        <id>gopds:category:%s:all</id>
        <link rel="subsection" href="%s" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>
    </entry>`, html.EscapeString(category), totalCount, html.EscapeString(strings.ToLower(category)), html.EscapeString(totalHref))

	for _, sub := range keys {
		count := subCounts[sub]
		href := fmt.Sprintf("/opds/categories?category=%s&subcategory=%s&page=1&limit=100", url.QueryEscape(category), url.QueryEscape(sub))
		fmt.Fprintf(w, `
    <entry>
        <title>%s / %s (%d)</title>
        <id>gopds:category:%s:%s</id>
        <link rel="subsection" href="%s" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>
    </entry>`,
			html.EscapeString(category), html.EscapeString(sub), count,
			html.EscapeString(strings.ToLower(category)), html.EscapeString(strings.ToLower(sub)),
			html.EscapeString(href))
	}
	fmt.Fprint(w, `</feed>`)
}

func (s *Server) handleCategoryBooksFeed(w http.ResponseWriter, r *http.Request, category, subcategory string) {
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	if page < 1 {
		page = 1
	}
	limit := parseIntDefault(r.URL.Query().Get("limit"), 100)
	if limit < 1 {
		limit = 100
	}
	if limit > 250 {
		limit = 250
	}

	total, err := s.db.CountBooksByCategory(category, subcategory)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	lastPage := 1
	if total > 0 {
		lastPage = (total + limit - 1) / limit
	}
	if page > lastPage {
		page = lastPage
	}
	offset := (page - 1) * limit

	books, err := s.db.GetBooksByCategory(category, subcategory, limit, offset)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	base := fmt.Sprintf("/opds/categories?category=%s&limit=%d", url.QueryEscape(category), limit)
	if subcategory != "" {
		base += "&subcategory=" + url.QueryEscape(subcategory)
	}
	self := fmt.Sprintf("%s&page=%d", base, page)
	first := fmt.Sprintf("%s&page=1", base)
	last := fmt.Sprintf("%s&page=%d", base, lastPage)
	title := category
	if subcategory != "" {
		title = category + " / " + subcategory
	}

	w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog;kind=acquisition;charset=utf-8")
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><feed xmlns="http://www.w3.org/2005/Atom">`)
	fmt.Fprintf(w, `<title>GoPDS Library - %s (%d)</title>`, html.EscapeString(title), total)
	fmt.Fprintf(w, `<id>gopds:category:%s:%d</id>`, html.EscapeString(strings.ToLower(title)), page)
	fmt.Fprintf(w, `<updated>%s</updated>`, time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(w, `<link rel="self" href="%s" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>`, html.EscapeString(self))
	fmt.Fprint(w, `<link rel="up" href="/opds/categories" type="application/atom+xml;profile=opds-catalog;kind=navigation"/>`)
	fmt.Fprintf(w, `<link rel="first" href="%s" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>`, html.EscapeString(first))
	fmt.Fprintf(w, `<link rel="last" href="%s" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>`, html.EscapeString(last))
	if page > 1 {
		prev := fmt.Sprintf("%s&page=%d", base, page-1)
		fmt.Fprintf(w, `<link rel="previous" href="%s" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>`, html.EscapeString(prev))
	}
	if page < lastPage {
		next := fmt.Sprintf("%s&page=%d", base, page+1)
		fmt.Fprintf(w, `<link rel="next" href="%s" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>`, html.EscapeString(next))
	}

	for _, b := range books {
		writeOPDSEntry(w, b)
	}
	fmt.Fprint(w, `</feed>`)
}

func writeOPDSEntry(w io.Writer, b database.Book) {
	safeTitle := html.EscapeString(b.Title)
	safeAuthor := html.EscapeString(b.Author)
	fmt.Fprintf(w, `
    <entry>
        <title>%s</title>
        <id>%d</id>
        <author><name>%s</name></author>`, safeTitle, b.ID, safeAuthor)
	if strings.TrimSpace(b.Category) != "" {
		fmt.Fprintf(w, `<category term="%s" label="%s"/>`, html.EscapeString(b.Category), html.EscapeString(b.Category))
	}
	if strings.TrimSpace(b.Subcategory) != "" {
		label := b.Category + " / " + b.Subcategory
		fmt.Fprintf(w, `<category term="%s" label="%s"/>`, html.EscapeString(label), html.EscapeString(label))
	}
	fmt.Fprintf(w, `
        <link rel="http://opds-spec.org/image" href="/covers/%d.jpg" type="image/jpeg"/>
        <link rel="http://opds-spec.org/acquisition" href="/download/%d" type="application/epub+zip"/>
    </entry>`, b.ID, b.ID)
}

func parseAuthorRangeSelector(selector string) (string, string, string, error) {
	s := strings.ToUpper(strings.TrimSpace(selector))
	if s == "OTHER" || s == "#" {
		return "#", "#", "Other", nil
	}

	if len(s) == 1 && s[0] >= 'A' && s[0] <= 'Z' {
		return s, s, s, nil
	}

	parts := strings.Split(s, "-")
	if len(parts) == 2 && len(parts[0]) == 1 && len(parts[1]) == 1 {
		a := parts[0][0]
		b := parts[1][0]
		if a >= 'A' && a <= 'Z' && b >= 'A' && b <= 'Z' && a <= b {
			return string(a), string(b), fmt.Sprintf("%c-%c", a, b), nil
		}
	}

	return "", "", "", fmt.Errorf("invalid selector")
}

func parseIntDefault(raw string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return v
}

func (s *Server) HandleRoot(w http.ResponseWriter, r *http.Request) {
	accept := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept")))
	ua := strings.ToLower(strings.TrimSpace(r.Header.Get("User-Agent")))

	// Serve OPDS catalog at root for OPDS/e-reader clients, while keeping HTML UI for browsers.
	wantsOPDS := strings.Contains(accept, "application/atom+xml") ||
		strings.Contains(accept, "application/opds+json") ||
		(strings.Contains(accept, "application/xml") && !strings.Contains(accept, "text/html")) ||
		(strings.Contains(accept, "*/*") && !strings.Contains(accept, "text/html")) ||
		strings.Contains(ua, "thorium")

	if wantsOPDS || r.URL.Query().Get("opds") == "1" {
		s.HandleCatalog(w, r)
		return
	}

	indexContent, err := s.uiFS.ReadFile("web/ui/index.html")
	if err != nil {
		http.Error(w, "UI not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexContent)
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.authenticatedUser(r); !ok {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) HandleAuthStatus(w http.ResponseWriter, r *http.Request) {
	username, ok := s.authenticatedUser(r)
	w.Header().Set("Content-Type", "application/json")
	if !ok {
		_ = json.NewEncoder(w).Encode(authStatusPayload{Authenticated: false})
		return
	}
	_ = json.NewEncoder(w).Encode(authStatusPayload{
		Authenticated: true,
		Username:      username,
	})
}

func (s *Server) HandleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.adminPass) == "" {
		http.Error(w, "Authentication is not configured on server", http.StatusServiceUnavailable)
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		req.Username = "admin"
	}

	if req.Username != s.adminUser || req.Password != s.adminPass {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	token, err := generateSessionToken()
	if err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	expiresAt := time.Now().UTC().Add(sessionTTL)
	s.sessionMu.Lock()
	s.sessions[token] = authSession{
		Username:  req.Username,
		ExpiresAt: expiresAt,
	}
	s.sessionMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		MaxAge:   int(sessionTTL.Seconds()),
		Secure:   r.TLS != nil,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(authStatusPayload{
		Authenticated: true,
		Username:      req.Username,
	})
}

func (s *Server) HandleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		token := strings.TrimSpace(c.Value)
		if token != "" {
			s.sessionMu.Lock()
			delete(s.sessions, token)
			s.sessionMu.Unlock()
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		Secure:   r.TLS != nil,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(authStatusPayload{Authenticated: false})
}

func (s *Server) authenticatedUser(r *http.Request) (string, bool) {
	if strings.TrimSpace(s.adminPass) == "" {
		return "", false
	}

	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false
	}
	token := strings.TrimSpace(c.Value)
	if token == "" {
		return "", false
	}

	now := time.Now().UTC()
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	sess, ok := s.sessions[token]
	if !ok {
		return "", false
	}
	if now.After(sess.ExpiresAt) {
		delete(s.sessions, token)
		return "", false
	}
	return sess.Username, true
}

func generateSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
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

	bookPath, err := s.resolveBookPath(book)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read EPUB metadata: %v", err), http.StatusUnprocessableEntity)
		return
	}

	meta, err := scanner.ExtractLiveMetadata(bookPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read EPUB metadata: %v", err), http.StatusUnprocessableEntity)
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
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	applyOutboundHeaders(req)

	resp, err := client.Do(req)
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

func applyOutboundHeaders(req *http.Request) {
	// Wikimedia APIs require a descriptive User-Agent; reuse this for all upstream lookups.
	req.Header.Set("User-Agent", "GoPDS/1.0 (+https://github.com/ab0oo/gopds)")
	req.Header.Set("Accept", "application/json, image/*;q=0.9, */*;q=0.8")
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

	bookPath, err := s.resolveBookPath(book)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to update EPUB metadata: %v", err), http.StatusUnprocessableEntity)
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

	meta, err := scanner.UpdateEPUBMetadata(bookPath, scanner.MetadataUpdate{
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
		log.Printf("metadata update error for %s: %v", bookPath, err)
		http.Error(w, "Failed to update EPUB metadata", http.StatusUnprocessableEntity)
		return
	}

	info, statErr := os.Stat(bookPath)
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

func (s *Server) HandleCoverCandidates(w http.ResponseWriter, r *http.Request) {
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

	bookPath, err := s.resolveBookPath(book)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to locate EPUB: %v", err), http.StatusUnprocessableEntity)
		return
	}

	options, err := scanner.ListCoverOptions(bookPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list cover candidates: %v", err), http.StatusUnprocessableEntity)
		return
	}

	out := make([]coverCandidate, 0, len(options))
	for _, c := range options {
		key := encodeCoverKey(c.ZipPath)
		out = append(out, coverCandidate{
			Key:        key,
			Name:       c.Name,
			MediaType:  c.MediaType,
			Width:      c.Width,
			Height:     c.Height,
			IsCurrent:  c.IsCurrent,
			PreviewURL: fmt.Sprintf("/api/books/%d/covers/candidates/%s", book.ID, url.PathEscape(key)),
			Source:     "epub",
			Remote:     false,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		BookID     int              `json:"book_id"`
		Candidates []coverCandidate `json:"candidates"`
	}{
		BookID:     book.ID,
		Candidates: out,
	})
}

func (s *Server) HandleOnlineCoverCandidates(w http.ResponseWriter, r *http.Request) {
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

	bookPath, err := s.resolveBookPath(book)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to locate EPUB: %v", err), http.StatusUnprocessableEntity)
		return
	}

	meta, _ := scanner.ExtractLiveMetadata(bookPath)
	title := strings.TrimSpace(book.Title)
	author := strings.TrimSpace(book.Author)
	isbn := ""
	if meta != nil {
		if strings.TrimSpace(meta.Title) != "" {
			title = strings.TrimSpace(meta.Title)
		}
		if strings.TrimSpace(meta.Author) != "" {
			author = strings.TrimSpace(meta.Author)
		}
		isbn = normalizeISBN(meta.Identifier)
	}

	client := &http.Client{Timeout: 12 * time.Second}
	candidates := make([]coverCandidate, 0, 12)
	seen := map[string]struct{}{}
	log.Printf("[covers.online] lookup start book_id=%d title=%q author=%q isbn=%q", book.ID, title, author, isbn)

	// Open Library ISBN cover tends to be high quality when ISBN is available.
	if isbn != "" {
		ol := fmt.Sprintf("https://covers.openlibrary.org/b/isbn/%s-L.jpg?default=false", url.PathEscape(isbn))
		if ok := remoteImageReachable(client, ol); ok {
			candidates = append(candidates, makeRemoteCoverCandidate(
				ol,
				fmt.Sprintf("Open Library ISBN %s", isbn),
				"openlibrary",
			))
			seen[ol] = struct{}{}
			log.Printf("[covers.online] openlibrary isbn hit book_id=%d url=%s", book.ID, ol)
		} else {
			log.Printf("[covers.online] openlibrary isbn miss book_id=%d url=%s", book.ID, ol)
		}
	} else {
		log.Printf("[covers.online] no isbn available for book_id=%d", book.ID)
	}

	query := strings.TrimSpace(strings.Join([]string{title, author, "book"}, " "))
	if query != "" || isbn != "" {
		gb, err := fetchGoogleBookCoverCandidates(client, query, isbn, 8)
		if err == nil {
			log.Printf("[covers.online] googlebooks candidates book_id=%d query=%q isbn=%q count=%d", book.ID, query, isbn, len(gb))
			for _, c := range gb {
				if _, ok := seen[c.ImageURL]; ok {
					continue
				}
				seen[c.ImageURL] = struct{}{}
				candidates = append(candidates, c)
			}
		} else {
			log.Printf("[covers.online] googlebooks error book_id=%d query=%q isbn=%q err=%v", book.ID, query, isbn, err)
		}
	}

	if query != "" {
		olSearch, err := fetchOpenLibrarySearchCoverCandidates(client, query, 8)
		if err == nil {
			log.Printf("[covers.online] openlibrary search candidates book_id=%d query=%q count=%d", book.ID, query, len(olSearch))
			for _, c := range olSearch {
				if _, ok := seen[c.ImageURL]; ok {
					continue
				}
				seen[c.ImageURL] = struct{}{}
				candidates = append(candidates, c)
			}
		} else {
			log.Printf("[covers.online] openlibrary search error book_id=%d query=%q err=%v", book.ID, query, err)
		}
	}

	wikiQueries := make([]string, 0, 2)
	if query != "" {
		wikiQueries = append(wikiQueries, query)
	}
	if title != "" {
		wikiQueries = append(wikiQueries, strings.TrimSpace(title+" book"))
	}
	for _, q := range wikiQueries {
		wiki, err := fetchWikipediaCoverCandidates(client, q, 6)
		if err == nil {
			log.Printf("[covers.online] wikipedia candidates book_id=%d query=%q count=%d", book.ID, q, len(wiki))
			for _, c := range wiki {
				if _, ok := seen[c.ImageURL]; ok {
					continue
				}
				seen[c.ImageURL] = struct{}{}
				candidates = append(candidates, c)
			}
		} else {
			log.Printf("[covers.online] wikipedia error book_id=%d query=%q err=%v", book.ID, q, err)
		}
	}

	if query == "" {
		log.Printf("[covers.online] empty query for book_id=%d", book.ID)
	} else {
		log.Printf("[covers.online] query used for book_id=%d query=%q", book.ID, query)
	}

	candidates = rankAndFilterOnlineCovers(client, candidates)
	log.Printf("[covers.online] lookup done book_id=%d total_candidates=%d", book.ID, len(candidates))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		BookID     int              `json:"book_id"`
		Candidates []coverCandidate `json:"candidates"`
	}{
		BookID:     book.ID,
		Candidates: candidates,
	})
}

func (s *Server) HandleCoverCandidateImage(w http.ResponseWriter, r *http.Request) {
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

	bookPath, err := s.resolveBookPath(book)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to locate EPUB: %v", err), http.StatusUnprocessableEntity)
		return
	}

	key := chi.URLParam(r, "key")
	zipPath, err := decodeCoverKey(key)
	if err != nil {
		http.Error(w, "Invalid cover key", http.StatusBadRequest)
		return
	}

	raw, _, err := scanner.ReadCoverOption(bookPath, zipPath)
	if err != nil {
		http.Error(w, "Cover candidate not found", http.StatusNotFound)
		return
	}

	contentType := "image/jpeg"
	if strings.HasSuffix(strings.ToLower(zipPath), ".png") {
		contentType = "image/png"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

func (s *Server) HandleUpdateCover(w http.ResponseWriter, r *http.Request) {
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

	bookPath, err := s.resolveBookPath(book)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to locate EPUB: %v", err), http.StatusUnprocessableEntity)
		return
	}

	var req updateCoverRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	req.ImageURL = strings.TrimSpace(req.ImageURL)
	if req.Key == "" && req.ImageURL == "" {
		http.Error(w, "Cover key or image_url is required", http.StatusBadRequest)
		return
	}

	var raw []byte
	var zipPath string
	if req.ImageURL != "" {
		raw, err = fetchAllowedRemoteImage(req.ImageURL)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to fetch remote cover: %v", err), http.StatusUnprocessableEntity)
			return
		}
	} else {
		zipPath, err = decodeCoverKey(req.Key)
		if err != nil {
			http.Error(w, "Invalid cover key", http.StatusBadRequest)
			return
		}

		raw, _, err = scanner.ReadCoverOption(bookPath, zipPath)
		if err != nil {
			http.Error(w, "Cover candidate not found", http.StatusNotFound)
			return
		}
	}

	cacheJPG, err := scanner.ConvertImageToJPEG(raw)
	if err != nil {
		http.Error(w, fmt.Sprintf("Cover conversion failed: %v", err), http.StatusUnprocessableEntity)
		return
	}

	if err := os.MkdirAll("./data/covers", 0755); err != nil {
		http.Error(w, fmt.Sprintf("Failed to prepare covers cache: %v", err), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(fmt.Sprintf("./data/covers/%d.jpg", book.ID), cacheJPG, 0644); err != nil {
		http.Error(w, fmt.Sprintf("Failed to update cover cache: %v", err), http.StatusInternalServerError)
		return
	}

	if req.WriteToEPUB {
		if req.ImageURL != "" {
			if err := scanner.WriteCoverBytesToEPUB(bookPath, cacheJPG); err != nil {
				http.Error(w, fmt.Sprintf("Failed writing remote cover to EPUB: %v", err), http.StatusUnprocessableEntity)
				return
			}
		} else {
			if err := scanner.WriteCoverToEPUB(bookPath, zipPath); err != nil {
				http.Error(w, fmt.Sprintf("Failed writing cover to EPUB: %v", err), http.StatusUnprocessableEntity)
				return
			}
		}
		localCoverPath := filepath.Join(filepath.Dir(bookPath), "cover.jpg")
		if err := os.WriteFile(localCoverPath, cacheJPG, 0644); err != nil {
			if errors.Is(err, os.ErrPermission) {
				http.Error(w, "Write permission denied for sibling cover.jpg", http.StatusForbidden)
				return
			}
			http.Error(w, fmt.Sprintf("Failed writing sibling cover.jpg: %v", err), http.StatusUnprocessableEntity)
			return
		}
		if info, err := os.Stat(bookPath); err == nil {
			_ = s.db.UpdateBookMetadata(book.ID, book.Title, book.Author, book.Description, info.ModTime())
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		OK          bool `json:"ok"`
		BookID      int  `json:"book_id"`
		WroteToEPUB bool `json:"wrote_to_epub"`
	}{
		OK:          true,
		BookID:      book.ID,
		WroteToEPUB: req.WriteToEPUB,
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

	bookPath, err := s.resolveBookPath(book)
	if err != nil {
		log.Printf("Download error (ID %s): %v", id, err)
		http.Error(w, "Book file not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.epub\"", book.Title))
	w.Header().Set("Content-Type", "application/epub+zip")
	http.ServeFile(w, r, bookPath)
}

func (s *Server) HandleRebuildLibrary(w http.ResponseWriter, r *http.Request) {
	s.startScanJob(w, "rebuild")
}

func (s *Server) HandleRescanLibrary(w http.ResponseWriter, r *http.Request) {
	s.startScanJob(w, "rescan")
}

func (s *Server) startScanJob(w http.ResponseWriter, operation string) {
	s.rebuildMu.Lock()
	if s.rebuildState.Running {
		status := s.rebuildState
		s.rebuildMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(status)
		return
	}
	startedAt := time.Now().UTC()
	label := "Rebuild"
	if operation == "rescan" {
		label = "Rescan"
	}
	s.rebuildState = rebuildStatus{
		Running:   true,
		Operation: operation,
		Phase:     "queued",
		Message:   label + " queued.",
		StartedAt: startedAt,
	}
	status := s.rebuildState
	s.rebuildMu.Unlock()

	go s.runScanJob(operation)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(status)
}

func (s *Server) HandleRebuildStatus(w http.ResponseWriter, r *http.Request) {
	s.rebuildMu.Lock()
	status := s.rebuildState
	s.rebuildMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func encodeCoverKey(zipPath string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(zipPath))
}

func decodeCoverKey(key string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(key))
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(string(raw))
	if p == "" {
		return "", fmt.Errorf("empty cover key")
	}
	return p, nil
}

type wikiOpenSearchResponse []any

type wikiSummaryResponse struct {
	Title     string `json:"title"`
	Thumbnail *struct {
		Source string `json:"source"`
	} `json:"thumbnail"`
	OriginalImage *struct {
		Source string `json:"source"`
	} `json:"originalimage"`
}

func fetchWikipediaCoverCandidates(client *http.Client, query string, limit int) ([]coverCandidate, error) {
	if limit <= 0 {
		limit = 6
	}
	opensearchURL := "https://en.wikipedia.org/w/api.php?action=opensearch&format=json&namespace=0&limit=" + strconv.Itoa(limit) + "&search=" + url.QueryEscape(query)
	var raw wikiOpenSearchResponse
	if err := fetchJSON(client, opensearchURL, &raw); err != nil {
		return nil, err
	}
	if len(raw) < 2 {
		return nil, nil
	}

	titlesAny, ok := raw[1].([]any)
	if !ok {
		return nil, nil
	}

	out := make([]coverCandidate, 0, len(titlesAny))
	seen := map[string]struct{}{}
	for _, v := range titlesAny {
		title, ok := v.(string)
		if !ok {
			continue
		}
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}

		summaryURL := "https://en.wikipedia.org/api/rest_v1/page/summary/" + url.PathEscape(title)
		var summary wikiSummaryResponse
		if err := fetchJSON(client, summaryURL, &summary); err != nil {
			continue
		}

		imageURL := ""
		if summary.OriginalImage != nil {
			imageURL = strings.TrimSpace(summary.OriginalImage.Source)
		}
		if imageURL == "" && summary.Thumbnail != nil {
			imageURL = strings.TrimSpace(summary.Thumbnail.Source)
		}
		if imageURL == "" {
			continue
		}
		if !isAllowedRemoteCoverURL(imageURL) {
			continue
		}
		if _, ok := seen[imageURL]; ok {
			continue
		}
		seen[imageURL] = struct{}{}
		out = append(out, makeRemoteCoverCandidate(imageURL, firstNonEmpty([]string{summary.Title, title}), "wikipedia"))
	}
	return out, nil
}

func makeRemoteCoverCandidate(imageURL, name, source string) coverCandidate {
	return coverCandidate{
		Key:        "remote:" + encodeCoverKey(imageURL),
		Name:       strings.TrimSpace(name),
		MediaType:  mediaTypeFromURL(imageURL),
		Width:      0,
		Height:     0,
		IsCurrent:  false,
		PreviewURL: imageURL,
		Source:     source,
		Remote:     true,
		ImageURL:   imageURL,
	}
}

func fetchGoogleBookCoverCandidates(client *http.Client, query string, isbn string, limit int) ([]coverCandidate, error) {
	if limit <= 0 {
		limit = 6
	}
	queries := make([]string, 0, 2)
	if strings.TrimSpace(isbn) != "" {
		queries = append(queries, "isbn:"+normalizeISBN(isbn))
	}
	if strings.TrimSpace(query) != "" {
		queries = append(queries, strings.TrimSpace(query))
	}

	out := make([]coverCandidate, 0, limit)
	seen := map[string]struct{}{}

	for _, q := range queries {
		googleURL := "https://www.googleapis.com/books/v1/volumes?maxResults=" + strconv.Itoa(limit) + "&q=" + url.QueryEscape(q)
		var decoded googleBooksResponse
		if err := fetchJSON(client, googleURL, &decoded); err != nil {
			continue
		}

		for _, item := range decoded.Items {
			imageURL := pickFirstNonEmpty(
				item.VolumeInfo.ImageLinks.ExtraLarge,
				item.VolumeInfo.ImageLinks.Large,
				item.VolumeInfo.ImageLinks.Medium,
				item.VolumeInfo.ImageLinks.Small,
				item.VolumeInfo.ImageLinks.Thumbnail,
				item.VolumeInfo.ImageLinks.SmallThumbnail,
			)
			imageURL = strings.TrimSpace(imageURL)
			if imageURL == "" {
				continue
			}
			imageURL = normalizeGoogleBooksImageURL(imageURL)
			if !isAllowedRemoteCoverURL(imageURL) {
				continue
			}
			if _, ok := seen[imageURL]; ok {
				continue
			}
			seen[imageURL] = struct{}{}

			name := firstNonEmpty([]string{item.VolumeInfo.Title, "Google Books"})
			out = append(out, makeRemoteCoverCandidate(imageURL, name, "googlebooks"))
		}
	}

	return out, nil
}

func fetchOpenLibrarySearchCoverCandidates(client *http.Client, query string, limit int) ([]coverCandidate, error) {
	if limit <= 0 {
		limit = 8
	}
	openLibraryURL := "https://openlibrary.org/search.json?limit=" + strconv.Itoa(limit) + "&q=" + url.QueryEscape(query)
	var decoded openLibrarySearchResponse
	if err := fetchJSON(client, openLibraryURL, &decoded); err != nil {
		return nil, err
	}

	out := make([]coverCandidate, 0, len(decoded.Docs))
	seen := map[string]struct{}{}
	for _, d := range decoded.Docs {
		if d.CoverI <= 0 {
			continue
		}
		imageURL := fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-L.jpg?default=false", d.CoverI)
		if !isAllowedRemoteCoverURL(imageURL) {
			continue
		}
		if _, ok := seen[imageURL]; ok {
			continue
		}
		seen[imageURL] = struct{}{}
		name := firstNonEmpty([]string{d.Title, "Open Library"})
		out = append(out, makeRemoteCoverCandidate(imageURL, name, "openlibrary"))
	}
	return out, nil
}

func normalizeGoogleBooksImageURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	if strings.EqualFold(u.Scheme, "http") {
		u.Scheme = "https"
	}
	q := u.Query()
	q.Del("edge")
	q.Set("img", "1")
	if q.Get("zoom") == "" {
		q.Set("zoom", "2")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func pickFirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func mediaTypeFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "image/jpeg"
	}
	p := strings.ToLower(u.Path)
	if strings.HasSuffix(p, ".png") {
		return "image/png"
	}
	return "image/jpeg"
}

func isAllowedRemoteCoverURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return false
	}
	allowed := []string{
		"covers.openlibrary.org",
		"books.google.com",
		"books.googleusercontent.com",
		"lh3.googleusercontent.com",
		"upload.wikimedia.org",
		"wikipedia.org",
		"en.wikipedia.org",
	}
	for _, a := range allowed {
		if host == a || strings.HasSuffix(host, "."+a) {
			return true
		}
	}
	return false
}

func remoteImageReachable(client *http.Client, raw string) bool {
	if !isAllowedRemoteCoverURL(raw) {
		return false
	}
	req, err := http.NewRequest(http.MethodHead, raw, nil)
	if err != nil {
		return false
	}
	applyOutboundHeaders(req)
	res, err := client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode >= 200 && res.StatusCode < 300
}

func fetchAllowedRemoteImage(raw string) ([]byte, error) {
	if !isAllowedRemoteCoverURL(raw) {
		return nil, fmt.Errorf("remote URL host is not allowed")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	applyOutboundHeaders(req)
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status: %d", res.StatusCode)
	}
	const maxBytes = 10 << 20 // 10MB
	limited := io.LimitReader(res.Body, maxBytes+1)
	b, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(b) > maxBytes {
		return nil, fmt.Errorf("remote image too large")
	}
	return b, nil
}

func rankAndFilterOnlineCovers(client *http.Client, in []coverCandidate) []coverCandidate {
	minW := envIntDefault("ONLINE_COVER_MIN_WIDTH", 300)
	minH := envIntDefault("ONLINE_COVER_MIN_HEIGHT", 420)

	out := make([]coverCandidate, 0, len(in))
	for _, c := range in {
		if !c.Remote {
			out = append(out, c)
			continue
		}

		w, h, ok := probeRemoteImageDimensions(client, c.ImageURL)
		if ok {
			c.Width = w
			c.Height = h
		}
		if c.Width > 0 && c.Height > 0 && (c.Width < minW || c.Height < minH) {
			continue
		}
		out = append(out, c)
	}

	sort.SliceStable(out, func(i, j int) bool {
		a := out[i]
		b := out[j]

		ar := sourcePriorityRank(a.Source)
		br := sourcePriorityRank(b.Source)
		if ar != br {
			return ar < br
		}

		aa := a.Width * a.Height
		ba := b.Width * b.Height
		if aa != ba {
			return aa > ba
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})

	return out
}

func sourcePriorityRank(source string) int {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "googlebooks":
		return 1
	case "openlibrary":
		return 2
	case "wikipedia":
		return 3
	default:
		return 9
	}
}

func probeRemoteImageDimensions(client *http.Client, raw string) (int, int, bool) {
	if !isAllowedRemoteCoverURL(raw) {
		return 0, 0, false
	}
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		return 0, 0, false
	}
	applyOutboundHeaders(req)
	res, err := client.Do(req)
	if err != nil {
		return 0, 0, false
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return 0, 0, false
	}

	const sniffLimit = 5 << 20 // 5MB cap for probing dimensions
	b, err := io.ReadAll(io.LimitReader(res.Body, sniffLimit))
	if err != nil {
		return 0, 0, false
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

func envIntDefault(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func (s *Server) runScanJob(operation string) {
	label := "Rebuild"
	if operation == "rescan" {
		label = "Rescan"
	}

	if operation == "rebuild" {
		s.setRebuildProgress("resetting_db", "Resetting database cache...")
		if err := s.db.RebuildBooksTable(); err != nil {
			s.finishRebuildWithError(fmt.Sprintf("Failed to reset database: %v", err), label)
			return
		}

		s.setRebuildProgress("clearing_covers", "Clearing covers cache...")
		if err := os.RemoveAll("./data/covers"); err != nil {
			s.finishRebuildWithError(fmt.Sprintf("Failed to clear covers cache: %v", err), label)
			return
		}
		if err := os.MkdirAll("./data/covers", 0755); err != nil {
			s.finishRebuildWithError(fmt.Sprintf("Failed to recreate covers cache: %v", err), label)
			return
		}
	}

	bookPath := strings.TrimSpace(os.Getenv("BOOK_PATH"))
	if bookPath == "" {
		bookPath = "./books"
	}

	s.setRebuildProgress("scanning", "Scanning library...")
	sc := scanner.New(s.db)
	if err := sc.Start(bookPath); err != nil {
		s.finishRebuildWithError(fmt.Sprintf("%s scan failed: %v", label, err), label)
		return
	}

	books, err := s.db.GetAllBooks()
	if err != nil {
		s.finishRebuildWithError(fmt.Sprintf("%s finished but listing failed: %v", label, err), label)
		return
	}

	s.rebuildMu.Lock()
	s.rebuildState.Running = false
	s.rebuildState.Phase = "complete"
	s.rebuildState.Message = fmt.Sprintf("%s complete. %d books indexed.", label, len(books))
	s.rebuildState.Error = ""
	s.rebuildState.Count = len(books)
	s.rebuildState.CompletedAt = time.Now().UTC()
	s.rebuildMu.Unlock()
}

func (s *Server) setRebuildProgress(phase, message string) {
	s.rebuildMu.Lock()
	s.rebuildState.Phase = phase
	s.rebuildState.Message = message
	s.rebuildMu.Unlock()
}

func (s *Server) finishRebuildWithError(message string, label string) {
	s.rebuildMu.Lock()
	s.rebuildState.Running = false
	s.rebuildState.Phase = "failed"
	s.rebuildState.Message = label + " failed."
	s.rebuildState.Error = message
	s.rebuildState.CompletedAt = time.Now().UTC()
	s.rebuildMu.Unlock()
}

func (s *Server) resolveBookPath(book *database.Book) (string, error) {
	if book == nil {
		return "", fmt.Errorf("book is nil")
	}

	current := strings.TrimSpace(book.Path)
	if current == "" {
		return "", fmt.Errorf("book path is empty")
	}

	if _, err := os.Stat(current); err == nil {
		return current, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	root := strings.TrimSpace(os.Getenv("BOOK_PATH"))
	base := filepath.Base(current)
	if base == "" || strings.EqualFold(base, ".") || strings.EqualFold(base, string(filepath.Separator)) {
		return "", fmt.Errorf("book file missing and cannot infer filename from path %q", current)
	}

	roots := make([]string, 0, 4)
	if root != "" {
		roots = append(roots, root)
	}
	roots = append(roots, "./books")
	if ancestor := nearestExistingDir(current); ancestor != "" {
		roots = append(roots, ancestor)
	}
	if parent := nearestExistingDir(filepath.Dir(current)); parent != "" {
		roots = append(roots, parent)
	}

	recovered, err := findEPUBByBaseNameAcrossRoots(base, roots)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", current, os.ErrNotExist)
	}

	if err := s.db.UpdateBookPath(book.ID, recovered); err != nil {
		log.Printf("failed to update recovered book path for id=%d: %v", book.ID, err)
	} else {
		book.Path = recovered
		log.Printf("recovered missing book path for id=%d: %s -> %s", book.ID, current, recovered)
	}

	return recovered, nil
}

func findEPUBByBaseNameAcrossRoots(base string, roots []string) (string, error) {
	seen := map[string]struct{}{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		cleanRoot := filepath.Clean(root)
		if _, ok := seen[cleanRoot]; ok {
			continue
		}
		seen[cleanRoot] = struct{}{}

		if info, err := os.Stat(cleanRoot); err != nil || !info.IsDir() {
			continue
		}
		match, err := findEPUBByBaseName(cleanRoot, base)
		if err == nil && match != "" {
			return match, nil
		}
	}
	return "", os.ErrNotExist
}

func findEPUBByBaseName(root, base string) (string, error) {
	target := strings.ToLower(strings.TrimSpace(base))
	if target == "" {
		return "", os.ErrNotExist
	}

	var match string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".epub") {
			return nil
		}
		if strings.ToLower(d.Name()) == target {
			match = path
			return io.EOF
		}
		return nil
	})
	if errors.Is(err, io.EOF) && match != "" {
		return match, nil
	}
	if match != "" {
		return match, nil
	}
	return "", os.ErrNotExist
}

func nearestExistingDir(path string) string {
	p := filepath.Clean(strings.TrimSpace(path))
	if p == "" {
		return ""
	}
	for {
		info, err := os.Stat(p)
		if err == nil && info.IsDir() {
			return p
		}
		next := filepath.Dir(p)
		if next == p {
			return ""
		}
		p = next
	}
}
