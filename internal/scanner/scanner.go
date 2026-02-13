package scanner

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
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
	Meta        []struct {
		Name    string `xml:"name,attr"`
		Content string `xml:"content,attr"`
	} `xml:"metadata>meta"`
	Manifest []struct {
		ID         string `xml:"id,attr"`
		Href       string `xml:"href,attr"`
		Properties string `xml:"properties,attr"`
		MediaType  string `xml:"media-type,attr"`
	} `xml:"manifest>item"`
}

type EPUBMetadata struct {
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

type MetadataUpdate struct {
	Title       string
	Creator     string
	Language    string
	Identifier  string
	Publisher   string
	Date        string
	Description string
	Subjects    []string
	Series      string
	SeriesIndex string
}

type CoverOption struct {
	ZipPath   string `json:"zip_path"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	IsCurrent bool   `json:"is_current"`
}

var (
	errMetadataTagNotFound = errors.New("metadata section not found in OPF")
)

func ErrMetadataTagNotFound() error {
	return errMetadataTagNotFound
}

func ExtractMetadata(path string) (*OPF, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	opfPath, err := findOPFPath(reader.File)
	if err != nil {
		return nil, err
	}
	if opfPath == "" {
		return nil, nil
	}

	for _, f := range reader.File {
		if f.Name == opfPath {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()

			var opf OPF
			if err := xml.NewDecoder(rc).Decode(&opf); err != nil {
				return nil, err
			}
			return &opf, nil
		}
	}

	return nil, nil
}

func ExtractLiveMetadata(epubPath string) (*EPUBMetadata, error) {
	opfContent, _, err := readOPFContent(epubPath)
	if err != nil {
		return nil, err
	}

	metaBlock, err := extractMetadataBlock(opfContent)
	if err != nil {
		return nil, err
	}

	subjects := extractAllTagValues(metaBlock, "subject")
	identifier := extractPreferredIdentifier(metaBlock)

	return &EPUBMetadata{
		Title:       extractFirstTagValue(metaBlock, "title"),
		Author:      extractFirstTagValue(metaBlock, "creator"),
		Language:    extractFirstTagValue(metaBlock, "language"),
		Identifier:  identifier,
		Publisher:   extractFirstTagValue(metaBlock, "publisher"),
		Date:        extractFirstTagValue(metaBlock, "date"),
		Description: extractFirstTagValue(metaBlock, "description"),
		Subjects:    subjects,
		Series:      extractMetaContentByName(metaBlock, "calibre:series"),
		SeriesIndex: extractMetaContentByName(metaBlock, "calibre:series_index"),
	}, nil
}

func UpdateEPUBMetadata(epubPath string, update MetadataUpdate) (*EPUBMetadata, error) {
	reader, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	opfPath, err := findOPFPath(reader.File)
	if err != nil {
		return nil, err
	}
	if opfPath == "" {
		return nil, fmt.Errorf("opf package document not found")
	}

	tempFile, err := os.CreateTemp(filepath.Dir(epubPath), ".gopds-*.epub")
	if err != nil {
		return nil, err
	}
	tempPath := tempFile.Name()
	cleanupTemp := true
	defer func() {
		tempFile.Close()
		if cleanupTemp {
			_ = os.Remove(tempPath)
		}
	}()

	writer := zip.NewWriter(tempFile)
	for _, f := range reader.File {
		h := f.FileHeader
		dst, err := writer.CreateHeader(&h)
		if err != nil {
			_ = writer.Close()
			return nil, err
		}

		src, err := f.Open()
		if err != nil {
			_ = writer.Close()
			return nil, err
		}

		if f.Name == opfPath {
			opfContent, err := io.ReadAll(src)
			src.Close()
			if err != nil {
				_ = writer.Close()
				return nil, err
			}

			updatedContent, err := rewriteOPFMetadata(opfContent, update)
			if err != nil {
				_ = writer.Close()
				return nil, err
			}

			if _, err := dst.Write(updatedContent); err != nil {
				_ = writer.Close()
				return nil, err
			}
			continue
		}

		if _, err := io.Copy(dst, src); err != nil {
			src.Close()
			_ = writer.Close()
			return nil, err
		}
		src.Close()
	}

	if err := writer.Close(); err != nil {
		return nil, err
	}
	if err := tempFile.Close(); err != nil {
		return nil, err
	}

	if err := os.Rename(tempPath, epubPath); err != nil {
		return nil, err
	}
	cleanupTemp = false

	return ExtractLiveMetadata(epubPath)
}

func readOPFContent(epubPath string) ([]byte, string, error) {
	reader, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, "", err
	}
	defer reader.Close()

	opfPath, err := findOPFPath(reader.File)
	if err != nil {
		return nil, "", err
	}
	if opfPath == "" {
		return nil, "", fmt.Errorf("opf package document not found")
	}

	for _, f := range reader.File {
		if f.Name == opfPath {
			rc, err := f.Open()
			if err != nil {
				return nil, "", err
			}
			defer rc.Close()
			content, err := io.ReadAll(rc)
			if err != nil {
				return nil, "", err
			}
			return content, opfPath, nil
		}
	}

	return nil, "", fmt.Errorf("opf package document not found")
}

func findOPFPath(files []*zip.File) (string, error) {
	for _, f := range files {
		if f.Name == "META-INF/container.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()

			var c Container
			if err := xml.NewDecoder(rc).Decode(&c); err != nil {
				return "", err
			}
			if len(c.Rootfiles) > 0 {
				return c.Rootfiles[0].FullPath, nil
			}
			return "", nil
		}
	}

	for _, f := range files {
		if strings.HasSuffix(strings.ToLower(f.Name), ".opf") {
			return f.Name, nil
		}
	}
	return "", nil
}

func rewriteOPFMetadata(opfContent []byte, update MetadataUpdate) ([]byte, error) {
	metadataInner, start, end, err := metadataInnerBlock(opfContent)
	if err != nil {
		return nil, err
	}

	changed := false
	newInner := metadataInner

	newInner, changed = setSingleTag(newInner, "title", update.Title, changed)
	newInner, changed = setSingleTag(newInner, "creator", update.Creator, changed)
	newInner, changed = setSingleTag(newInner, "language", update.Language, changed)
	newInner, changed = setSingleTag(newInner, "identifier", update.Identifier, changed)
	newInner, changed = setSingleTag(newInner, "publisher", update.Publisher, changed)
	newInner, changed = setSingleTag(newInner, "date", update.Date, changed)
	newInner, changed = setSingleTag(newInner, "description", update.Description, changed)
	newInner, changed = setMultiTag(newInner, "subject", update.Subjects, changed)
	newInner, changed = setMetaNameContent(newInner, "calibre:series", update.Series, changed)
	newInner, changed = setMetaNameContent(newInner, "calibre:series_index", update.SeriesIndex, changed)

	if !changed {
		return nil, errMetadataTagNotFound
	}

	result := make([]byte, 0, len(opfContent)-len(metadataInner)+len(newInner))
	result = append(result, opfContent[:start]...)
	result = append(result, newInner...)
	result = append(result, opfContent[end:]...)
	return result, nil
}

func metadataInnerBlock(content []byte) ([]byte, int, int, error) {
	// Some EPUBs namespace OPF tags (e.g. <opf:metadata>...</opf:metadata>).
	re := regexp.MustCompile(`(?is)<(?:[a-zA-Z_][\w.-]*:)?metadata\b[^>]*>(.*?)</(?:[a-zA-Z_][\w.-]*:)?metadata>`)
	idx := re.FindSubmatchIndex(content)
	if idx == nil || len(idx) < 4 {
		return nil, 0, 0, errMetadataTagNotFound
	}
	return content[idx[2]:idx[3]], idx[2], idx[3], nil
}

func extractMetadataBlock(content []byte) ([]byte, error) {
	inner, _, _, err := metadataInnerBlock(content)
	return inner, err
}

func extractFirstTagValue(metadata []byte, tag string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(fmt.Sprintf(`(?is)<dc:%s\b[^>]*>(.*?)</dc:%s>`, regexp.QuoteMeta(tag), regexp.QuoteMeta(tag))),
		regexp.MustCompile(fmt.Sprintf(`(?is)<%s\b[^>]*>(.*?)</%s>`, regexp.QuoteMeta(tag), regexp.QuoteMeta(tag))),
	}
	for _, re := range patterns {
		m := re.FindSubmatch(metadata)
		if len(m) >= 2 {
			return cleanXMLValue(string(m[1]))
		}
	}
	return ""
}

func extractAllTagValues(metadata []byte, tag string) []string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(fmt.Sprintf(`(?is)<dc:%s\b[^>]*>(.*?)</dc:%s>`, regexp.QuoteMeta(tag), regexp.QuoteMeta(tag))),
		regexp.MustCompile(fmt.Sprintf(`(?is)<%s\b[^>]*>(.*?)</%s>`, regexp.QuoteMeta(tag), regexp.QuoteMeta(tag))),
	}

	matches := make([][]byte, 0)
	for _, re := range patterns {
		for _, m := range re.FindAllSubmatch(metadata, -1) {
			if len(m) >= 2 {
				matches = append(matches, m[1])
			}
		}
	}

	out := make([]string, 0, len(matches))
	seen := map[string]struct{}{}
	for _, raw := range matches {
		v := cleanXMLValue(string(raw))
		if v == "" {
			continue
		}
		if _, exists := seen[v]; exists {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func extractPreferredIdentifier(metadata []byte) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?is)<dc:identifier\b([^>]*)>(.*?)</dc:identifier>`),
		regexp.MustCompile(`(?is)<identifier\b([^>]*)>(.*?)</identifier>`),
	}

	first := ""
	for _, re := range patterns {
		matches := re.FindAllSubmatch(metadata, -1)
		for _, m := range matches {
			if len(m) < 3 {
				continue
			}
			attrs := strings.ToLower(string(m[1]))
			value := cleanXMLValue(string(m[2]))
			if value == "" {
				continue
			}
			if first == "" {
				first = value
			}
			if strings.Contains(attrs, "isbn") || strings.Contains(strings.ToLower(value), "isbn") {
				return value
			}
		}
	}

	return first
}

