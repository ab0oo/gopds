// SPDX-License-Identifier: MIT

package normalize

import "testing"

func TestAssessCoverRejectsNonArtwork(t *testing.T) {
	cases := []struct {
		name string
		w, h int
		why  string
	}{
		{"HarperCollins_200_Logo_ebk.jpg", 1500, 900, "publisher logo, landscape and named logo"},
		{"images/00004.jpeg", 1500, 900, "landscape aspect"},
		{"thumb.jpg", 60, 40, "icon-sized"},
		{"square.jpg", 500, 500, "square is not a book cover"},
		{"banner.jpg", 1200, 300, "banner"},
		{"titlepage.jpg", 600, 900, "title page furniture"},
		{"nothing.jpg", 0, 0, "no dimensions"},
	}
	for _, c := range cases {
		if got := AssessCover(c.name, c.w, c.h); got != CoverUnusable {
			t.Errorf("AssessCover(%q, %d, %d) = %v, want unusable (%s)", c.name, c.w, c.h, got, c.why)
		}
	}
}

func TestAssessCoverAcceptsRealCovers(t *testing.T) {
	cases := []struct {
		name string
		w, h int
		want CoverVerdict
	}{
		{"images/00062.jpeg", 1242, 1900, CoverGood},
		{"cover.jpg", 800, 1200, CoverGood},
		{"cover.jpg", 386, 500, CoverAcceptable},
		{"cover.jpeg", 313, 500, CoverAcceptable},
		{"art.jpg", 290, 400, CoverUnusable}, // below the floor
	}
	for _, c := range cases {
		if got := AssessCover(c.name, c.w, c.h); got != c.want {
			t.Errorf("AssessCover(%q, %d, %d) = %v, want %v", c.name, c.w, c.h, got, c.want)
		}
	}
}

func TestCoverScorePrefersRealArtOverLogo(t *testing.T) {
	// The logo has more pixels but must never outrank the genuine cover.
	logo := CoverScore("HarperCollins_200_Logo_ebk.jpg", 1500, 900)
	art := CoverScore("images/00062.jpeg", 800, 1200)
	if logo >= art {
		t.Errorf("logo scored %d, real cover scored %d: logo must not win", logo, art)
	}
	if logo != 0 {
		t.Errorf("unusable image should score 0, got %d", logo)
	}
}

func TestCoverScorePrefersLargerAtSameShape(t *testing.T) {
	small := CoverScore("cover.jpg", 400, 600)
	large := CoverScore("cover.jpg", 800, 1200)
	if large <= small {
		t.Errorf("larger cover should score higher: %d vs %d", large, small)
	}
}

func TestCoverScorePrefersCanonicalName(t *testing.T) {
	named := CoverScore("cover.jpg", 600, 900)
	other := CoverScore("images/00012.jpeg", 600, 900)
	if named <= other {
		t.Errorf("canonical cover.jpg should outrank generic name: %d vs %d", named, other)
	}
}

func TestLooksLikeFurniture(t *testing.T) {
	if looksLikeFurniture("cover.jpg") {
		t.Error("cover.jpg must never be treated as furniture")
	}
	if !looksLikeFurniture("back_cover.jpg") {
		t.Error("back_cover.jpg should be furniture")
	}
}
