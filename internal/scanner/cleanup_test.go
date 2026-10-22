// SPDX-License-Identifier: MIT

package scanner

import (
	"strings"
	"testing"
)

func TestRewriteOPFCleanup(t *testing.T) {
	const opf = `<package><metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
<dc:title id="t1">Xanth 14 - Question Quest</dc:title>
<dc:creator opf:role="aut" opf:file-as="Jacob, Piers A.">Piers A. Jacob</dc:creator>
<dc:creator opf:role="ill">Some Illustrator</dc:creator>
<dc:language>en</dc:language>
<dc:subject>Fantasy</dc:subject>
<meta name="calibre:title_sort" content="Xanth 14 - Question Quest"/>
</metadata><manifest><item id="a" href="a.xhtml"/></manifest></package>`

	orig := []byte(opf)
	out, err := rewriteOPFCleanup(orig, CleanupUpdate{
		Title: "Question Quest", Authors: []string{"Piers Anthony"}, Series: "Xanth", SeriesIndex: "14",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(orig) != opf {
		t.Errorf("input buffer was modified")
	}
	got := string(out)
	for _, want := range []string{
		`<dc:title id="t1">Question Quest</dc:title>`,
		`<dc:creator opf:role="aut" opf:file-as="Anthony, Piers">Piers Anthony</dc:creator>`,
		`<dc:creator opf:role="ill">Some Illustrator</dc:creator>`,
		`<dc:language>en</dc:language>`,
		`<dc:subject>Fantasy</dc:subject>`,
		`<meta name="calibre:series" content="Xanth"/>`,
		`<meta name="calibre:series_index" content="14"/>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "title_sort") || strings.Contains(got, "Jacob") {
		t.Errorf("stale values survived:\n%s", got)
	}
	if !strings.HasSuffix(got, `</metadata><manifest><item id="a" href="a.xhtml"/></manifest></package>`) {
		t.Errorf("content after metadata damaged:\n%s", got)
	}
}

func TestRewriteOPFCleanupLeavesUnnamedFields(t *testing.T) {
	const opf = `<package><metadata>
<dc:title>Ogre Ogre</dc:title>
<dc:creator id="c1">Piers Anthony</dc:creator>
<meta refines="#c1" property="file-as">Anthony, Piers</meta>
<meta name="calibre:title_sort" content="Ogre Ogre"/>
</metadata></package>`

	out, err := rewriteOPFCleanup([]byte(opf), CleanupUpdate{Title: "Ogre, Ogre"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `<dc:title>Ogre, Ogre</dc:title>`) ||
		!strings.Contains(got, `<dc:creator id="c1">Piers Anthony</dc:creator>`) ||
		!strings.Contains(got, `<meta refines="#c1" property="file-as">Anthony, Piers</meta>`) {
		t.Errorf("unexpected output:\n%s", got)
	}
	if strings.Contains(got, "calibre:series") {
		t.Errorf("series written though none was given:\n%s", got)
	}
}

func TestRewriteOPFCleanupUpdatesEPUB3SortName(t *testing.T) {
	const opf = `<package><metadata>
<dc:title>Spell</dc:title>
<dc:creator id="c1">Piers A. Jacob</dc:creator>
<meta refines="#c1" property="file-as">Jacob, Piers A.</meta>
</metadata></package>`

	out, err := rewriteOPFCleanup([]byte(opf), CleanupUpdate{Authors: []string{"Piers Anthony"}})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `<dc:creator id="c1">Piers Anthony</dc:creator>`) ||
		!strings.Contains(got, `<meta refines="#c1" property="file-as">Anthony, Piers</meta>`) {
		t.Errorf("unexpected output:\n%s", got)
	}
}

// The layout of a real Question Quest EPUB: the right author first, then the
// same person under his legal name as a second author.
func TestRewriteOPFCleanupDropsExtraAuthors(t *testing.T) {
	const opf = `<package><metadata xmlns:opf="http://www.idpf.org/2007/opf">
<dc:creator opf:role="aut" opf:file-as="Anthony, Piers &amp; Jacob, Piers A.">Piers Anthony</dc:creator>
<dc:creator opf:role="aut">Piers A. Jacob</dc:creator>
<dc:creator opf:role="ill">An Artist</dc:creator>
<dc:title>Xanth 14 - Question Quest</dc:title>
</metadata></package>`

	out, err := rewriteOPFCleanup([]byte(opf), CleanupUpdate{Authors: []string{"Piers Anthony"}})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `<dc:creator opf:role="aut" opf:file-as="Anthony, Piers">Piers Anthony</dc:creator>`) ||
		!strings.Contains(got, `<dc:creator opf:role="ill">An Artist</dc:creator>`) ||
		strings.Contains(got, "Jacob") {
		t.Errorf("unexpected output:\n%s", got)
	}
}

func TestRewriteOPFCleanupWritesCoAuthors(t *testing.T) {
	const opf = `<package><metadata>
<dc:creator id="c1">Robert Margroff</dc:creator>
<dc:creator id="c2">Somebody Wrong</dc:creator>
<meta refines="#c2" property="file-as">Wrong, Somebody</meta>
</metadata></package>`

	out, err := rewriteOPFCleanup([]byte(opf), CleanupUpdate{Authors: []string{"Piers Anthony", "Robert E. Margroff"}})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	want := "<dc:creator id=\"c1\">Piers Anthony</dc:creator>\n<dc:creator>Robert E. Margroff</dc:creator>"
	if !strings.Contains(got, want) || strings.Contains(got, "Wrong") {
		t.Errorf("unexpected output:\n%s", got)
	}
}

func TestPrimaryAuthorSkipsOtherRoles(t *testing.T) {
	cases := map[string]string{
		`<dc:creator opf:role="ill">Artist</dc:creator><dc:creator opf:role="aut">Writer</dc:creator>`: "Writer",
		`<dc:creator>First</dc:creator><dc:creator>Second</dc:creator>`:                                "First",
		`<dc:creator id="a">Artist</dc:creator><meta refines="#a" property="role">ill</meta>
<dc:creator id="b">Writer</dc:creator>`: "Writer",
		`<dc:creator opf:role="edt">Editor Only</dc:creator>`: "Editor Only",
	}
	for in, want := range cases {
		if got := primaryAuthor([]byte(in)); got != want {
			t.Errorf("primaryAuthor(%q) = %q, want %q", in, got, want)
		}
	}
}
