// SPDX-License-Identifier: MIT

package scanner

import (
	"strings"
	"testing"
)

func TestRewriteOPFSeries(t *testing.T) {
	const opf = `<package><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:title>Stone of Tears</dc:title>
<dc:creator>Terry Goodkind</dc:creator>
<meta name="calibre:series" content="Old Name"/>
<meta name="calibre:series_index" content="9"/>
</metadata><manifest><item id="a" href="a.xhtml"/></manifest></package>`

	out, err := rewriteOPFSeries([]byte(opf), "The Sword of Truth", "2")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		`<meta name="calibre:series" content="The Sword of Truth"/>`,
		`<meta name="calibre:series_index" content="2"/>`,
		`<dc:title>Stone of Tears</dc:title>`,
		`<dc:creator>Terry Goodkind</dc:creator>`,
		`</metadata><manifest><item id="a" href="a.xhtml"/></manifest></package>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Old Name") || strings.Contains(got, `content="9"`) {
		t.Errorf("old series values survived:\n%s", got)
	}
}

// A book with no series tags yet exercises the append path, which must not
// write over the OPF content that follows the metadata block.
func TestRewriteOPFSeriesAddsToUntaggedBook(t *testing.T) {
	const opf = `<package><metadata><dc:title>Dune</dc:title></metadata><manifest><item id="a" href="a.xhtml"/></manifest></package>`
	orig := []byte(opf)
	out, err := rewriteOPFSeries(orig, "Dune", "1")
	if err != nil {
		t.Fatal(err)
	}
	if string(orig) != opf {
		t.Errorf("input buffer was modified:\n%s", orig)
	}
	got := string(out)
	if !strings.Contains(got, `<meta name="calibre:series" content="Dune"/>`) ||
		!strings.HasSuffix(got, `</metadata><manifest><item id="a" href="a.xhtml"/></manifest></package>`) {
		t.Errorf("unexpected output:\n%s", got)
	}
}
