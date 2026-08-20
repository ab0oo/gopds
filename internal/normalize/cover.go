// SPDX-License-Identifier: MIT

package normalize

import (
	"path/filepath"
	"strings"
)

// CoverVerdict classifies a candidate image's suitability as book artwork.
type CoverVerdict int

const (
	CoverUnusable   CoverVerdict = iota // wrong shape, too small, or not artwork
	CoverAcceptable                     // book-shaped and legible, but not sharp
	CoverGood                           // bookstore quality
)

func (v CoverVerdict) String() string {
	switch v {
	case CoverGood:
		return "good"
	case CoverAcceptable:
		return "acceptable"
	default:
		return "unusable"
	}
}

// Book covers are portrait and roughly 2:3. These bounds are deliberately
// generous at the edges but still reject squares, banners, and logos.
const (
	coverAspectMin = 0.55
	coverAspectMax = 0.80
	coverGoodWidth = 600
	coverMinWidth  = 300
	coverMinHeight = 420
	coverIconMax   = 150 // below this in either axis it is an icon, not art
)

// logoHints are filename fragments that betray publisher furniture rather than
// cover artwork. Seen in the wild as e.g. "HarperCollins_200_Logo_ebk.jpg",
// which is large and would otherwise beat the real cover on pixel count.
var logoHints = []string{
	"logo", "publisher", "imprint", "brand", "colophon",
	"titlepage", "title_page", "backcover", "back_cover", "spine",
	"advert", "promo", "banner", "device", "orbit", "signature",
}

// AssessCover judges whether an image is usable as a book cover.
//
// Aspect ratio is checked before size on purpose: a large landscape image is a
// publisher logo or a two-page spread, never a cover, and picking it because it
// has the most pixels is exactly the mistake this is here to prevent.
func AssessCover(name string, width, height int) CoverVerdict {
	if width <= 0 || height <= 0 {
		return CoverUnusable
	}
	if looksLikeFurniture(name) {
		return CoverUnusable
	}
	if width < coverIconMax || height < coverIconMax {
		return CoverUnusable
	}

	ratio := float64(width) / float64(height)
	if ratio < coverAspectMin || ratio > coverAspectMax {
		return CoverUnusable
	}

	if width >= coverGoodWidth && height >= coverGoodWidth*4/3 {
		return CoverGood
	}
	if width >= coverMinWidth && height >= coverMinHeight {
		return CoverAcceptable
	}
	return CoverUnusable
}

// looksLikeFurniture reports whether a filename suggests non-cover artwork.
func looksLikeFurniture(name string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(name)))
	if base == "" {
		return false
	}
	// A file explicitly named cover is trusted even if it also says "back".
	if base == "cover.jpg" || base == "cover.jpeg" || base == "cover.png" {
		return false
	}
	for _, hint := range logoHints {
		if strings.Contains(base, hint) {
			return true
		}
	}
	return false
}

// CoverScore ranks a usable cover so the best candidate can be chosen. Higher
// is better. Images closer to a 2:3 portrait ratio are preferred at equal size,
// which favours real artwork over cropped or padded variants.
func CoverScore(name string, width, height int) int {
	if AssessCover(name, width, height) == CoverUnusable {
		return 0
	}
	ratio := float64(width) / float64(height)
	ideal := 0.667
	deviation := ratio - ideal
	if deviation < 0 {
		deviation = -deviation
	}
	// Pixel count drives the score; deviation from ideal shape discounts it.
	score := width * height
	penalty := 1.0 - (deviation * 1.5)
	if penalty < 0.3 {
		penalty = 0.3
	}
	score = int(float64(score) * penalty)

	// A canonical filename is a strong signal of intent.
	if isCanonicalCoverName(name) {
		score += score / 4
	}
	return score
}

func isCanonicalCoverName(name string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(name)))
	return strings.HasPrefix(base, "cover")
}
