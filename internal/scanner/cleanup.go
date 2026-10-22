// SPDX-License-Identifier: MIT

package scanner

import (
	"regexp"
	"sort"
	"strings"
)

// CleanupUpdate is a targeted correction to a book's identity. Empty fields
// are left exactly as they are in the EPUB. Authors, when given, is the
// complete author list; illustrators, translators and other contributors are
// never touched.
type CleanupUpdate struct {
	Title       string
	Authors     []string
	Series      string
	SeriesIndex string
}

// UpdateEPUBCleanup writes a CleanupUpdate. Unlike UpdateEPUBMetadata it edits
// elements in place: attributes, co-authors and every field not named in the
// update survive untouched.
func UpdateEPUBCleanup(epubPath string, update CleanupUpdate) (*EPUBMetadata, error) {
	return rewriteEPUBOPF(epubPath, func(opf []byte) ([]byte, error) {
		return rewriteOPFCleanup(opf, update)
	})
}

var (
	fileAsAttrRe = regexp.MustCompile(`(?i)(\bopf:file-as\s*=\s*)("[^"]*"|'[^']*')`)
	idAttrRe     = regexp.MustCompile(`(?i)\bid\s*=\s*["']([^"']+)["']`)
	roleAttrRe   = regexp.MustCompile(`(?i)\b(?:opf:)?role\s*=\s*["']([^"']*)["']`)
	creatorRes   = []*regexp.Regexp{
		regexp.MustCompile(`(?is)<dc:creator\b([^>]*)>(.*?)</dc:creator\s*>`),
		regexp.MustCompile(`(?is)<creator\b([^>]*)>(.*?)</creator\s*>`),
	}
)

// creatorElement is one <dc:creator> in an OPF metadata block.
type creatorElement struct {
	start, end int
	attrs      string
	name       string
	id         string
	author     bool
}