func extractMetaContentByName(metadata []byte, name string) string {
	re := regexp.MustCompile(`(?is)<(?:[a-zA-Z_][\w.-]*:)?meta\b([^>]*)/?>`)
	matches := re.FindAllSubmatch(metadata, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		attrs := string(m[1])
		metaName := strings.TrimSpace(strings.ToLower(extractAttrValue(attrs, "name")))
		if metaName != strings.ToLower(name) {
			continue
		}
		return cleanXMLValue(extractAttrValue(attrs, "content"))
	}
	return ""
}

func extractAttrValue(attrs, key string) string {
	doubleQuoted := regexp.MustCompile(fmt.Sprintf(`(?is)\b%s\s*=\s*"(.*?)"`, regexp.QuoteMeta(key)))
	if m := doubleQuoted.FindStringSubmatch(attrs); len(m) >= 2 {
		return m[1]
	}

	singleQuoted := regexp.MustCompile(fmt.Sprintf(`(?is)\b%s\s*=\s*'(.*?)'`, regexp.QuoteMeta(key)))
	if m := singleQuoted.FindStringSubmatch(attrs); len(m) >= 2 {
		return m[1]
	}

	return ""
}

func cleanXMLValue(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	re := regexp.MustCompile(`(?is)<[^>]+>`)
	s = re.ReplaceAllString(s, "")
	return strings.TrimSpace(html.UnescapeString(s))
}

