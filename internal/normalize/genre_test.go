// SPDX-License-Identifier: MIT

package normalize

import "testing"

func TestCanonicalGenreMergesVariants(t *testing.T) {
	// All of these appear in a real library and mean the same thing.
	for _, in := range []string{"Science Fiction", "Science fiction", "science fiction",
		"sf", "sf_history", "Sci-Fi Short", "Speculative", "Speculative Fiction",
		"Science Fiction - Adventure", "Science Fiction; American"} {
		if got := CanonicalGenre(in); got != GenreScienceFiction {
			t.Errorf("CanonicalGenre(%q) = %q, want %q", in, got, GenreScienceFiction)
		}
	}
	for _, in := range []string{"Fantasy", "fantasy", "Fantasy:Humour", "Urban Fantasy",
		"Young Adult Fansaty", "fantasy;cat;sword and sorcery;magic;high fantasy"} {
		if got := CanonicalGenre(in); got != GenreFantasy {
			t.Errorf("CanonicalGenre(%q) = %q, want %q", in, got, GenreFantasy)
		}
	}
}

func TestCanonicalGenreDropsJunk(t *testing.T) {
	for _, in := range []string{"", "Retail", "new", "ebook", "None", "Unknown",
		"General", "Age Range 2 Older Audience", "#genre", "F", "SS", "A&A",
		"english eBooks", "noC", "k'12"} {
		if got := CanonicalGenre(in); got != "" {
			t.Errorf("CanonicalGenre(%q) = %q, want \"\" (junk)", in, got)
		}
	}
}

func TestCanonicalGenreSpecificBeatsGeneral(t *testing.T) {
	// "science fiction" contains "fiction" but must not map to General Fiction.
	if got := CanonicalGenre("science fiction"); got != GenreScienceFiction {
		t.Errorf("got %q, want Science Fiction", got)
	}
	// "historical romance" is romance, checked before historical.
	if got := CanonicalGenre("historical romance"); got != GenreRomance {
		t.Errorf("got %q, want Romance", got)
	}
	// Bare fiction is the fallback.
	if got := CanonicalGenre("Fiction"); got != GenreFiction {
		t.Errorf("got %q, want General Fiction", got)
	}
}

func TestCanonicalGenreMinesKeywordDumps(t *testing.T) {
	in := "Lee Child;Jack Reacher;Kim Otto;vigilante justice;conspiracy thriller"
	if got := CanonicalGenre(in); got != GenreThriller {
		t.Errorf("CanonicalGenre(keyword dump) = %q, want Thriller", got)
	}
}

func TestCanonicalGenreBISAC(t *testing.T) {
	if got := CanonicalGenre("FIC009000"); got != GenreFiction {
		t.Errorf("FIC BISAC should map to General Fiction, got %q", got)
	}
	if got := CanonicalGenre("XYZ123456"); got != "" {
		t.Errorf("unknown BISAC should be dropped, got %q", got)
	}
}

func TestGenreFromSubjectsPrefersSpecific(t *testing.T) {
	// A book tagged both "Fiction" and "Science Fiction" is science fiction.
	got := GenreFromSubjects([]string{"Fiction", "Science Fiction"})
	if got != GenreScienceFiction {
		t.Errorf("GenreFromSubjects = %q, want Science Fiction", got)
	}
	// Junk subjects must not prevent a real match.
	got = GenreFromSubjects([]string{"Retail", "ebook", "Horror"})
	if got != GenreHorror {
		t.Errorf("GenreFromSubjects = %q, want Horror", got)
	}
	// Nothing usable means uncategorized, not a guess.
	if got := GenreFromSubjects([]string{"Retail", "new", ""}); got != "" {
		t.Errorf("GenreFromSubjects = %q, want empty", got)
	}
}

func TestCanonicalGenreDoesNotInventFromAuthorNames(t *testing.T) {
	// Author names used as subjects carry no genre signal.
	for _, in := range []string{"Philip K Dick", "neil gaiman", "Terry Pratchet", "Dresden Files"} {
		if got := CanonicalGenre(in); got != "" {
			t.Errorf("CanonicalGenre(%q) = %q, want \"\"", in, got)
		}
	}
}
