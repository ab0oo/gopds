// SPDX-License-Identifier: MIT

package web

import (
	"fmt"
	"testing"

	"github.com/ab0oo/gopds/internal/database"
)

func TestBuildSeriesCleanup(t *testing.T) {
	entries := []seriesEntry{
		{ID: "Q1", Title: "A Spell for Chameleon", Index: "1"},
		{ID: "Q3", Title: "Castle Roogna", Index: "3"},
		{ID: "Q5", Title: "Ogre, Ogre", Index: "5"},
		{ID: "Q13", Title: "Isle of View", Index: "13"},
		{ID: "Q14", Title: "Question Quest", Index: "14"},
		{ID: "Q19", Title: "Roc and a Hard Place", Index: "19"},
	}
	authors := map[string][]string{}
	for _, e := range entries {
		authors[e.ID] = []string{"Piers Anthony"}
	}
	anchor := database.Book{ID: 3, Title: "Xanth 03 - Castle Roogna", Author: "Piers Anthony"}
	books := []database.Book{
		{ID: 1, Title: "A Spell for Chameleon", Author: "Piers Anthony", Series: "Xanth", SeriesIndex: "1.0"},
		anchor,
		{ID: 5, Title: "Xanth 05 - Ogre Ogre", Author: "Piers Anthony"},
		{ID: 14, Title: "Xanth 14 - Question Quest", Author: "Piers A. Jacob"},
		{ID: 19, Title: "Xanth 19 - Roc & a Hard Place", Author: "Piers Anthony"},
		// In the series by its title, but Wikidata has no such volume.
		{ID: 40, Title: "Xanth 40 - Isis Orb", Author: "Piers Anthony"},
		// Same author, not in the series: left alone.
		{ID: 50, Title: "Tarot 1 - God of Tarot", Author: "Piers Anthony"},
		// Someone else's book that happens to share a volume title.
		{ID: 60, Title: "Question Quest", Author: "Other Writer"},
	}

	prop := buildSeriesCleanup(anchor, "Q3", "Xanth", entries, authors, books, nil)

	if prop.Unchanged != 1 {
		t.Errorf("unchanged = %d, want 1 (book 1 is already clean)", prop.Unchanged)
	}
	want := map[int]cleanupFields{
		3:  {Title: "Castle Roogna", Author: "Piers Anthony", Series: "Xanth", SeriesIndex: "3"},
		5:  {Title: "Ogre, Ogre", Author: "Piers Anthony", Series: "Xanth", SeriesIndex: "5"},
		14: {Title: "Question Quest", Author: "Piers Anthony", Series: "Xanth", SeriesIndex: "14"},
		19: {Title: "Roc and a Hard Place", Author: "Piers Anthony", Series: "Xanth", SeriesIndex: "19"},
		40: {Title: "Isis Orb", Author: "Piers Anthony", Series: "Xanth", SeriesIndex: "40"},
	}
	order := []int{}
	for _, item := range prop.Items {
		order = append(order, item.BookID)
		w, ok := want[item.BookID]
		if !ok {
			t.Errorf("unexpected item for book %d: %+v", item.BookID, item.Proposed)
			continue
		}
		got := item.Proposed
		got.Authors = nil
		if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", w) {
			t.Errorf("book %d proposed %+v, want %+v", item.BookID, item.Proposed, w)
		}
		if item.Verified != (item.BookID != 40) {
			t.Errorf("book %d verified = %v", item.BookID, item.Verified)
		}
	}
	if got := fmt.Sprint(order); got != "[3 5 14 19 40]" {
		t.Errorf("item order = %s, want [3 5 14 19 40]", got)
	}
	if len(prop.Missing) != 1 || prop.Missing[0].Title != "Isle of View" {
		t.Errorf("missing = %+v, want only Isle of View", prop.Missing)
	}
}