func setSingleTag(metadata []byte, tag string, value string, changed bool) ([]byte, bool) {
	value = strings.TrimSpace(value)
	patterns := []struct {
		re     *regexp.Regexp
		prefix string
	}{
		{
			re:     regexp.MustCompile(fmt.Sprintf(`(?is)<dc:%s\b[^>]*>.*?</dc:%s>`, regexp.QuoteMeta(tag), regexp.QuoteMeta(tag))),
			prefix: "dc:" + tag,
		},
		{
			re:     regexp.MustCompile(fmt.Sprintf(`(?is)<%s\b[^>]*>.*?</%s>`, regexp.QuoteMeta(tag), regexp.QuoteMeta(tag))),
			prefix: tag,
		},
	}

	foundPrefix := ""
	for _, p := range patterns {
		if p.re.Match(metadata) {
			metadata = p.re.ReplaceAll(metadata, []byte(""))
			if foundPrefix == "" {
				foundPrefix = p.prefix
			}
			changed = true
		}
	}

	if foundPrefix != "" {
		if value != "" {
			escaped, _ := xmlEscape(value)
			metadata = append(metadata, []byte("\n<"+foundPrefix+">"+escaped+"</"+foundPrefix+">")...)
		}
		return metadata, true
	}

	if value != "" {
		escaped, _ := xmlEscape(value)
		metadata = append(metadata, []byte("\n<dc:"+tag+">"+escaped+"</dc:"+tag+">")...)
		return metadata, true
	}

	return metadata, changed
}

