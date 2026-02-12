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
	// Resolve the symlink to the real path
	realPath, err := filepath.EvalSymlinks(root)
	if err != nil {
		log.Printf("❌ Error resolving symlink %s: %v", root, err)
		return err
	}

	log.Printf("🚀 Starting scan of %s (resolved to: %s)...", root, realPath)
	start := time.Now()

	stats := struct {
		Total     int
		Rescanned int
		NoMeta    int
		NoCover   int
	}{}

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
		if err != nil || meta == nil || meta.Title == "" {
			stats.NoMeta++
			log.Printf("⚠  Metadata missing for %s, using filename.", d.Name())
			meta = &OPF{
				Title:   strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())),
				Creator: "Unknown Author",
			}
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
			log.Printf("❌ Error saving book to DB: %v", err)
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
	// --- PRIORITY 1: EXTERNAL FILESYSTEM ---
	// Check for cover.jpg sitting in the folder on the NAS.
	// This ensures we get the high-res 30KB+ file you manually or API downloaded.
	localCoverPath := filepath.Join(filepath.Dir(epubPath), "cover.jpg")
	if info, err := os.Stat(localCoverPath); err == nil && !info.IsDir() {
		return saveExternalCover(localCoverPath, bookID)
	}

	// --- PRIORITY 2: INTERNAL EXTRACTION ---
	// If no external cover exists, open the ZIP to find internal art.
	reader, err := zip.OpenReader(epubPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	// A. Check for common file names inside the zip (cover.jpg, folder.jpg, etc)
	for _, f := range reader.File {
		low := strings.ToLower(f.Name)
		if (strings.Contains(low, "cover") || strings.Contains(low, "folder")) &&
			(strings.HasSuffix(low, ".jpg") || strings.HasSuffix(low, ".jpeg") || strings.HasSuffix(low, ".png")) {
			return extractZipFile(f, bookID)
		}
	}

	// B. Parse the OPF to find the officially designated cover
	var opfPath string
	for _, f := range reader.File {
		if strings.HasSuffix(f.Name, ".opf") {
			opfPath = f.Name
			break
		}
	}

	if opfPath != "" {
		rc, _ := reader.Open(opfPath)
		var opf OPF
		xml.NewDecoder(rc).Decode(&opf)
		rc.Close()

		var coverHref string
		// Try EPUB 3 manifest properties
		for _, item := range opf.Manifest {
			if strings.Contains(item.Properties, "cover-image") {
				coverHref = item.Href
				break
			}
		}
		// Try EPUB 2 meta tags
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

		if coverHref != "" {
			baseDir := filepath.Dir(opfPath)
			fullCoverPath := filepath.ToSlash(filepath.Join(baseDir, coverHref))
			for _, f := range reader.File {
				if f.Name == fullCoverPath || f.Name == coverHref {
					return extractZipFile(f, bookID)
				}
			}
		}
	}

	return fmt.Errorf("no cover found for %s", epubPath)
}

// Helper to copy an existing high-quality cover.jpg from the NAS to the data directory
func saveExternalCover(srcPath string, bookID int) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	os.MkdirAll("./data/covers", 0755)
	dstPath := fmt.Sprintf("./data/covers/%d.jpg", bookID)
	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

// Helper to extract a single file from the EPUB zip to the data directory
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
