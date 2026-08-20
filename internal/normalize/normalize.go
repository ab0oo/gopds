// SPDX-License-Identifier: MIT

// Package normalize contains deterministic, offline metadata cleanups.
//
// Everything here is a pure function of the input string: no network calls, no
// external lookups, no guessing. That is deliberate. These rules run on every
// scan, so they must be idempotent and must never need a human to confirm them.
// Anything that cannot be decided with certainty is reported as needing review
// rather than being rewritten on a hunch.
package normalize

import (
	"regexp"
	"strings"
)

// Review flags describing why a value could not be safely normalized.
const (
	ReviewAuthorMissing   = "author_missing"
	ReviewAuthorSuspect   = "author_suspect"
	ReviewAuthorAmbiguous = "author_ambiguous"
	ReviewTitleSuspect    = "title_suspect"
)

// nameSuffixes are trailing name parts that look like a "Last, First" inversion
// but are not: "King, Jr." must stay as-is.
var nameSuffixes = map[string]struct{}{
	"jr": {}, "sr": {}, "ii": {}, "iii": {}, "iv": {}, "v": {},
	"phd": {}, "md": {}, "esq": {}, "dds": {},
}

var (
	multiAuthorRe = regexp.MustCompile(`(?i)(\s&\s|\band\b|;|\bwith\b)`)
	writingAsRe   = regexp.MustCompile(`(?i)writing as`)
	hasDigitRe    = regexp.MustCompile(`\d`)
	seriesJunkRe  = regexp.MustCompile(`\s-\s|\(`)
	allCapsWordRe = regexp.MustCompile(`[A-Z]{4,}`)
	initialRe     = regexp.MustCompile(`^[A-Z]\.?$`)
	whitespaceRe  = regexp.MustCompile(`\s+`)
)

// AuthorResult carries the normalized author plus any review flag raised.
type AuthorResult struct {
	Value   string
	Flag    string
	Changed bool
}

// Author canonicalizes a creator string to "First Last" form and repairs casing.
//
// It refuses to transform anything it cannot prove is a simple inversion:
// multi-author strings, pseudonym phrasing, name suffixes, and author fields
// polluted with series text ("Bujold, Lois McMaster - Vorkosigan 04") are left
// untouched so a later human pass can decide.
func Author(raw string) AuthorResult {
	original := strings.TrimSpace(raw)
	if original == "" {
		return AuthorResult{Value: original, Flag: ReviewAuthorMissing}
	}

	// A creator that is entirely digits or punctuation is corrupt metadata,
	// not a name. Seen in the wild as author="3".
	if !strings.ContainsFunc(original, func(r rune) bool {
		return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
	}) {
		return AuthorResult{Value: original, Flag: ReviewAuthorSuspect}
	}

	out := collapseSpace(original)
	flag := ""

	// Step 1: invert "Last, First" when it is unambiguously that shape.
	if inverted, ok := invertCommaName(out); ok {
		out = inverted
	} else if strings.Contains(out, ",") {
		// Has a comma but did not qualify for inversion -> a human should look.
		flag = ReviewAuthorAmbiguous
	}

	// Step 2: repair shouting. Applied after inversion so "GERARD, CINDY"
	// becomes "Cindy Gerard" rather than stopping at "CINDY GERARD".
	out = fixAllCaps(out)

	// Step 3: an all-caps name with no comma may also be reversed
	// ("BROWN SANDRA"), but word order cannot be recovered without a name
	// database, so flag instead of guessing.
	if flag == "" && isShouted(original) && !strings.Contains(original, ",") {
		flag = ReviewAuthorAmbiguous
	}

	return AuthorResult{Value: out, Flag: flag, Changed: out != original}
}

func invertCommaName(s string) (string, bool) {
	if strings.Count(s, ",") != 1 {
		return s, false
	}
	if multiAuthorRe.MatchString(s) || writingAsRe.MatchString(s) {
		return s, false
	}

	parts := strings.SplitN(s, ",", 2)
	last := strings.TrimSpace(parts[0])
	first := strings.TrimSpace(parts[1])
	if last == "" || first == "" {
		return s, false
	}

	// "King, Jr." is a suffix, not an inversion.
	if _, isSuffix := nameSuffixes[strings.ToLower(strings.Trim(first, ". "))]; isSuffix {
		return s, false
	}

	// Series or annotation junk hiding in the creator field.
	if seriesJunkRe.MatchString(first) || hasDigitRe.MatchString(first) {
		return s, false
	}

	return first + " " + last, true
}

// isShouted reports whether a string is written in shouting caps. It tolerates
// small interior lowercase runs so that "LYN McCONCHIE" still counts: the test
// is that the string contains a long all-caps run and has no ordinary
// lowercase word of its own.
func isShouted(s string) bool {
	if len(s) <= 3 || !allCapsWordRe.MatchString(s) {
		return false
	}
	for _, w := range strings.Fields(s) {
		trimmed := strings.Trim(w, ".,'-")
		if trimmed == "" {
			continue
		}
		// A word with no uppercase at all means the string is mixed-case prose,
		// not shouting.
		if trimmed == strings.ToLower(trimmed) {
			return false
		}
	}
	return true
}