func setMultiTag(metadata []byte, tag string, values []string, changed bool) ([]byte, bool) {
	prefix := "dc:" + tag
	dcRe := regexp.MustCompile(fmt.Sprintf(`(?is)<dc:%s\b[^>]*>.*?</dc:%s>`, regexp.QuoteMeta(tag), regexp.QuoteMeta(tag)))
	plainRe := regexp.MustCompile(fmt.Sprintf(`(?is)<%s\b[^>]*>.*?</%s>`, regexp.QuoteMeta(tag), regexp.QuoteMeta(tag)))
	if dcRe.Match(metadata) {
		metadata = dcRe.ReplaceAll(metadata, []byte(""))
		changed = true
	}
	if plainRe.Match(metadata) {
		metadata = plainRe.ReplaceAll(metadata, []byte(""))
		prefix = tag
		changed = true
	}

	cleaned := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		cleaned = append(cleaned, v)
	}

	for _, v := range cleaned {
		escaped, _ := xmlEscape(v)
		metadata = append(metadata, []byte("\n<"+prefix+">"+escaped+"</"+prefix+">")...)
		changed = true
	}

	return metadata, changed
}

func setMetaNameContent(metadata []byte, name, value string, changed bool) ([]byte, bool) {
	value = strings.TrimSpace(value)
	doubleQuoted := regexp.MustCompile(`(?is)<(?:[a-zA-Z_][\w.-]*:)?meta\b[^>]*name\s*=\s*"` + regexp.QuoteMeta(name) + `"[^>]*/?>`)
	singleQuoted := regexp.MustCompile(`(?is)<(?:[a-zA-Z_][\w.-]*:)?meta\b[^>]*name\s*=\s*'` + regexp.QuoteMeta(name) + `'[^>]*/?>`)
	if doubleQuoted.Match(metadata) {
		metadata = doubleQuoted.ReplaceAll(metadata, []byte(""))
		changed = true
	}
	if singleQuoted.Match(metadata) {
		metadata = singleQuoted.ReplaceAll(metadata, []byte(""))
		changed = true
	}

	if value != "" {
		escaped, _ := xmlEscape(value)
		metadata = append(metadata, []byte("\n<meta name=\""+name+"\" content=\""+escaped+"\"/>")...)
		changed = true
	}

	return metadata, changed
}

func xmlEscape(s string) (string, error) {
	var b bytes.Buffer
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return "", err
	}
	return b.String(), nil
}

type Scanner struct {
	db *database.DB
}

func New(db *database.DB) *Scanner {
	return &Scanner{db: db}
}

