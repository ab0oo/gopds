package web

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"time"

	"github.com/ab0oo/gopds/internal/database"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// OPDS 1.2 XML Structures
type Feed struct {
	XMLName xml.Name `xml:"feed"`
	Xmlns   string   `xml:"xmlns,attr"`
	Title   string   `xml:"title"`
	ID      string   `xml:"id"`
	Updated string   `xml:"updated"`
	Entries []Entry  `xml:"entry"`
}

type Entry struct {
	Title   string `xml:"title"`
	ID      string `xml:"id"`
	Updated string `xml:"updated"`
	Author  string `xml:"author>name"`
	Summary string `xml:"summary"`
	Links   []Link `xml:"link"`
}

type Link struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
	Type string `xml:"type,attr"`
}

type Server struct {
	db *database.DB
}

func NewServer(db *database.DB) *Server {
	return &Server{db: db}
}

// Router sets up the URL paths for the OPDS feed and file serving
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.Logger)    // Log every request to the terminal
	r.Use(middleware.Recoverer) // Don't let a panic kill the whole app

	r.Get("/opds", s.HandleCatalog)
	r.Get("/download/{id}", s.HandleDownload)
	r.Get("/covers/{id}.jpg", s.HandleCover)

	return r
}

// HandleCatalog generates the Atom XML feed for the OPDS reader
func (s *Server) HandleCatalog(w http.ResponseWriter, r *http.Request) {
	books, err := s.db.GetAllBooks()
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	feed := Feed{
		Xmlns:   "http://www.w3.org/2005/Atom",
		Title:   "GoPDS Library",
		ID:      "main-catalog",
		Updated: time.Now().Format(time.RFC3339),
	}

	for _, b := range books {
		entry := Entry{
			Title:   b.Title,
			ID:      fmt.Sprintf("book:%d", b.ID),
			Updated: b.ModTime.Format(time.RFC3339),
			Author:  b.Author,
			Summary: b.Description,
		}

		// 1. Download Link
		entry.Links = append(entry.Links, Link{
			Rel:  "http://opds-spec.org/acquisition",
			Href: fmt.Sprintf("/download/%d", b.ID),
			Type: "application/epub+zip",
		})

		// 2. Cover Image Link
		entry.Links = append(entry.Links, Link{
			Rel:  "http://opds-spec.org/image",
			Href: fmt.Sprintf("/covers/%d.jpg", b.ID),
			Type: "image/jpeg",
		})

		feed.Entries = append(feed.Entries, entry)
	}

	w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog;kind=acquisition")

	enc := xml.NewEncoder(w)
	enc.Indent("", "  ") // This is the correct method name for XML
	if err := enc.Encode(feed); err != nil {
		// If you use 'log', make sure it's in your imports
		fmt.Printf("Error encoding XML: %v\n", err)
	}
}

// HandleDownload streams the actual EPUB file to the user
func (s *Server) HandleDownload(w http.ResponseWriter, r *http.Request) {
	bookID := chi.URLParam(r, "id")
	book, err := s.db.GetBookByID(bookID)
	if err != nil {
		http.Error(w, "Book not found", http.StatusNotFound)
		return
	}

	filename := fmt.Sprintf("%s.epub", book.Title)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.Header().Set("Content-Type", "application/epub+zip")

	http.ServeFile(w, r, book.Path)
}

// HandleCover serves the extracted .jpg from the data directory
func (s *Server) HandleCover(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	path := fmt.Sprintf("./data/covers/%s.jpg", id)
	
	// http.ServeFile handles the "404 Not Found" automatically if the image doesn't exist
	http.ServeFile(w, r, path)
}
