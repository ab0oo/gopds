// SPDX-License-Identifier: MIT

package web

import "testing"

func TestWikiPageMatchesBook(t *testing.T) {
	cases := []struct {
		page, title, author string
		want                bool
	}{
		{"Soul of the Fire", "Soul of the Fire", "Terry Goodkind", true},
		{"Temple of the Winds", "Temple of the Winds", "Terry Goodkind", true},
		{"Temple of the Winds (building)", "Temple of the Winds", "Terry Goodkind", false},
		{"Temple of the Winged Lions", "Temple of the Winds", "Terry Goodkind", false},
		{"Terry Goodkind", "Soul of the Fire", "Terry Goodkind", false},
		{"The Sword of Truth", "Soul of the Fire", "Terry Goodkind", false},
		{"Faith of the Fallen", "Soul of the Fire", "Terry Goodkind", false},
		{"Dune (novel)", "Dune", "Frank Herbert", true},
		{"Dune (franchise)", "Dune", "Frank Herbert", false},
		{"It (novel)", "It", "Stephen King", true},
		{"Emma (Austen novel)", "Emma", "Jane Austen", true},
		{"The Stand (King)", "The Stand", "Stephen King", true},
		{"Wizard's First Rule", "Wizards First Rule", "Terry Goodkind", true},
		{"Soul of the Fire", "Soul of the Fire (Sword of Truth #5)", "Terry Goodkind", true},
		{"Soul of the Fire", "Soul of the Fire: A Sword of Truth Novel", "Terry Goodkind", true},
		{"", "Soul of the Fire", "Terry Goodkind", false},
	}
	for _, c := range cases {
		if got := wikiPageMatchesBook(c.page, c.title, c.author); got != c.want {
			t.Errorf("wikiPageMatchesBook(%q, %q, %q) = %v, want %v", c.page, c.title, c.author, got, c.want)
		}
	}
}
