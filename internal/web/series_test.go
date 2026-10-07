// SPDX-License-Identifier: MIT

package web

import (
	"testing"

	"github.com/ab0oo/gopds/internal/database"
)

func TestMatchSeriesEntries(t *testing.T) {
	entries := []seriesEntry{
		{ID: "Q1", Title: "Wizard's First Rule", Index: "1"},
		{ID: "Q2", Title: "Stone of Tears", Index: "2"},
		{ID: "Q3", Title: "Blood of the Fold", Index: "3"},
		{ID: "Q4", Title: "The First Confessor: The Legend of Magda Searus", Index: "-2"},
	}
	anchor := database.Book{ID: 10, Title: "Wizards First Rule", Author: "Terry Goodkind"}
	books := []database.Book{
		anchor,
		{ID: 11, Title: "Stone of Tears", Author: "Goodkind, Terry"},
		{ID: 12, Title: "Blood of the Fold", Author: "Someone Else"},
		{ID: 13, Title: "The First Confessor", Author: "Terry Goodkind"},
		{ID: 14, Title: "Stone of Tears", Author: "Terry Goodkind"},
		{ID: 15, Title: "The Law of Nines", Author: "Terry Goodkind"},
	}

	matched := matchSeriesEntries(entries, books, anchor, "Q1")

	want := map[string][]int{"Q1": {10}, "Q2": {11, 14}, "Q3": {}, "Q4": {13}}
	total := 0
	for _, e := range entries {
		got := []int{}
		for _, m := range e.Matches {
			got = append(got, m.BookID)
		}
		total += len(got)
		if len(got) != len(want[e.ID]) {
			t.Errorf("%s (%s): matched %v, want %v", e.ID, e.Title, got, want[e.ID])
			continue
		}
		for i := range got {
			if got[i] != want[e.ID][i] {
				t.Errorf("%s (%s): matched %v, want %v", e.ID, e.Title, got, want[e.ID])
			}
		}
	}
	if matched != total {
		t.Errorf("matched count = %d, want %d", matched, total)
	}
}

func TestMatchSeriesEntriesPlacesAnchorByTitle(t *testing.T) {
	entries := []seriesEntry{{ID: "Q1", Title: "Dune", Index: "1"}}
	anchor := database.Book{ID: 3, Title: "Dune", Author: "Frank Herbert"}
	// The anchor's Wikidata volume is unknown, so it must be found by title,
	// and exactly once.
	if n := matchSeriesEntries(entries, []database.Book{anchor}, anchor, ""); n != 1 {
		t.Fatalf("matched = %d, want 1", n)
	}
}

func TestSameAuthor(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"Terry Goodkind", "Terry Goodkind", true},
		{"Goodkind, Terry", "Terry Goodkind", true},
		{"Terry Goodkind", "Terry Pratchett", false},
		{"", "Terry Goodkind", false},
	}
	for _, c := range cases {
		if got := sameAuthor(c.a, c.b); got != c.want {
			t.Errorf("sameAuthor(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