func TestBuildSeriesCleanupFromTitlesAlone(t *testing.T) {
	// No Wikidata series: the series comes from the anchor's own title.
	anchor := database.Book{ID: 1, Title: "Tarot 1 - God of Tarot", Author: "Piers Anthony"}
	books := []database.Book{
		anchor,
		{ID: 2, Title: "Tarot 2 - Vision of Tarot", Author: "Piers Anthony"},
		{ID: 3, Title: "Tarot 3 - Faith of Tarot", Author: "Someone Else"},
	}
	prop := buildSeriesCleanup(anchor, "", "", nil, nil, books, nil)
	if prop.Series != "Tarot" || len(prop.Items) != 2 {
		t.Fatalf("got series %q with %d items, want Tarot with 2", prop.Series, len(prop.Items))
	}
	if p := prop.Items[1].Proposed; p.Title != "Vision of Tarot" || p.SeriesIndex != "2" || prop.Items[1].Verified {
		t.Errorf("book 2 proposed %+v verified=%v", p, prop.Items[1].Verified)
	}
}

func TestTidySeriesName(t *testing.T) {
	for in, want := range map[string]string{
		"Tarot series":        "Tarot",
		"Remember Me Series":  "Remember Me",
		"Xanth":               "Xanth",
		"Series":              "Series",
		" The Expanse series": "The Expanse",
	} {
		if got := tidySeriesName(in); got != want {
			t.Errorf("tidySeriesName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildSeriesCleanupUsesFileAuthors(t *testing.T) {
	entries := []seriesEntry{{ID: "Q14", Title: "Question Quest", Index: "14"}}
	authors := map[string][]string{"Q14": {"Piers Anthony"}}
	anchor := database.Book{ID: 1, Title: "Xanth 01 - A Spell for Chameleon", Author: "Piers Anthony"}
	// The cache holds the last creator; the file lists both names.
	qq := database.Book{ID: 14, Title: "Xanth 14 - Question Quest", Author: "Piers A. Jacob"}
	file := &cleanupFields{Title: "Xanth 14 - Question Quest", Authors: []string{"Piers Anthony", "Piers A. Jacob"}}

	prop := buildSeriesCleanup(anchor, "", "Xanth", entries, authors, []database.Book{anchor, qq},
		func(b database.Book) *cleanupFields {
			if b.ID == 14 {
				return file
			}
			return nil
		})

	var item *cleanupItem
	for i := range prop.Items {
		if prop.Items[i].BookID == 14 {
			item = &prop.Items[i]
		}
	}
	if item == nil {
		t.Fatal("Question Quest not proposed")
	}
	if fmt.Sprint(item.Proposed.Authors) != "[Piers Anthony]" || item.Current.Author != "Piers Anthony & Piers A. Jacob" {
		t.Errorf("authors: current %q, proposed %v", item.Current.Author, item.Proposed.Authors)
	}
}

func TestBuildSeriesCleanupKeepsKnownCoAuthors(t *testing.T) {
	entries := []seriesEntry{{ID: "Q1", Title: "Dragon's Gold", Index: "1"}}
	authors := map[string][]string{"Q1": {"Piers Anthony", "Robert E. Margroff"}}
	anchor := database.Book{ID: 1, Title: "Dragon's Gold 1 - Dragon's Gold", Author: "Robert Margroff"}

	prop := buildSeriesCleanup(anchor, "Q1", "Dragon's Gold", entries, authors, []database.Book{anchor}, nil)
	if len(prop.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(prop.Items))
	}
	for _, c := range prop.Items[0].Changes {
		if c == "author" {
			t.Errorf("author change proposed for a listed co-author: %v", prop.Items[0].Proposed.Authors)
		}
	}
}

func TestBuildSeriesCleanupComparesAgainstFile(t *testing.T) {
	// A rescan already cleaned the cached title and series, but the EPUB
	// itself still carries the prefix and no series tag.
	entries := []seriesEntry{{ID: "Q3", Title: "Castle Roogna", Index: "3"}}
	anchor := database.Book{ID: 3, Title: "Castle Roogna", Author: "Piers Anthony", Series: "Xanth", SeriesIndex: "3"}
	file := &cleanupFields{Title: "Xanth 03 - Castle Roogna", Authors: []string{"Piers Anthony"}}

	prop := buildSeriesCleanup(anchor, "Q3", "Xanth", entries, nil, []database.Book{anchor},
		func(database.Book) *cleanupFields { return file })
	if len(prop.Items) != 1 || fmt.Sprint(prop.Items[0].Changes) != "[title series series_index]" {
		t.Fatalf("got %+v, want title, series and index changes", prop.Items)
	}
}
