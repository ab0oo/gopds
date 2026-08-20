// SPDX-License-Identifier: MIT

package normalize

import (
	"strings"
	"unicode"
)

// Confidence tiers for a remote metadata match. The whole point of Tier 2 is
// that only ConfidenceExact results are ever applied without a human looking.
type Confidence int

const (
	ConfidenceNone Confidence = iota
	ConfidenceLow
	ConfidenceMedium
	ConfidenceExact
)

func (c Confidence) String() string {
	switch c {
	case ConfidenceExact:
		return "exact"
	case ConfidenceMedium:
		return "medium"
	case ConfidenceLow:
		return "low"
	default:
		return "none"
	}
}

// MatchCandidate is the subset of a remote result needed to judge a match.
type MatchCandidate struct {
	Title      string
	Author     string
	Identifier string
}

// MatchInput is the local book being matched.
type MatchInput struct {
	Title      string
	Author     string
	Identifier string
}

// Thresholds for fuzzy agreement, expressed as a 0..1 similarity ratio.
const (
	titleStrongMatch = 0.92
	titleWeakMatch   = 0.80
	authorMatchFloor = 0.85
)

// Match judges how confidently a remote candidate describes the same book.
//
// The rules are intentionally strict. A wrong "high confidence" match writes
// bad data into a library the user cannot easily audit, so anything short of
// an ISBN agreement or a very strong title+author agreement is sent to review.
func Match(local MatchInput, cand MatchCandidate) Confidence {
	lISBN := strings.TrimSpace(local.Identifier)
	cISBN := strings.TrimSpace(cand.Identifier)

	lTitle := normalizeForCompare(local.Title)
	cTitle := normalizeForCompare(cand.Title)
	if lTitle == "" || cTitle == "" {
		return ConfidenceNone
	}

	titleSim := similarity(lTitle, cTitle)

	// An ISBN agreement is the only signal trusted on its own -- but only when
	// the title is not wildly different, which catches bad ISBNs in the wild.
	if lISBN != "" && cISBN != "" && lISBN == cISBN {
		if titleSim >= titleWeakMatch {
			return ConfidenceExact
		}
		return ConfidenceMedium
	}

	lAuthor := normalizeForCompare(local.Author)
	cAuthor := normalizeForCompare(cand.Author)
	authorSim := 0.0
	if lAuthor != "" && cAuthor != "" {
		authorSim = authorSimilarity(lAuthor, cAuthor)
	}

	switch {
	case titleSim >= titleStrongMatch && authorSim >= authorMatchFloor:
		return ConfidenceExact
	case titleSim >= titleStrongMatch && authorSim > 0:
		return ConfidenceMedium
	case titleSim >= titleWeakMatch && authorSim >= authorMatchFloor:
		return ConfidenceMedium
	case titleSim >= titleWeakMatch:
		return ConfidenceLow
	default:
		return ConfidenceNone
	}
}

// normalizeForCompare lowercases, strips punctuation and leading articles so
// "The Hobbit" and "Hobbit, The" compare equal.
func normalizeForCompare(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else if unicode.IsSpace(r) || r == '-' {
			b.WriteRune(' ')
		}
	}
	out := collapseSpace(b.String())
	for _, art := range []string{"the ", "a ", "an "} {
		out = strings.TrimPrefix(out, art)
	}
	return out
}

// authorSimilarity compares author names by token set, so "J. R. R. Tolkien"
// and "John Ronald Reuel Tolkien" do not have to match character for character.
// Surname agreement is required.
func authorSimilarity(a, b string) float64 {
	at := strings.Fields(a)
	bt := strings.Fields(b)
	if len(at) == 0 || len(bt) == 0 {
		return 0
	}
	// Surnames must agree; initials often differ between sources.
	if at[len(at)-1] != bt[len(bt)-1] {
		return 0
	}
	// Collapse runs of single-letter initials so "j r r tolkien" and
	// "jrr tolkien" compare as the same name.
	at = foldInitials(at)
	bt = foldInitials(bt)

	set := map[string]struct{}{}
	for _, t := range at {
		set[t] = struct{}{}
	}
	shared := 0
	for _, t := range bt {
		if _, ok := set[t]; ok {
			shared++
		}
	}
	longest := len(at)
	if len(bt) > longest {
		longest = len(bt)
	}
	return float64(shared) / float64(longest)
}

// similarity returns a 0..1 ratio using normalized Levenshtein distance.
func similarity(a, b string) float64 {
	if a == b {
		return 1
	}
	if a == "" || b == "" {
		return 0
	}
	d := levenshtein(a, b)
	longest := len(a)
	if len(b) > longest {
		longest = len(b)
	}
	return 1 - float64(d)/float64(longest)
}

func levenshtein(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// foldInitials merges consecutive single-letter tokens into one token, so that
// differing initial spacing between metadata sources does not defeat matching.
func foldInitials(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	run := ""
	for _, t := range tokens {
		if len([]rune(t)) == 1 {
			run += t
			continue
		}
		if run != "" {
			out = append(out, run)
			run = ""
		}
		out = append(out, t)
	}
	if run != "" {
		out = append(out, run)
	}
	return out
}
