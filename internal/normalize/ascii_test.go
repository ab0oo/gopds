// SPDX-License-Identifier: MIT

package normalize

import "testing"

func TestFoldASCII(t *testing.T) {
	cases := map[string]string{
		"Der Außenseiter":    "Der Aussenseiter",
		"Demons Don’t Dream": "Demons Don't Dream",
		"Café – Crème":       "Cafe - Creme",
		"三体":                 "__",
		"Plain":              "Plain",
	}
	for in, want := range cases {
		if got := FoldASCII(in, "_"); got != want {
			t.Errorf("FoldASCII(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestASCIIFilename(t *testing.T) {
	cases := map[string]string{
		"Piers Anthony - Castle Roogna.epub":      "Piers_Anthony_-_Castle_Roogna.epub",
		"Piers Anthony - Demons Don’t Dream.epub": "Piers_Anthony_-_Demons_Dont_Dream.epub",
		"Gregg Hurwitz - Der Außenseiter.epub":    "Gregg_Hurwitz_-_Der_Aussenseiter.epub",
		"三体.epub":                                 "book.epub",
		"":                                        "book",
	}
	for in, want := range cases {
		if got := ASCIIFilename(in); got != want {
			t.Errorf("ASCIIFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
