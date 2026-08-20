// SPDX-License-Identifier: MIT

package normalize

import (
	"regexp"
	"strings"
)

// Canonical genres. Deliberately small: a browse list is only useful if each
// entry holds enough books to be worth opening. Anything that cannot be mapped
// with confidence is left uncategorized rather than forced into a bucket.
const (
	GenreScienceFiction = "Science Fiction"
	GenreFantasy        = "Fantasy"
	GenreMystery        = "Mystery"
	GenreThriller       = "Thriller"
	GenreHorror         = "Horror"
	GenreRomance        = "Romance"
	GenreHistorical     = "Historical Fiction"
	GenreAdventure      = "Action & Adventure"
	GenreYoungAdult     = "Young Adult"
	GenreClassics       = "Classics"
	GenreNonFiction     = "Non-Fiction"
	GenreBiography      = "Biography"
	GenreFiction        = "General Fiction"
)

// genreRules maps a normalized subject fragment to a canonical genre. Order
// matters: the first matching rule wins, so more specific terms are listed
// before the general ones they contain.
var genreRules = []struct {
	needles []string
	genre   string
}{
	// Specific subgenres first -- "science fiction" must not be swallowed by
	// the bare "fiction" rule, and "historical romance" is romance.
	{[]string{"urban fantasy", "high fantasy", "sword and sorcery", "epic fantasy",
		"dragons", "mythical", "fantasy", "fae", "magic", "wizard", "fansaty"}, GenreFantasy},
	{[]string{"science fiction", "sci-fi", "scifi", "sci fi", "space opera",
		"cyberpunk", "dystopia", "apocalyptic", "time travel", "speculative",
		"hard sf"}, GenreScienceFiction},
	{[]string{"cozy myster", "detective", "hard-boiled", "hardboiled", "whodunit",
		"murder", "mystery", "crime", "courtroom", "noir"}, GenreMystery},
	{[]string{"techno thriller", "technothriller", "espionage", "spy", "spionage",
		"conspiracy", "suspense", "thriller", "vigilante"}, GenreThriller},
	{[]string{"horror", "vampire", "zombie", "supernatural", "ghost", "occult"}, GenreHorror},
	{[]string{"romance", "erotic", "love stor", "man-woman relationships",
		"contemporary romance", "new adult"}, GenreRomance},
	{[]string{"historical fiction", "historical", "medieval", "war stories",
		"war story", "military", "civil war", "world war"}, GenreHistorical},
	{[]string{"young adult", "juvenile", "children", "middle grade", "coming of age"}, GenreYoungAdult},
	{[]string{"classic", "literature"}, GenreClassics},
	{[]string{"biography", "autobiography", "memoir"}, GenreBiography},
	{[]string{"adventure", "action"}, GenreAdventure},
	{[]string{"non-fiction", "nonfiction", "history", "science", "philosophy",
		"politics", "business", "self-help", "reference", "true crime"}, GenreNonFiction},
	// Bare "fiction" last: it is the weakest possible signal.
	{[]string{"fiction", "novel"}, GenreFiction},
}

// junkSubjects are values that carry no genre meaning. They appear in the wild
// as retailer tags, Calibre artifacts, and age-range markers.
var junkSubjects = map[string]struct{}{
	"": {}, "none": {}, "unknown": {}, "general": {}, "misc": {}, "miscellaneous": {},
	"retail": {}, "new": {}, "ebook": {}, "ebooks": {}, "english ebooks": {},
	"calibre": {}, "adult": {}, "#genre": {}, "f": {}, "ss": {}, "a&a": {},
	"collection": {}, "omnibus": {}, "noc": {}, "usa": {}, "contemporary": {},
	"high tech": {}, "signals , noise": {}, "k'12": {}, "prose": {},
}

var (
	bisacRe     = regexp.MustCompile(`(?i)^[A-Z]{3}\d{6}`)
	ageRangeRe  = regexp.MustCompile(`(?i)age\s*range|older audience|reading level`)
	nonWordTrim = regexp.MustCompile(`[^\p{L}\p{N}\s&'-]+`)
)

// CanonicalGenre maps a raw subject string onto a canonical genre.
// It returns "" when the subject carries no usable genre signal, which the
// caller should treat as "leave uncategorized" rather than as a failure.
func CanonicalGenre(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	if _, junk := junkSubjects[s]; junk {
		return ""
	}
	if ageRangeRe.MatchString(s) {
		return ""
	}

	// BISAC codes ("FIC009000") are real subject codes but opaque to a reader.
	// Map the common fiction prefix and otherwise ignore them.
	if bisacRe.MatchString(s) {
		if strings.HasPrefix(strings.ToUpper(s), "FIC") {
			return GenreFiction
		}
		return ""
	}

	// Keyword-dump subjects ("Lee Child;Jack Reacher;vigilante justice;...")
	// are still worth mining: check every fragment, not just the whole string.
	fragments := splitSubjectFragments(s)

	// "sf" and its Calibre-style variants ("sf_history", "sf_fantasy") are
	// science-fiction tags. Handled before the general rules because the
	// suffix would otherwise match an unrelated genre.
	for _, frag := range fragments {
		if frag == "sf" || strings.HasPrefix(frag, "sf ") || strings.HasPrefix(frag, "sf_") {
			return GenreScienceFiction
		}
	}

	for _, rule := range genreRules {
		for _, needle := range rule.needles {
			for _, frag := range fragments {
				if strings.Contains(frag, needle) {
					return rule.genre
				}
			}
		}
	}
	return ""
}

// splitSubjectFragments breaks a subject on the separators publishers use for
// keyword lists, returning cleaned fragments plus the original string.
func splitSubjectFragments(s string) []string {
	out := []string{s}
	for _, sep := range []string{";", ",", "/", "|", ":", ">"} {
		var next []string
		for _, part := range out {
			next = append(next, strings.Split(part, sep)...)
		}
		out = next
	}

	cleaned := make([]string, 0, len(out))
	seen := map[string]struct{}{}
	for _, frag := range out {
		frag = collapseSpace(nonWordTrim.ReplaceAllString(frag, " "))
		if frag == "" {
			continue
		}
		if _, dup := seen[frag]; dup {
			continue
		}
		seen[frag] = struct{}{}
		cleaned = append(cleaned, frag)
	}
	return cleaned
}

// GenreFromSubjects picks the best canonical genre from a book's subject list,
// preferring the most specific match rather than the first one seen.
func GenreFromSubjects(subjects []string) string {
	// Rule index doubles as a specificity rank: earlier rules are more specific.
	best := ""
	bestRank := len(genreRules) + 1

	for _, subject := range subjects {
		g := CanonicalGenre(subject)
		if g == "" {
			continue
		}
		rank := genreRank(g)
		if rank < bestRank {
			bestRank = rank
			best = g
		}
	}
	return best
}

func genreRank(genre string) int {
	for i, rule := range genreRules {
		if rule.genre == genre {
			return i
		}
	}
	return len(genreRules) + 1
}
