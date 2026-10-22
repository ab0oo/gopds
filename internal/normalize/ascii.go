// SPDX-License-Identifier: MIT

package normalize

import (
	"path/filepath"
	"regexp"
	"strings"
)

var asciiFolds = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'æ': "ae", 'ç': "c",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ì': "i", 'í': "i", 'î': "i", 'ï': "i",
	'ñ': "n", 'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'ù': "u",
	'ú': "u", 'û': "u", 'ü': "u", 'ý': "y", 'ÿ': "y", 'ß': "ss", 'œ': "oe",
	'À': "A", 'Á': "A", 'Â': "A", 'Ã': "A", 'Ä': "A", 'Å': "A", 'Æ': "AE", 'Ç': "C",
	'È': "E", 'É': "E", 'Ê': "E", 'Ë': "E", 'Ì': "I", 'Í': "I", 'Î': "I", 'Ï': "I",
	'Ñ': "N", 'Ò': "O", 'Ó': "O", 'Ô': "O", 'Õ': "O", 'Ö': "O", 'Ø': "O", 'Ù': "U",
	'Ú': "U", 'Û': "U", 'Ü': "U", 'Ý': "Y", 'Œ': "OE",
	'‘': "'", '’': "'", '“': "\"", '”': "\"", '–': "-", '—': "-", '…': "...",
}

// FoldASCII replaces common accented letters and typographic punctuation with
// plain ASCII equivalents ("Außenseiter" -> "Aussenseiter", "Don’t" ->
// "Don't"). Characters with no equivalent become replacement.
func FoldASCII(s, replacement string) string {
	var b strings.Builder
	for _, r := range s {
		switch f, ok := asciiFolds[r]; {
		case r < 128:
			b.WriteRune(r)
		case ok:
			b.WriteString(f)
		default:
			b.WriteString(replacement)
		}
	}
	return b.String()
}

var unsafeFilenameRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// maxASCIIStemBytes caps the name before its extension.
const maxASCIIStemBytes = 120

// ASCIIFilename reduces a filename to letters, digits, ".", "_" and "-" for
// receivers that reject anything else, such as Send to Kindle:
// "Piers Anthony - Demons Don’t Dream.epub" ->
// "Piers_Anthony_-_Demons_Dont_Dream.epub". A name with nothing left becomes
// "book" plus its extension.
func ASCIIFilename(name string) string {
	ext := filepath.Ext(name)
	stem := strings.ReplaceAll(FoldASCII(strings.TrimSuffix(name, ext), "_"), "'", "")
	stem = strings.Trim(unsafeFilenameRe.ReplaceAllString(stem, "_"), "._-")
	if len(stem) > maxASCIIStemBytes {
		stem = strings.TrimRight(stem[:maxASCIIStemBytes], "._-")
	}
	if stem == "" {
		stem = "book"
	}
	return stem + ext
}