func fixAllCaps(s string) string {
	if !isShouted(s) {
		return s
	}
	words := strings.Fields(s)
	for i, w := range words {
		if initialRe.MatchString(w) {
			continue // keep "C." in "Arthur C. Clarke"
		}
		words[i] = titleWord(w)
	}
	return strings.Join(words, " ")
}

// titleWord capitalizes a single word, preserving interior capitals that are
// part of the name itself ("McConchie", "O'Brien").
func titleWord(w string) string {
	lower := strings.ToLower(w)
	runes := []rune(lower)
	upperNext := true
	for i, r := range runes {
		if upperNext && r >= 'a' && r <= 'z' {
			runes[i] = r - 32
			upperNext = false
			continue
		}
		if r == '\'' || r == '-' || r == '.' {
			upperNext = true
			continue
		}
		// "Mc"/"Mac" prefixes capitalize the following letter.
		if i == 1 && (lower == "mc" || strings.HasPrefix(lower, "mc")) {
			upperNext = true
			continue
		}
		upperNext = false
	}
	out := string(runes)
	if strings.HasPrefix(lower, "mc") && len(out) > 2 {
		out = out[:2] + strings.ToUpper(out[2:3]) + out[3:]
	}
	return out
}

func collapseSpace(s string) string {
	return strings.TrimSpace(whitespaceRe.ReplaceAllString(s, " "))
}

// TitleResult carries a cleaned title plus any series data lifted out of it.
type TitleResult struct {
	Title       string
	Series      string
	SeriesIndex string
	Flag        string
	Changed     bool
}

var (
	// "(Hercule Poirot Series Book 1)", "(Harlequin Crew #2)", "[Vega Jane, Book 4]"
	seriesWithNameRe = regexp.MustCompile(`(?i)\s*[\(\[]\s*([^)\]]*?)[,\s]+(?:#|book\s*|vol\.?\s*|part\s*)\s*(\d+(?:\.\d+)?)\s*[\)\]]\s*$`)
	// "(Book 3)", "(#2)" with no series name attached
	seriesBareNumRe = regexp.MustCompile(`(?i)\s*[\(\[]\s*(?:#|book\s*|vol\.?\s*)\s*(\d+(?:\.\d+)?)\s*[\)\]]\s*$`)
	hashLikeRe      = regexp.MustCompile(`^[0-9a-f]{16,}$`)
)

// Title strips trailing series annotations from a title and returns them as
// structured series fields. Existing series values take precedence: this only
// fills gaps, it never overwrites data the EPUB already declared.
func Title(raw, existingSeries, existingIndex string) TitleResult {
	original := strings.TrimSpace(raw)
	if original == "" {
		return TitleResult{Title: original, Flag: ReviewTitleSuspect}
	}

	res := TitleResult{
		Title:       collapseSpace(original),
		Series:      sanitizeSeriesName(existingSeries),
		SeriesIndex: strings.TrimSpace(existingIndex),
	}

	if m := seriesWithNameRe.FindStringSubmatch(res.Title); m != nil {
		name := collapseSpace(m[1])
		// Guard against eating a real subtitle: require something name-shaped.
		if name != "" && !hasDigitRe.MatchString(name) {
			res.Title = strings.TrimSpace(res.Title[:len(res.Title)-len(m[0])])
			if res.Series == "" {
				res.Series = name
			}
			if res.SeriesIndex == "" {
				res.SeriesIndex = m[2]
			}
		}
	} else if m := seriesBareNumRe.FindStringSubmatch(res.Title); m != nil {
		res.Title = strings.TrimSpace(res.Title[:len(res.Title)-len(m[0])])
		if res.SeriesIndex == "" {
			res.SeriesIndex = m[1]
		}
	}

	res.Title = fixAllCaps(res.Title)

	// A title that is a bare hash or that we emptied out is not usable.
	if res.Title == "" {
		res.Title = collapseSpace(original)
		res.Flag = ReviewTitleSuspect
	} else if hashLikeRe.MatchString(strings.ToLower(res.Title)) {
		res.Flag = ReviewTitleSuspect
	}

	res.Changed = res.Title != original ||
		res.Series != strings.TrimSpace(existingSeries) ||
		res.SeriesIndex != strings.TrimSpace(existingIndex)
	return res
}

// sanitizeSeriesName rejects series values that are really genre labels.
// Some EPUBs put "Horror" or "Fantasy" in calibre:series, which would render
// as a nonsense series card grouping unrelated books together.
func sanitizeSeriesName(raw string) string {
	name := collapseSpace(raw)
	if name == "" {
		return ""
	}
	if CanonicalGenre(name) != "" && len(strings.Fields(name)) <= 2 {
		return ""
	}
	return name
}
