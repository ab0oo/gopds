// SPDX-License-Identifier: MIT

package normalize

import (
	"os"
	"strings"
)

// Quality scoring weights. These sum to 100 and define what "good metadata"
// means for this library: a book scoring 100 needs no further attention.
const (
	WeightDescription = 25
	WeightCover       = 25
	WeightAuthor      = 15
	WeightCategory    = 15
	WeightIdentifier  = 10
	WeightSeries      = 10
)

// MinDescriptionChars is the length below which a description is treated as a
// stub rather than real blurb text.
const MinDescriptionChars = 200

// Cover dimension floor for "bookstore quality" artwork.
const (
	MinCoverWidth  = 300
	MinCoverHeight = 420
	GoodCoverWidth = 600
)

// QualityInput is everything the scorer needs about one book.
type QualityInput struct {
	Title       string
	Author      string
	Description string
	Category    string
	Identifier  string
	Series      string
	SeriesIndex string
	CoverWidth  int
	CoverHeight int
	AuthorFlag  string
	TitleFlag   string
}

// QualityResult is the score plus the reasons it is not 100.
type QualityResult struct {
	Score int
	Flags []string
}

// Additional review flags raised by scoring rather than by parsing.
const (
	ReviewNoDescription = "no_description"
	ReviewThinCover     = "thin_cover"
	ReviewNoCover       = "no_cover"
	ReviewNoCategory    = "no_category"
	ReviewNoIdentifier  = "no_identifier"
)

// Score rates a book 0-100 on metadata completeness. It is a pure function so
// the same inputs always produce the same score, which keeps the aggregate
// number meaningful over time.
func Score(in QualityInput) QualityResult {
	res := QualityResult{}

	if len(strings.TrimSpace(in.Description)) >= MinDescriptionChars {
		res.Score += WeightDescription
	} else {
		res.Flags = append(res.Flags, ReviewNoDescription)
	}

	switch {
	case in.CoverWidth <= 0 || in.CoverHeight <= 0:
		res.Flags = append(res.Flags, ReviewNoCover)
	case in.CoverWidth < MinCoverWidth || in.CoverHeight < MinCoverHeight:
		res.Flags = append(res.Flags, ReviewThinCover)
	case in.CoverWidth >= GoodCoverWidth:
		res.Score += WeightCover
	default:
		res.Score += WeightCover / 2
	}

	if strings.TrimSpace(in.Author) != "" && in.AuthorFlag == "" {
		res.Score += WeightAuthor
	} else if in.AuthorFlag != "" {
		res.Flags = append(res.Flags, in.AuthorFlag)
	}

	if strings.TrimSpace(in.Category) != "" {
		res.Score += WeightCategory
	} else {
		res.Flags = append(res.Flags, ReviewNoCategory)
	}

	if strings.TrimSpace(in.Identifier) != "" {
		res.Score += WeightIdentifier
	} else {
		res.Flags = append(res.Flags, ReviewNoIdentifier)
	}

	// Series credit is only withheld when the book claims to be part of a
	// series but is missing its position (or vice versa). Standalone books are
	// not penalized for having no series.
	hasSeries := strings.TrimSpace(in.Series) != ""
	hasIndex := strings.TrimSpace(in.SeriesIndex) != ""
	if hasSeries == hasIndex {
		res.Score += WeightSeries
	}

	if in.TitleFlag != "" {
		res.Flags = append(res.Flags, in.TitleFlag)
	}

	return res
}

// FlagsString joins flags for storage in the enrichment table.
func FlagsString(flags []string) string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return strings.Join(out, ",")
}

// CoverDimensions reads the pixel size of a cached cover, returning zeros when
// the file is absent or undecodable.
func CoverDimensions(path string) (int, int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	cfg, _, err := decodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}
