// SPDX-License-Identifier: MIT

package normalize

import "testing"

func TestCategoryLabel(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Fantasy fiction", "Fantasy Fiction"},
		{"Fantasy Fiction", "Fantasy Fiction"},
		{"FANTASY FICTION", "Fantasy Fiction"},
		{"  fantasy   fiction ", "Fantasy Fiction"},
		{"sword and sorcery", "Sword and Sorcery"},
		{"the lord of the rings", "The Lord of the Rings"},
		{"sci-fi", "Sci-Fi"},
		{"coming-of-age", "Coming-of-Age"},
		{"children's fiction", "Children's Fiction"},
		{"(epic) fantasy", "(Epic) Fantasy"},
		{"LitRPG", "LitRPG"},
		{"YA fantasy", "YA Fantasy"},
		{"sf", "SF"},
		{"ya fantasy", "YA Fantasy"},
		{"wwii", "WWII"},
		{"élan vital", "Élan Vital"},
		{"", ""},
	} {
		if got := CategoryLabel(tc.in); got != tc.want {
			t.Errorf("CategoryLabel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The canonical genres are what most books are filed under, so cleaning must
// never rename them.
func TestCategoryLabelKeepsCanonicalGenres(t *testing.T) {
	for _, rule := range genreRules {
		if got := CategoryLabel(rule.genre); got != rule.genre {
			t.Errorf("CategoryLabel(%q) = %q", rule.genre, got)
		}
	}
}
