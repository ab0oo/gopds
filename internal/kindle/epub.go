// SPDX-License-Identifier: MIT

// Package kindle prepares EPUBs for Amazon's Send to Kindle service.
//
// Send to Kindle accepts EPUB, but rejects or garbles files that other readers
// take in their stride. The repairs here follow kindle-epub-fix
// (https://github.com/innocenat/kindle-epub-fix), which collects the known
// causes. They are applied to an in-memory copy: the library file is never
// modified.
package kindle

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

// MaxEmailBytes is Send to Kindle's limit on the attachments of one email.
const MaxEmailBytes = 50 << 20

var (
	xmlDeclRe     = regexp.MustCompile(`(?is)^\s*<\?xml\s+version\s*=\s*["'][\d.]+["']\s+encoding\s*=\s*["'][a-zA-Z\d.-]+["'].*?\?>`)
	anyXMLDeclRe  = regexp.MustCompile(`(?is)^\s*<\?xml\b.*?\?>`)
	bodyIDRe      = regexp.MustCompile(`(?is)<body\b[^>]*?\bid\s*=\s*["']([^"']+)["']`)
	imgTagRe      = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	srcAttrRe     = regexp.MustCompile(`(?is)\bsrc\s*=\s*["'][^"']+["']`)
	rootfileRe    = regexp.MustCompile(`(?is)<rootfile\b[^>]*\bfull-path\s*=\s*["']([^"']+)["']`)
	languageTagRe = regexp.MustCompile(`(?is)<dc:language\b[^>]*>(.*?)</dc:language\s*>`)
	metadataEndRe = regexp.MustCompile(`(?is)</(?:opf:)?metadata\s*>`)
)

// supportedLanguages are the languages Kindle converts, from
// https://kdp.amazon.com/en_US/help/topic/G200673300 as listed by
// kindle-epub-fix.
var supportedLanguages = map[string]bool{}

func init() {
	for _, l := range strings.Fields(`af gsw ar eu nb br ca zh kw co da nl stq en fi fr fy gl
		de gu hi is ga it ja lb mr ml gv frr nn pl pt oc rm sco gd es sv ta cy
		afr ara eus baq nob bre cat zho chi cor cos dan nld dut eng fin fra fre fry glg
		deu ger guj hin isl ice gle ita jpn ltz mar mal glv nor nno por oci roh gla spa
		swe tam cym wel`) {
		supportedLanguages[l] = true
	}
}

// Prepared is an EPUB ready to send, plus a note of each repair made.
type Prepared struct {
	Data  []byte
	Fixes []string
	// Warnings are problems found but not fixed, such as a language Kindle
	// does not support.
	Warnings []string
}

// PrepareEPUB reads the EPUB at epubPath and returns a copy repaired for
// Send to Kindle.
func PrepareEPUB(epubPath string) (*Prepared, error) {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	out := &Prepared{}
	edits := map[string][]byte{}

	read := func(f *zip.File) ([]byte, error) {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}

	var pages []*zip.File
	var opfName string
	for _, f := range zr.File {
		switch strings.ToLower(path.Ext(f.Name)) {
		case ".html", ".xhtml", ".htm":
			pages = append(pages, f)
		}
		if f.Name == "META-INF/container.xml" {
			b, err := read(f)
			if err != nil {
				return nil, err
			}
			if m := rootfileRe.FindSubmatch(b); m != nil {
				opfName = string(m[1])
			}
		}
	}

	// Read every page once; the fixes below edit these copies.
	original := map[string][]byte{}
	content := map[string][]byte{}
	for _, f := range pages {
		b, err := read(f)
		if err != nil {
			return nil, err
		}
		original[f.Name] = b
		content[f.Name] = b
	}

	// Links to a page's <body id> show as unresolved on Kindle; point them
	// at the page itself.
	for _, f := range pages {
		if m := bodyIDRe.FindSubmatch(content[f.Name]); m != nil {
			from := path.Base(f.Name) + "#" + string(m[1])
			to := path.Base(f.Name)
			for _, g := range pages {
				if bytes.Contains(content[g.Name], []byte(from)) {
					content[g.Name] = bytes.ReplaceAll(content[g.Name], []byte(from), []byte(to))
					out.Fixes = append(out.Fixes, fmt.Sprintf("replaced link target %s with %s in %s", from, to, g.Name))
				}
			}
		}
	}

	for _, f := range pages {
		b := content[f.Name]

		// <img> without a src makes conversion fail.
		stray := 0
		b = imgTagRe.ReplaceAllFunc(b, func(tag []byte) []byte {
			if srcAttrRe.Match(tag) {
				return tag
			}
			stray++
			return nil
		})
		if stray > 0 {
			out.Fixes = append(out.Fixes, fmt.Sprintf("removed %d stray image tag(s) in %s", stray, f.Name))
		}

		// Without an encoding declaration Send to Kindle assumes ISO-8859-1
		// and mangles every non-ASCII character.
		if !xmlDeclRe.Match(b) {
			trimmed := bytes.TrimLeft(b, " \t\r\n")
			trimmed = bytes.TrimPrefix(trimmed, []byte("\xef\xbb\xbf"))
			if loc := anyXMLDeclRe.FindIndex(trimmed); loc != nil {
				trimmed = bytes.TrimLeft(trimmed[loc[1]:], " \t\r\n")
			}
			b = append([]byte("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n"), trimmed...)
			out.Fixes = append(out.Fixes, "added UTF-8 encoding declaration to "+f.Name)
		}

		if !bytes.Equal(b, original[f.Name]) {
			edits[f.Name] = b
		}
	}

	if opfName != "" {
		for _, f := range zr.File {
			if f.Name != opfName {
				continue
			}
			b, err := read(f)
			if err != nil {
				return nil, err
			}
			if fixed, fix, warning := fixLanguage(b); fix != "" || warning != "" {
				if fix != "" {
					edits[f.Name] = fixed
					out.Fixes = append(out.Fixes, fix)
				}
				if warning != "" {
					out.Warnings = append(out.Warnings, warning)
				}
			}
		}
	}

	data, err := rewriteZip(zr.File, edits)
	if err != nil {
		return nil, err
	}
	out.Data = data
	return out, nil
}

