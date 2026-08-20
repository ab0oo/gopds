// SPDX-License-Identifier: MIT

package normalize

import "testing"

func TestMatchISBNExact(t *testing.T) {
	local := MatchInput{Title: "Dreamcatcher", Author: "Stephen King", Identifier: "9780743221887"}
	cand := MatchCandidate{Title: "Dreamcatcher", Author: "Stephen King", Identifier: "9780743221887"}
	if got := Match(local, cand); got != ConfidenceExact {
		t.Errorf("ISBN agreement should be exact, got %v", got)
	}
}

func TestMatchISBNAgreesButTitleWildlyDifferent(t *testing.T) {
	// A bad ISBN in the wild must not blindly win.
	local := MatchInput{Title: "Dreamcatcher", Author: "Stephen King", Identifier: "9780743221887"}
	cand := MatchCandidate{Title: "Cooking With Yogurt", Author: "Anon", Identifier: "9780743221887"}
	if got := Match(local, cand); got == ConfidenceExact {
		t.Errorf("mismatched title with same ISBN must not be exact, got %v", got)
	}
}

func TestMatchTitleAuthorExact(t *testing.T) {
	local := MatchInput{Title: "The Hobbit", Author: "J. R. R. Tolkien"}
	cand := MatchCandidate{Title: "The Hobbit", Author: "J.R.R. Tolkien"}
	if got := Match(local, cand); got != ConfidenceExact {
		t.Errorf("strong title+author should be exact, got %v", got)
	}
}

func TestMatchDifferentAuthorSameTitleIsNotExact(t *testing.T) {
	// Many books share a title; the author is what disambiguates.
	local := MatchInput{Title: "Blackout", Author: "Connie Willis"}
	cand := MatchCandidate{Title: "Blackout", Author: "Marc Elsberg"}
	if got := Match(local, cand); got == ConfidenceExact {
		t.Errorf("same title different author must not be exact, got %v", got)
	}
}

func TestMatchUnrelatedIsNone(t *testing.T) {
	local := MatchInput{Title: "Dune", Author: "Frank Herbert"}
	cand := MatchCandidate{Title: "Pride and Prejudice", Author: "Jane Austen"}
	if got := Match(local, cand); got != ConfidenceNone {
		t.Errorf("unrelated books should be none, got %v", got)
	}
}

func TestMatchArticleAndPunctuationInsensitive(t *testing.T) {
	local := MatchInput{Title: "The Left Hand of Darkness", Author: "Ursula K. Le Guin"}
	cand := MatchCandidate{Title: "Left Hand of Darkness, The", Author: "Ursula K Le Guin"}
	if got := Match(local, cand); got < ConfidenceMedium {
		t.Errorf("article/punctuation differences should still match, got %v", got)
	}
}

func TestMatchEmptyTitleIsNone(t *testing.T) {
	if got := Match(MatchInput{Title: ""}, MatchCandidate{Title: "Anything"}); got != ConfidenceNone {
		t.Errorf("empty local title should be none, got %v", got)
	}
}

func TestAuthorSimilarityRequiresSurname(t *testing.T) {
	if authorSimilarity("stephen king", "stephen fry") != 0 {
		t.Error("different surnames must score 0")
	}
	if authorSimilarity("j r r tolkien", "john ronald reuel tolkien") <= 0 {
		t.Error("shared surname with differing given names should score > 0")
	}
}
