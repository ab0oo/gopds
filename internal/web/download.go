// SPDX-License-Identifier: MIT

package web

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ab0oo/gopds/internal/database"
	"github.com/ab0oo/gopds/internal/normalize"
)

// maxBookNameRunes keeps names well inside filesystem limits (255 bytes
// on most) once ".epub" and multi-byte characters are added.
const maxBookNameRunes = 150

// bookFilename names a book "Author - Title.epub" from its metadata, whatever
// the file is called on the server. Downloads use it as is; emailed books use
// its normalize.ASCIIFilename form.
func bookFilename(book database.Book) string {
	title := cleanFilenamePart(book.Title)
	author := cleanFilenamePart(book.Author)
	if strings.EqualFold(author, "Unknown Author") {
		author = ""
	}
	if title == "" {
		title = cleanFilenamePart(strings.TrimSuffix(filepath.Base(book.Path), filepath.Ext(book.Path)))
	}

	name := title
	switch {
	case author != "" && title != "":
		name = author + " - " + title
	case author != "":
		name = author
	}
	if name == "" {
		name = fmt.Sprintf("book-%d", book.ID)
	}
	if utf8.RuneCountInString(name) > maxBookNameRunes {
		name = strings.TrimRight(string([]rune(name)[:maxBookNameRunes]), " .-")
	}
	return name + ".epub"
}

// cleanFilenamePart removes what Windows, macOS or Linux will not accept in
// a filename. A colon reads as a subtitle separator, so it becomes " -".
func cleanFilenamePart(s string) string {
	s = strings.ReplaceAll(s, ":", " -")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == '|':
			return '-'
		case strings.ContainsRune(`*?"<>`, r), unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	// Windows drops trailing dots and spaces, and a leading dot hides a file.
	return strings.Trim(s, " .")
}

// contentDisposition builds an attachment header carrying the exact UTF-8
// name (RFC 6266 filename*) and an ASCII fallback for older clients.
func contentDisposition(name string) string {
	fallback := normalize.FoldASCII(name, "_")
	fallback = strings.NewReplacer(`"`, "", `\`, "").Replace(fallback)
	if fallback == name {
		return `attachment; filename="` + name + `"`
	}
	return `attachment; filename="` + fallback + `"; filename*=UTF-8''` + encodeRFC5987(name)
}

// encodeRFC5987 percent-encodes everything outside RFC 5987's attr-char set.
func encodeRFC5987(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c < 128 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("!#$&+-.^_`|~", c) >= 0) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