var languageCodeRe = regexp.MustCompile(`[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*`)

// fixLanguage makes dc:language something Kindle accepts. Kindle refuses
// books with no language, so a missing, empty or "und" (undetermined) one
// becomes English, as kindle-epub-fix assumes. Free text that contains a code
// ("US English (en-us)") is reduced to that code. Any other unsupported
// language is reported but left alone, since replacing it would mislabel the
// book.
func fixLanguage(opf []byte) ([]byte, string, string) {
	if loc := languageTagRe.FindSubmatchIndex(opf); loc != nil {
		lang := strings.TrimSpace(string(opf[loc[2]:loc[3]]))
		replace := func(code, why string) ([]byte, string, string) {
			fixed := make([]byte, 0, len(opf))
			fixed = append(fixed, opf[:loc[2]]...)
			fixed = append(fixed, code...)
			fixed = append(fixed, opf[loc[3]:]...)
			return fixed, why, ""
		}
		switch {
		case lang == "" || strings.EqualFold(lang, "und"):
			return replace("en", fmt.Sprintf("set undetermined book language %q to en", lang))
		case supportedLanguages[languageBase(lang)]:
			return opf, "", ""
		}
		for _, code := range languageCodeRe.FindAllString(lang, -1) {
			if code != lang && supportedLanguages[languageBase(code)] && strings.Contains(code, "-") {
				return replace(code, fmt.Sprintf("reduced book language %q to %s", lang, code))
			}
		}
		return opf, "", fmt.Sprintf("language %q is not supported by Kindle; conversion may fail", lang)
	}
	loc := metadataEndRe.FindIndex(opf)
	if loc == nil {
		return opf, "", "book has no language and no metadata section to add one to"
	}
	fixed := make([]byte, 0, len(opf)+40)
	fixed = append(fixed, opf[:loc[0]]...)
	fixed = append(fixed, []byte("<dc:language>en</dc:language>\n")...)
	fixed = append(fixed, opf[loc[0]:]...)
	return fixed, "added missing book language (en)", ""
}

// rewriteZip copies the archive, replacing edited entries. The mimetype entry
// goes first and uncompressed, as the EPUB container format requires;
// untouched entries are copied without recompressing.
func languageBase(code string) string {
	return strings.ToLower(strings.SplitN(strings.TrimSpace(code), "-", 2)[0])
}

func rewriteZip(files []*zip.File, edits map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	ordered := make([]*zip.File, 0, len(files))
	for _, f := range files {
		if f.Name == "mimetype" {
			ordered = append([]*zip.File{f}, ordered...)
		} else {
			ordered = append(ordered, f)
		}
	}

	for _, f := range ordered {
		if f.Name == "mimetype" {
			w, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
			if err != nil {
				return nil, err
			}
			if _, err := w.Write([]byte("application/epub+zip")); err != nil {
				return nil, err
			}
			continue
		}
		// Some tools (older Calibre among them) deflate directory entries,
		// leaving an empty compressed stream that a raw copy cannot write
		// into a directory; recreate them empty instead.
		if strings.HasSuffix(f.Name, "/") {
			if _, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: zip.Store, Modified: f.Modified}); err != nil {
				return nil, err
			}
			continue
		}
		body, edited := edits[f.Name]
		if !edited {
			if err := zw.Copy(f); err != nil {
				return nil, err
			}
			continue
		}
		hdr := f.FileHeader
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(&hdr)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(body); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
