// SPDX-License-Identifier: MIT

package normalize

import "testing"

func TestAuthorInversion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Christie, Agatha", "Agatha Christie"},
		{"Bujold, Lois McMaster", "Lois McMaster Bujold"},
		{"Corey, James S. A.", "James S. A. Corey"},
		{"Clarke, Arthur C", "Arthur C Clarke"},
		{"Clark, Mary Higgins", "Mary Higgins Clark"},
		{"Agatha Christie", "Agatha Christie"}, // already canonical, idempotent
		{"  Spaced ,  Out  ", "Out Spaced"},
	}
	for _, c := range cases {
		if got := Author(c.in).Value; got != c.want {
			t.Errorf("Author(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAuthorRefusesUnsafeInversion(t *testing.T) {
	// These all contain a comma but must NOT be inverted.
	cases := []string{
		"Bujold, Lois McMaster - Vorkosigan 04", // series junk in creator field
		"Child, Lee (by Jude Hardin)",           // annotation
		"Agatha Christie, writing as Mary Westmacott",
		"King, Jr.",                         // name suffix
		"Gaiman, Neil and Pratchett, Terry", // multi-author
		"Smith, John & Doe, Jane",
	}
	for _, in := range cases {
		if got := Author(in).Value; got != in {
			t.Errorf("Author(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestAuthorCapsRepair(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ARTHUR C. CLARKE", "Arthur C. Clarke"},
		{"GERARD, CINDY", "Cindy Gerard"}, // inversion + caps repair compose
		{"LYN McCONCHIE", "Lyn McConchie"},
		{"Cindy Gerard", "Cindy Gerard"}, // untouched
	}
	for _, c := range cases {
		if got := Author(c.in).Value; got != c.want {
			t.Errorf("Author(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAuthorFlags(t *testing.T) {
	cases := []struct{ in, wantFlag string }{
		{"", ReviewAuthorMissing},
		{"3", ReviewAuthorSuspect},
		{"BROWN SANDRA", ReviewAuthorAmbiguous}, // reversed, no comma to prove it
		{"Bujold, Lois McMaster - Vorkosigan 04", ReviewAuthorAmbiguous},
		{"Agatha Christie", ""},
		{"Christie, Agatha", ""},
	}
	for _, c := range cases {
		if got := Author(c.in).Flag; got != c.wantFlag {
			t.Errorf("Author(%q) flag = %q, want %q", c.in, got, c.wantFlag)
		}
	}
}

func TestAuthorIdempotent(t *testing.T) {
	for _, in := range []string{"Christie, Agatha", "ARTHUR C. CLARKE", "GERARD, CINDY", "Lois McMaster Bujold"} {
		once := Author(in).Value
		twice := Author(once).Value
		if once != twice {
			t.Errorf("Author not idempotent for %q: %q then %q", in, once, twice)
		}
	}
}

func TestTitleSeriesExtraction(t *testing.T) {
	cases := []struct{ in, wantTitle, wantSeries, wantIdx string }{
		{"Imperial Lady (Central Asia Series Book 1)", "Imperial Lady", "Central Asia Series", "1"},
		{"Dead Man's Isle (Harlequin Crew #2)", "Dead Man's Isle", "Harlequin Crew", "2"},
		{"The Stars Below (Vega Jane, Book 4)", "The Stars Below", "Vega Jane", "4"},
		{"The Hit (Will Robie Book 2)", "The Hit", "Will Robie", "2"},
		{"Plain Title", "Plain Title", "", ""},
	}
	for _, c := range cases {
		got := Title(c.in, "", "")
		if got.Title != c.wantTitle || got.Series != c.wantSeries || got.SeriesIndex != c.wantIdx {
			t.Errorf("Title(%q) = (%q, %q, %q), want (%q, %q, %q)",
				c.in, got.Title, got.Series, got.SeriesIndex, c.wantTitle, c.wantSeries, c.wantIdx)
		}
	}
}

func TestTitleNeverOverwritesExistingSeries(t *testing.T) {
	got := Title("Imperial Lady (Central Asia Series Book 1)", "Real Series", "9")
	if got.Series != "Real Series" || got.SeriesIndex != "9" {
		t.Errorf("existing series clobbered: got (%q, %q)", got.Series, got.SeriesIndex)
	}
	if got.Title != "Imperial Lady" {
		t.Errorf("title should still be cleaned, got %q", got.Title)
	}
}

func TestTitleIdempotent(t *testing.T) {
	for _, in := range []string{"Imperial Lady (Central Asia Series Book 1)", "The Hit (Will Robie Book 2)"} {
		first := Title(in, "", "")
		second := Title(first.Title, first.Series, first.SeriesIndex)
		if second.Title != first.Title || second.Series != first.Series {
			t.Errorf("Title not idempotent for %q", in)
		}
	}
}

func TestTitlePreservesSubtitleParens(t *testing.T) {
	// Trailing parens that are not series annotations must survive.
	in := "Finders Killers: (A Wallace Mack Thriller)"
	if got := Title(in, "", "").Title; got != in {
		t.Errorf("Title(%q) = %q, want unchanged", in, got)
	}
}
