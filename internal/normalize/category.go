// SPDX-License-Identifier: MIT

package normalize

import (
	"strings"
	"unicode"
)

// categorySmallWords stay lowercase inside a label, as in "Action & Adventure"
// or "Sword and Sorcery".
var categorySmallWords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "as": {}, "at": {}, "but": {}, "by": {},
	"for": {}, "in": {}, "nor": {}, "of": {}, "on": {}, "or": {}, "the": {},
	"to": {}, "vs": {}, "with": {},
}

// categoryAcronyms are written in capitals whatever case they arrive in, so a
// lowercase "sf" tag files beside "SF".
var categoryAcronyms = map[string]string{
	"sf": "SF", "ya": "YA", "uk": "UK", "usa": "USA",
	"lgbt": "LGBT", "lgbtq": "LGBTQ", "wwi": "WWI", "wwii": "WWII",
}

// CategoryLabel puts a category or subcategory into one standard form, so
// "Fantasy fiction", "fantasy  fiction" and "FANTASY FICTION" all become
// "Fantasy Fiction" and browse as a single entry. Words are title-cased with
// small joining words lowercased; known acronyms are capitalized, and
// deliberate mixed case ("LitRPG") and other short capitals are kept.
func CategoryLabel(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		parts := strings.Split(w, "-")
		for j, p := range parts {
			parts[j] = categoryWord(p, i == 0 && j == 0)
		}
		words[i] = strings.Join(parts, "-")
	}
	return strings.Join(words, " ")
}

func categoryWord(w string, first bool) string {
	runes := []rune(w)
	hasLower, innerUpper, letters := false, false, 0
	for i, r := range runes {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if unicode.IsLower(r) {
			hasLower = true
		} else if i > 0 && unicode.IsUpper(r) {
			innerUpper = true
		}
	}

	lower := strings.ToLower(w)
	if acronym, ok := categoryAcronyms[lower]; ok {
		return acronym
	}
	if _, small := categorySmallWords[lower]; small && !first {
		return lower
	}
	if hasLower && innerUpper {
		return w // "LitRPG", "McCaffrey"
	}
	if !hasLower && letters > 0 && letters <= 3 {
		return w // "YA", "SF", "UK"
	}

	runes = []rune(lower)
	for i, r := range runes {
		if unicode.IsLetter(r) {
			runes[i] = unicode.ToUpper(r)
			break
		}
	}
	return string(runes)
}
