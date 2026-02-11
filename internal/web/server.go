package web

import (
	"embed"
	"fmt"
	"io/fs"
	"html"
	"log"
	"net/http"

	"github.com/ab0oo/gopds/internal/database"
	"github.com/go-chi/chi/middleware"
	"github.com/go-chi/chi/v5"
)

type Server struct {
	db   *database.DB
	uiFS embed.FS // Store it in the struct
}

func NewServer(db *database.DB, uiFS embed.FS) *Server {
	return &Server{
		db:   db,
		uiFS: uiFS,
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	// 1. Middlewares
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer) // The Safety Net

	// 2. Sub-filesystem logic
	publicFS, err := fs.Sub(s.uiFS, "web/ui")
	if err != nil {
		log.Fatalf("FS Sub Error: %v", err)
	}

	// DEBUG: Run this once to see what Go actually embedded
	entries, _ := fs.ReadDir(publicFS, ".")
	for _, e := range entries {
		log.Printf("DEBUG: Found in publicFS: %s", e.Name())
	}

	// 3. API Routes
	r.Get("/opds", s.HandleCatalog)
	r.Get("/covers/{id}.jpg", s.HandleCover)
	r.Get("/download/{id}", s.HandleDownload)

	// 4. Static Files
	r.Handle("/*", http.FileServer(http.FS(publicFS)))

	return r
}

func (s *Server) HandleCatalog(w http.ResponseWriter, r *http.Request) {
    books, err := s.db.GetAllBooks()
    if err != nil {
        http.Error(w, "Internal Server Error", 500)
        return
    }

    w.Header().Set("Content-Type", "application/xml")
    fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><feed xmlns="http://www.w3.org/2005/Atom"><title>GoPDS Library</title>`)

    for _, b := range books {
        // ESCAPE the strings to turn '&' into '&amp;' etc.
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

func (s *Server) HandleCover(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id") // This comes in as a string from the URL
	coverPath := fmt.Sprintf("data/covers/%s.jpg", id)
	http.ServeFile(w, r, coverPath)
}

func (s *Server) HandleDownload(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// 1. Look up the book in the DB to get the physical file path
	book, err := s.db.GetBookByID(id) // Ensure this method exists!
	if err != nil {
		log.Printf("Download error (ID %s): %v", id, err)
		http.Error(w, "Book not found", http.StatusNotFound)
		return
	}

	// 2. Set headers so the browser/reader knows it's a file download
	// We use the book title for the filename to make it user-friendly
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.epub\"", book.Title))
	w.Header().Set("Content-Type", "application/epub+zip")

	// 3. Serve the file from the absolute path on your NAS/Disk
	http.ServeFile(w, r, book.Path)
}
