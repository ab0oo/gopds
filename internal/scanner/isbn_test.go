// SPDX-License-Identifier: MIT

package scanner

import "testing"

func TestISBNFromString(t *testing.T) {
	cases := []struct{ in, want string }{
		{"9781760857899", "9781760857899"},
		{"isbn:9781982192655", "9781982192655"},
		{"978-0-7432-2188-7", "9780743221887"},
		{"0439023481", "0439023481"},
		{"043902348X", "043902348X"},
		{"urn:isbn:9780743221887", "9780743221887"},
		// Rejections
		{"992b2d82-ebb6-40ac-add4-46c368b56848", ""}, // UUID
		{"0fae00cf-d0ea-4545-9f3a-a9c23d9ee10b", ""}, // UUID
		{"google:gW1xzwEACAAJ", ""},                  // google volume id
		{"", ""},
		{"12345", ""}, // too short
	}
	for _, c := range cases {
		if got := ISBNFromString(c.in); got != c.want {
			t.Errorf("ISBNFromString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