// findCreators lists creator elements in document order and marks which are
// authors: no role at all, or role "aut" given as an attribute (EPUB 2) or a
// refining <meta property="role"> (EPUB 3).
func findCreators(metadata string) []creatorElement {
	var out []creatorElement
	for _, re := range creatorRes {
		for _, loc := range re.FindAllStringSubmatchIndex(metadata, -1) {
			c := creatorElement{
				start: loc[0],
				end:   loc[1],
				attrs: metadata[loc[2]:loc[3]],
				name:  cleanXMLValue(metadata[loc[4]:loc[5]]),
			}
			if m := idAttrRe.FindStringSubmatch(c.attrs); m != nil {
				c.id = m[1]
			}
			role := ""
			if m := roleAttrRe.FindStringSubmatch(c.attrs); m != nil {
				role = m[1]
			} else if c.id != "" {
				refine := regexp.MustCompile(`(?is)<meta\b[^>]*\brefines\s*=\s*["']#` + regexp.QuoteMeta(c.id) + `["'][^>]*\bproperty\s*=\s*["']role["'][^>]*>(.*?)</meta\s*>`)
				if m := refine.FindStringSubmatch(metadata); m != nil {
					role = m[1]
				}
			}
			role = strings.ToLower(strings.TrimSpace(role))
			c.author = role == "" || role == "aut"
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

// primaryAuthor is the first author named in the metadata, falling back to
// the first creator of any role when none is marked as an author.
func primaryAuthor(metadata []byte) string {
	creators := findCreators(string(metadata))
	for _, c := range creators {
		if c.author && c.name != "" {
			return c.name
		}
	}
	for _, c := range creators {
		if c.name != "" {
			return c.name
		}
	}
	return ""
}

// ReadEPUBAuthors returns the book's authors in the order the EPUB lists them.
func ReadEPUBAuthors(epubPath string) ([]string, error) {
	opf, _, err := readOPFContent(epubPath)
	if err != nil {
		return nil, err
	}
	metadata, err := extractMetadataBlock(opf)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range findCreators(string(metadata)) {
		if c.author && c.name != "" {
			out = append(out, c.name)
		}
	}
	return out, nil
}

func rewriteOPFCleanup(opfContent []byte, update CleanupUpdate) ([]byte, error) {
	metadataInner, start, end, err := metadataInnerBlock(opfContent)
	if err != nil {
		return nil, err
	}

	// Copy first: the helpers append, and metadataInner aliases the OPF buffer.
	inner := string(append([]byte(nil), metadataInner...))

	if title := strings.TrimSpace(update.Title); title != "" {
		inner, _ = replaceFirstElementText(inner, "title", title, nil)
		// The sort title was derived from the old title ("Xanth 03 - ...");
		// a stale one would keep sorting the book under its old name.
		b, _ := setMetaNameContent([]byte(inner), "calibre:title_sort", "", false)
		inner = string(b)
	}
	if authors := nonEmpty(update.Authors); len(authors) > 0 {
		inner = replaceAuthors(inner, authors)
	}
	if series := strings.TrimSpace(update.Series); series != "" {
		b, _ := setMetaNameContent([]byte(inner), "calibre:series", series, false)
		inner = string(b)
	}
	if index := strings.TrimSpace(update.SeriesIndex); index != "" {
		b, _ := setMetaNameContent([]byte(inner), "calibre:series_index", index, false)
		inner = string(b)
	}

	result := make([]byte, 0, len(opfContent)-len(metadataInner)+len(inner))
	result = append(result, opfContent[:start]...)
	result = append(result, inner...)
	result = append(result, opfContent[end:]...)
	return result, nil
}

// replaceAuthors makes authors the book's author list. The first existing
// author element keeps its attributes (with its sort name updated); further
// author elements are removed and the remaining names added after it.
func replaceAuthors(metadata string, authors []string) string {
	var existing []creatorElement
	for _, c := range findCreators(metadata) {
		if c.author {
			existing = append(existing, c)
		}
	}
	added := ""
	for _, name := range authors[1:] {
		added += "\n<dc:creator>" + xmlEscapeString(name) + "</dc:creator>"
	}
	if len(existing) == 0 {
		return metadata + "\n<dc:creator>" + xmlEscapeString(authors[0]) + "</dc:creator>" + added
	}

	// Work backwards so earlier offsets stay valid.
	var dropped []string
	for i := len(existing) - 1; i >= 1; i-- {
		c := existing[i]
		metadata = metadata[:c.start] + metadata[c.end:]
		if c.id != "" {
			dropped = append(dropped, c.id)
		}
	}
	first := existing[0]
	sortName := authorSortName(authors[0])
	attrs := fileAsAttrRe.ReplaceAllString(first.attrs, "${1}\""+strings.ReplaceAll(xmlEscapeString(sortName), "$", "$$")+"\"")
	tag := "dc:creator"
	if !strings.HasPrefix(strings.ToLower(metadata[first.start:]), "<dc:") {
		tag = "creator"
	}
	element := "<" + tag + attrs + ">" + xmlEscapeString(authors[0]) + "</" + tag + ">"
	metadata = metadata[:first.start] + element + added + metadata[first.end:]

	if first.id != "" {
		metadata = replaceRefinesFileAs(metadata, first.id, sortName)
	}
	// A removed author's EPUB 3 refinements (role, sort name) would dangle.
	for _, id := range dropped {
		re := regexp.MustCompile(`(?is)\s*<meta\b[^>]*\brefines\s*=\s*["']#` + regexp.QuoteMeta(id) + `["'][^>]*?(?:/>|>.*?</meta\s*>)`)
		metadata = re.ReplaceAllString(metadata, "")
	}
	return metadata
}

func nonEmpty(values []string) []string {
	var out []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// replaceFirstElementText swaps the text of the first <dc:tag> (or bare <tag>)
// element, keeping its attributes, optionally rewritten by editAttrs. With no
// such element, a new <dc:tag> is appended.
func replaceFirstElementText(metadata, tag, value string, editAttrs func(string) string) (string, bool) {
	escaped := xmlEscapeString(value)
	for _, name := range []string{"dc:" + tag, tag} {
		re := regexp.MustCompile(`(?is)<` + regexp.QuoteMeta(name) + `\b([^>]*)>(.*?)</` + regexp.QuoteMeta(name) + `\s*>`)
		loc := re.FindStringSubmatchIndex(metadata)
		if loc == nil {
			continue
		}
		attrs := metadata[loc[2]:loc[3]]
		if editAttrs != nil {
			attrs = editAttrs(attrs)
		}
		element := "<" + name + attrs + ">" + escaped + "</" + name + ">"
		return metadata[:loc[0]] + element + metadata[loc[1]:], true
	}
	return metadata + "\n<dc:" + tag + ">" + escaped + "</dc:" + tag + ">", true
}

// replaceRefinesFileAs updates an EPUB 3 sort name, which lives in a separate
// <meta refines="#id" property="file-as"> element rather than an attribute.
func replaceRefinesFileAs(metadata, id, sortName string) string {
	re := regexp.MustCompile(`(?is)(<meta\b[^>]*\brefines\s*=\s*["']#` + regexp.QuoteMeta(id) + `["'][^>]*\bproperty\s*=\s*["']file-as["'][^>]*>)(.*?)(</meta\s*>)`)
	return re.ReplaceAllString(metadata, "${1}"+strings.ReplaceAll(xmlEscapeString(sortName), "$", "$$")+"${3}")
}

// authorSortName turns "Piers Anthony" into "Anthony, Piers".
func authorSortName(name string) string {
	fields := strings.Fields(name)
	if len(fields) < 2 {
		return name
	}
	return fields[len(fields)-1] + ", " + strings.Join(fields[:len(fields)-1], " ")
}

func xmlEscapeString(s string) string {
	escaped, _ := xmlEscape(s)
	return escaped
}