func (s *Scanner) Start(root string) error {
	realPath, err := filepath.EvalSymlinks(root)
	if err != nil {
		log.Printf("❌ Error resolving symlink %s: %v", root, err)
		return err
	}

	log.Printf("🚀 Starting scan of %s (resolved to: %s)...", root, realPath)
	start := time.Now()
	usePathCategories := isPathCategoryEnabled()

	stats := struct {
		Total     int
		Rescanned int
		NoMeta    int
		NoCover   int
	}{}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

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
		if usePathCategories {
			book.Category, book.Subcategory = categoriesFromPath(realPath, path)
		}

		id, err := s.db.SaveBookTx(tx, book)
		if err != nil {
			log.Printf("❌ Error saving book to DB: %v", err)
			return nil
		}

		if err := SaveCover(path, int(id)); err != nil {
			stats.NoCover++
		}

		return nil
	})
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	elapsed := time.Since(start)
	log.Printf("\n--- 🏁 Scan Complete (%v) ---", elapsed)
	log.Printf("Total Books Found:  %d", stats.Total)
	log.Printf("New/Updated:       %d", stats.Rescanned)
	log.Printf("Missing Metadata:   %d (Used filename instead)", stats.NoMeta)
	log.Printf("Missing Covers:     %d", stats.NoCover)
	log.Printf("-------------------------------\n")

	return nil
}

func isPathCategoryEnabled() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("CATEGORY_FROM_PATH")))
	return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
}

func categoriesFromPath(root, bookPath string) (string, string) {
	root = filepath.Clean(root)
	bookPath = filepath.Clean(bookPath)
	rel, err := filepath.Rel(root, bookPath)
	if err != nil {
		return "", ""
	}
	dir := filepath.Dir(rel)
	if dir == "." || dir == string(filepath.Separator) {
		return "", ""
	}
	parts := strings.Split(filepath.ToSlash(dir), "/")
	if len(parts) == 0 {
		return "", ""
	}
	category := strings.TrimSpace(parts[0])
	subcategory := ""
	if len(parts) > 1 {
		subcategory = strings.TrimSpace(parts[1])
	}
	return category, subcategory
}

func SaveCover(epubPath string, bookID int) error {
	localCoverPath := filepath.Join(filepath.Dir(epubPath), "cover.jpg")
	if info, err := os.Stat(localCoverPath); err == nil && !info.IsDir() {
		return saveExternalCover(localCoverPath, bookID)
	}

	reader, err := zip.OpenReader(epubPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, f := range reader.File {
		if isPreferredCoverFilename(f.Name) {
			return extractZipFile(f, bookID)
		}
	}

	for _, f := range reader.File {
		low := strings.ToLower(f.Name)
		if (strings.Contains(low, "cover") || strings.Contains(low, "folder")) &&
			(strings.HasSuffix(low, ".jpg") || strings.HasSuffix(low, ".jpeg") || strings.HasSuffix(low, ".png")) {
			return extractZipFile(f, bookID)
		}
	}

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
		for _, item := range opf.Manifest {
			if strings.Contains(item.Properties, "cover-image") {
				coverHref = item.Href
				break
			}
		}
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

func ListCoverOptions(epubPath string) ([]CoverOption, error) {
	reader, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	opfPath, err := findOPFPath(reader.File)
	if err != nil {
		return nil, err
	}
	if opfPath == "" {
		return nil, fmt.Errorf("opf package document not found")
	}

	opfContent, err := readZipEntry(reader.File, opfPath)
	if err != nil {
		return nil, err
	}
	var opf OPF
	if err := xml.Unmarshal(opfContent, &opf); err != nil {
		return nil, err
	}

	opfDir := filepath.Dir(opfPath)
	currentCoverPath := detectCurrentCoverZipPath(opf, opfDir)

	all := make([]CoverOption, 0, 12)
	suitable := make([]CoverOption, 0, 8)

	for _, item := range opf.Manifest {
		mt := strings.ToLower(strings.TrimSpace(item.MediaType))
		if mt != "image/jpeg" && mt != "image/jpg" && mt != "image/png" {
			continue
		}
		zipPath := normalizeZipPath(filepath.Join(opfDir, item.Href))
		if zipPath == "" {
			continue
		}

		raw, err := readZipEntry(reader.File, zipPath)
		if err != nil {
			continue
		}

		cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
			continue
		}

		opt := CoverOption{
			ZipPath:   zipPath,
			Name:      filepath.Base(zipPath),
			MediaType: mt,
			Width:     cfg.Width,
			Height:    cfg.Height,
			IsCurrent: zipPath == currentCoverPath,
		}
		all = append(all, opt)
		if isSuitableCoverDimension(cfg.Width, cfg.Height) {
			suitable = append(suitable, opt)
		}
	}

	if len(suitable) > 0 {
		return suitable, nil
	}
	return all, nil
}

func ReadCoverOption(epubPath, zipPath string) ([]byte, string, error) {
	reader, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, "", err
	}
	defer reader.Close()

	normalized := normalizeZipPath(zipPath)
	if normalized == "" {
		return nil, "", fmt.Errorf("invalid cover path")
	}

	for _, f := range reader.File {
		if normalizeZipPath(f.Name) != normalized {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, "", err
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			return nil, "", err
		}
		return b, mediaTypeFromPath(f.Name), nil
	}

	return nil, "", os.ErrNotExist
}

