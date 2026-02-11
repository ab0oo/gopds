package scanner

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ab0oo/gopds/internal/database"
)

// EPUB internal XML structures
type Container struct {
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type OPF struct {
	Title       string `xml:"metadata>title"`
	Creator     string `xml:"metadata>creator"`
	Description string `xml:"metadata>description"`
	// For EPUB 2 cover lookup
	Meta []struct {
		Name    string `xml:"name,attr"`
		Content string `xml:"content,attr"`
	} `xml:"metadata>meta"`
	// For EPUB 3 cover lookup
	Manifest []struct {
		ID         string `xml:"id,attr"`
		Href       string `xml:"href,attr"`
		Properties string `xml:"properties,attr"`
		MediaType  string `xml:"media-type,attr"`
	} `xml:"manifest>item"`
}

func ExtractMetadata(path string) (*OPF, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	// 1. Find the OPF file path via container.xml
	var opfPath string
	for _, f := range reader.File {
		if f.Name == "META-INF/container.xml" {
			rc, _ := f.Open()
			var c Container
			xml.NewDecoder(rc).Decode(&c)
			rc.Close()
			if len(c.Rootfiles) > 0 {
				opfPath = c.Rootfiles[0].FullPath
			}
			break
		}
	}

	// 2. Parse the OPF file
	for _, f := range reader.File {
		if f.Name == opfPath {
			rc, _ := f.Open()
			defer rc.Close()
			var opf OPF
			xml.NewDecoder(rc).Decode(&opf)
			return &opf, nil
		}
	}

	return nil, nil
}

type Scanner struct {
	db *database.DB
}

func New(db *database.DB) *Scanner {
	return &Scanner{db: db}
}

func (s *Scanner) Start(root string) error {
	// 1. Resolve the symlink to the real path
	realPath, err := filepath.EvalSymlinks(root)
	if err != nil {
		log.Printf("❌ Error resolving symlink %s: %v", root, err)
		return err
	}
    
	// Use realPath instead of root for the walk
	log.Printf("🚀 Starting scan of %s (resolved to: %s)...", root, realPath)

	stats := struct {
		Total    int
		Rescanned int
		NoMeta   int
		NoCover  int
	}{}

	log.Printf("🚀 Starting scan of %s (resolved to: %s)...", root, realPath)
	start := time.Now()

	err = filepath.WalkDir(realPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".epub") {
			return nil
		}

		stats.Total++
		info, _ := d.Info()

		if !s.db.NeedsReScan(path, info.ModTime()) {
			return nil
		}
		stats.Rescanned++

		meta, err := ExtractMetadata(path)
		if err != nil || meta.Title == "" {
			stats.NoMeta++
			log.Printf("⚠️  Metadata missing for %s, using filename.", d.Name())
			meta = &OPF{Title: strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())), Creator: "Unknown Author"}
		}

		book := database.Book{
			Path:        path,
			Title:       meta.Title,
			Author:      meta.Creator,
			Description: meta.Description,
			ModTime:     info.ModTime(),
		}

		id, err := s.db.SaveBook(book)
		if err != nil {
			return nil
		}

		if err := SaveCover(path, int(id)); err != nil {
			stats.NoCover++
		}

		return nil
	})

	elapsed := time.Since(start)
	log.Printf("\n--- 🏁 Scan Complete (%v) ---", elapsed)
	log.Printf("Total Books Found:  %d", stats.Total)
	log.Printf("New/Updated:       %d", stats.Rescanned)
	log.Printf("Missing Metadata:   %d (Used filename instead)", stats.NoMeta)
	log.Printf("Missing Covers:     %d", stats.NoCover)
	log.Printf("-------------------------------\n")

	return err
}
func SaveCover(epubPath string, bookID int) error {
	reader, err := zip.OpenReader(epubPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	// 1. Find the .opf file (the "map" of the book)
	var opfPath string
	for _, f := range reader.File {
		if strings.HasSuffix(f.Name, ".opf") {
			opfPath = f.Name
			break
		}
	}

	if opfPath == "" {
		return fmt.Errorf("could not find OPF file")
	}

	// 2. Parse the OPF to find the cover reference
	opfFile, _ := reader.Open(opfPath)
	var opf OPF
	xml.NewDecoder(opfFile).Decode(&opf)
	opfFile.Close()

	var coverHref string

	// Strategy A: Look for EPUB 3 "cover-image" property
	for _, item := range opf.Manifest {
		if strings.Contains(item.Properties, "cover-image") {
			coverHref = item.Href
			break
		}
	}

	// Strategy B: Look for EPUB 2 <meta name="cover" content="ID">
	if coverHref == "" {
		var coverID string
		for _, m := range opf.Meta {
			if m.Name == "cover" {
				coverID = m.Content
				break
			}
		}
		if coverID != "" {
			for _, item := range opf.Manifest {
				if item.ID == coverID {
					coverHref = item.Href
					break
				}
			}
		}
	}

	// 3. Resolve the path (OPF paths are relative to the OPF file location)
	if coverHref != "" {
		baseDir := filepath.Dir(opfPath)
		fullCoverPath := filepath.Join(baseDir, coverHref)
		// Zip paths use forward slashes even on Windows
		fullCoverPath = filepath.ToSlash(fullCoverPath)

		for _, f := range reader.File {
			if f.Name == fullCoverPath || f.Name == coverHref {
				return extractZipFile(f, bookID)
			}
		}
	}

	// Strategy C: Last Resort - Look for common filenames
	for _, f := range reader.File {
		low := strings.ToLower(f.Name)
		if (strings.Contains(low, "cover") || strings.Contains(low, "folder")) &&
			(strings.HasSuffix(low, ".jpg") || strings.HasSuffix(low, ".jpeg") || strings.HasSuffix(low, ".png")) {
			return extractZipFile(f, bookID)
		}
	}

	return fmt.Errorf("no cover found")
}

// Helper to actually write the file to disk
func extractZipFile(f *zip.File, bookID int) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	os.MkdirAll("./data/covers", 0755)
	out, err := os.Create(fmt.Sprintf("./data/covers/%d.jpg", bookID))
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}