func WriteCoverToEPUB(epubPath, selectedZipPath string) error {
	reader, err := zip.OpenReader(epubPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	opfPath, err := findOPFPath(reader.File)
	if err != nil {
		return err
	}
	if opfPath == "" {
		return fmt.Errorf("opf package document not found")
	}
	opfContent, err := readZipEntry(reader.File, opfPath)
	if err != nil {
		return err
	}
	var opf OPF
	if err := xml.Unmarshal(opfContent, &opf); err != nil {
		return err
	}
	opfDir := filepath.Dir(opfPath)
	canonicalCoverPath := normalizeZipPath(filepath.Join(opfDir, "cover.jpg"))
	canonicalHref := relativeHrefFromOPFDir(opfDir, canonicalCoverPath)
	updatedOPF, err := rewriteOPFCoverReference(opfContent, canonicalHref)
	if err != nil {
		return err
	}

	selectedRaw, err := readZipEntry(reader.File, normalizeZipPath(selectedZipPath))
	if err != nil {
		return err
	}

	img, _, err := image.Decode(bytes.NewReader(selectedRaw))
	if err != nil {
		return fmt.Errorf("selected cover decode failed: %w", err)
	}

	rewritten, err := encodeImageForMediaType(img, "image/jpeg", canonicalCoverPath)
	if err != nil {
		return err
	}

	tempFile, err := os.CreateTemp(filepath.Dir(epubPath), ".gopds-cover-*.epub")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	cleanupTemp := true
	defer func() {
		_ = tempFile.Close()
		if cleanupTemp {
			_ = os.Remove(tempPath)
		}
	}()

	writer := zip.NewWriter(tempFile)
	removePaths := collectExistingCoverPaths(opf, opfDir)
	delete(removePaths, canonicalCoverPath)

	wroteCover := false
	wroteOPF := false
	for _, f := range reader.File {
		normalized := normalizeZipPath(f.Name)

		if normalized == normalizeZipPath(opfPath) {
			h := f.FileHeader
			dst, err := writer.CreateHeader(&h)
			if err != nil {
				_ = writer.Close()
				return err
			}
			if _, err := dst.Write(updatedOPF); err != nil {
				_ = writer.Close()
				return err
			}
			wroteOPF = true
			continue
		}

		if normalized == canonicalCoverPath {
			h := f.FileHeader
			dst, err := writer.CreateHeader(&h)
			if err != nil {
				_ = writer.Close()
				return err
			}
			if _, err := dst.Write(rewritten); err != nil {
				_ = writer.Close()
				return err
			}
			wroteCover = true
			continue
		}

		if _, drop := removePaths[normalized]; drop {
			continue
		}

		h := f.FileHeader
		dst, err := writer.CreateHeader(&h)
		if err != nil {
			_ = writer.Close()
			return err
		}
		src, err := f.Open()
		if err != nil {
			_ = writer.Close()
			return err
		}
		if _, err := io.Copy(dst, src); err != nil {
			src.Close()
			_ = writer.Close()
			return err
		}
		src.Close()
	}

	if !wroteOPF {
		_ = writer.Close()
		return fmt.Errorf("opf package document missing during rewrite")
	}
	if !wroteCover {
		dst, err := writer.Create(canonicalCoverPath)
		if err != nil {
			_ = writer.Close()
			return err
		}
		if _, err := dst.Write(rewritten); err != nil {
			_ = writer.Close()
			return err
		}
	}

	if err := writer.Close(); err != nil {
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, epubPath); err != nil {
		return err
	}
	cleanupTemp = false
	return nil
}

func ConvertImageToJPEG(raw []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func isSuitableCoverDimension(width, height int) bool {
	if width < 240 || height < 320 {
		return false
	}
	ratio := float64(width) / float64(height)
	return ratio >= 0.55 && ratio <= 0.85
}

func detectCurrentCoverZipPath(opf OPF, opfDir string) string {
	for _, item := range opf.Manifest {
		p := normalizeZipPath(filepath.Join(opfDir, item.Href))
		if isPreferredCoverFilename(p) {
			return p
		}
	}

	for _, item := range opf.Manifest {
		if strings.Contains(strings.ToLower(item.Properties), "cover-image") {
			return normalizeZipPath(filepath.Join(opfDir, item.Href))
		}
	}

	var coverID string
	for _, m := range opf.Meta {
		if strings.EqualFold(strings.TrimSpace(m.Name), "cover") {
			coverID = strings.TrimSpace(m.Content)
			break
		}
	}
	if coverID != "" {
		for _, item := range opf.Manifest {
			if strings.TrimSpace(item.ID) == coverID {
				return normalizeZipPath(filepath.Join(opfDir, item.Href))
			}
		}
	}
	return ""
}

func resolveWritableCoverTarget(opf OPF, opfDir string) (string, string) {
	for _, item := range opf.Manifest {
		p := normalizeZipPath(filepath.Join(opfDir, item.Href))
		if !isPreferredCoverFilename(p) {
			continue
		}
		mt := strings.ToLower(strings.TrimSpace(item.MediaType))
		if mt == "" {
			mt = mediaTypeFromPath(p)
		}
		return p, mt
	}

	current := detectCurrentCoverZipPath(opf, opfDir)
	if current != "" {
		for _, item := range opf.Manifest {
			p := normalizeZipPath(filepath.Join(opfDir, item.Href))
			if p == current {
				return current, strings.ToLower(strings.TrimSpace(item.MediaType))
			}
		}
		return current, mediaTypeFromPath(current)
	}

	for _, item := range opf.Manifest {
		mt := strings.ToLower(strings.TrimSpace(item.MediaType))
		if mt == "image/jpeg" || mt == "image/jpg" || mt == "image/png" {
			return normalizeZipPath(filepath.Join(opfDir, item.Href)), mt
		}
	}
	return "", ""
}

func collectExistingCoverPaths(opf OPF, opfDir string) map[string]struct{} {
	out := map[string]struct{}{}

	for _, item := range opf.Manifest {
		p := normalizeZipPath(filepath.Join(opfDir, item.Href))
		if p == "" {
			continue
		}
		lprops := strings.ToLower(strings.TrimSpace(item.Properties))
		if strings.Contains(lprops, "cover-image") || isPreferredCoverFilename(p) {
			out[p] = struct{}{}
		}
	}

	current := detectCurrentCoverZipPath(opf, opfDir)
	if current != "" {
		out[current] = struct{}{}
	}

	return out
}

func relativeHrefFromOPFDir(opfDir, fullPath string) string {
	opfDir = normalizeZipPath(opfDir)
	fullPath = normalizeZipPath(fullPath)
	if opfDir == "" || opfDir == "." {
		return fullPath
	}
	prefix := opfDir + "/"
	if strings.HasPrefix(fullPath, prefix) {
		return strings.TrimPrefix(fullPath, prefix)
	}
	return filepath.Base(fullPath)
}

func rewriteOPFCoverReference(opfContent []byte, canonicalHref string) ([]byte, error) {
	updated := opfContent

	// Normalize metadata cover marker to a single <meta name="cover" content="cover-image"/>.
	metaInner, mStart, mEnd, err := metadataInnerBlock(updated)
	if err != nil {
		return nil, err
	}
	metaTagRe := regexp.MustCompile(`(?is)<(?:[a-zA-Z_][\w.-]*:)?meta\b[^>]*?/?>`)
	newMeta := metaTagRe.ReplaceAllFunc(metaInner, func(tag []byte) []byte {
		attrs := string(tag)
		name := strings.ToLower(strings.TrimSpace(extractAttrValue(attrs, "name")))
		if name == "cover" {
			return []byte("")
		}
		return tag
	})
	newMeta = append(newMeta, []byte(``+"\n"+`<meta name="cover" content="cover-image"/>`)...)

	updated = append(append([]byte{}, updated[:mStart]...), append(newMeta, updated[mEnd:]...)...)

	// Normalize manifest cover marker to a single canonical cover item.
	manifestRe := regexp.MustCompile(`(?is)<manifest\b[^>]*>(.*?)</manifest>`)
	manifestIdx := manifestRe.FindSubmatchIndex(updated)
	if manifestIdx == nil || len(manifestIdx) < 4 {
		return nil, fmt.Errorf("manifest section not found in OPF")
	}
	manifestInner := updated[manifestIdx[2]:manifestIdx[3]]

	itemRe := regexp.MustCompile(`(?is)<(?:[a-zA-Z_][\w.-]*:)?item\b[^>]*?/?>`)
	kept := itemRe.ReplaceAllFunc(manifestInner, func(tag []byte) []byte {
		attrs := string(tag)
		id := strings.ToLower(strings.TrimSpace(extractAttrValue(attrs, "id")))
		href := strings.TrimSpace(extractAttrValue(attrs, "href"))
		properties := strings.ToLower(strings.TrimSpace(extractAttrValue(attrs, "properties")))
		if id == "cover-image" || strings.Contains(properties, "cover-image") || isPreferredCoverFilename(href) {
			return []byte("")
		}
		return tag
	})

	escapedHref, _ := xmlEscape(canonicalHref)
	coverItem := []byte(`` + "\n" + `<item id="cover-image" href="` + escapedHref + `" media-type="image/jpeg" properties="cover-image"/>`)
	newManifestInner := append(kept, coverItem...)

	rebuilt := make([]byte, 0, len(updated)-len(manifestInner)+len(newManifestInner))
	rebuilt = append(rebuilt, updated[:manifestIdx[2]]...)
	rebuilt = append(rebuilt, newManifestInner...)
	rebuilt = append(rebuilt, updated[manifestIdx[3]:]...)

	return rebuilt, nil
}

func encodeImageForMediaType(img image.Image, mediaType, targetPath string) ([]byte, error) {
	mt := strings.ToLower(strings.TrimSpace(mediaType))
	if mt == "" {
		mt = mediaTypeFromPath(targetPath)
	}
	var out bytes.Buffer
	switch mt {
	case "image/png":
		if err := png.Encode(&out, img); err != nil {
			return nil, err
		}
	default:
		if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 90}); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

func mediaTypeFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	default:
		return "image/jpeg"
	}
}

func isPreferredCoverFilename(path string) bool {
	base := strings.ToLower(strings.TrimSpace(filepath.Base(path)))
	return base == "cover.jpg" || base == "cover.jpeg" || base == "cover.png"
}

func readZipEntry(files []*zip.File, path string) ([]byte, error) {
	target := normalizeZipPath(path)
	for _, f := range files {
		if normalizeZipPath(f.Name) != target {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	return nil, os.ErrNotExist
}

func normalizeZipPath(path string) string {
	p := strings.ReplaceAll(path, "\\", "/")
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "./")
	p = filepath.ToSlash(filepath.Clean(p))
	p = strings.TrimPrefix(p, "/")
	if p == "." {
		return ""
	}
	return p
}

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
