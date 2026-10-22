// SPDX-License-Identifier: MIT

package kindle

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type entry struct {
	name, body string
	method     uint16
}

func writeZip(t *testing.T, path string, entries []entry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for _, e := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: e.method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func readZip(t *testing.T, data []byte) ([]*zip.File, map[string]string) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return zr.File, out
}

const container = `<?xml version="1.0"?><container><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`

func TestPrepareEPUBAppliesKindleFixes(t *testing.T) {
	src := filepath.Join(t.TempDir(), "book.epub")
	writeZip(t, src, []entry{
		// Deliberately not first and compressed: the output must fix both.
		{name: "META-INF/container.xml", body: container, method: zip.Deflate},
		{name: "mimetype", body: "application/epub+zip", method: zip.Deflate},
		{name: "OEBPS/content.opf", body: `<package><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Castle Roogna</dc:title></metadata></package>`, method: zip.Deflate},
		{name: "OEBPS/ch1.xhtml", body: `<html><body id="start"><p>Café</p><img/><img src="a.jpg"/></body></html>`, method: zip.Deflate},
		{name: "OEBPS/ch2.xhtml", body: `<?xml version="1.0"?>` + "\n" + `<html><body><a href="ch1.xhtml#start">back</a></body></html>`, method: zip.Deflate},
		{name: "OEBPS/ok.xhtml", body: `<?xml version="1.0" encoding="UTF-8"?><html><body/></html>`, method: zip.Deflate},
		{name: "OEBPS/cover.jpg", body: "\xff\xd8binary", method: zip.Store},
	})

	got, err := PrepareEPUB(src)
	if err != nil {
		t.Fatal(err)
	}
	files, content := readZip(t, got.Data)

	if files[0].Name != "mimetype" || files[0].Method != zip.Store || content["mimetype"] != "application/epub+zip" {
		t.Errorf("first entry = %s (method %d), want stored mimetype", files[0].Name, files[0].Method)
	}
	ch1 := content["OEBPS/ch1.xhtml"]
	if !strings.HasPrefix(ch1, `<?xml version="1.0" encoding="utf-8"?>`) {
		t.Errorf("ch1 lacks UTF-8 declaration: %q", ch1)
	}
	if strings.Contains(ch1, "<img/>") || !strings.Contains(ch1, `<img src="a.jpg"/>`) {
		t.Errorf("stray img not removed, or real img lost: %q", ch1)
	}
	ch2 := content["OEBPS/ch2.xhtml"]
	if strings.Count(ch2, "<?xml") != 1 || !strings.HasPrefix(ch2, `<?xml version="1.0" encoding="utf-8"?>`) {
		t.Errorf("ch2 declaration not replaced cleanly: %q", ch2)
	}
	if !strings.Contains(ch2, `href="ch1.xhtml"`) {
		t.Errorf("body id link not rewritten: %q", ch2)
	}
	if content["OEBPS/ok.xhtml"] != `<?xml version="1.0" encoding="UTF-8"?><html><body/></html>` {
		t.Errorf("already-valid page changed: %q", content["OEBPS/ok.xhtml"])
	}
	if !strings.Contains(content["OEBPS/content.opf"], "<dc:language>en</dc:language>") {
		t.Errorf("language not added: %q", content["OEBPS/content.opf"])
	}
	if content["OEBPS/cover.jpg"] != "\xff\xd8binary" {
		t.Error("binary entry damaged")
	}
	if len(got.Fixes) < 5 {
		t.Errorf("fixes = %v, want at least 5", got.Fixes)
	}
}

func TestPrepareEPUBWarnsOnUnsupportedLanguage(t *testing.T) {
	src := filepath.Join(t.TempDir(), "book.epub")
	opf := `<package><metadata><dc:language>ru</dc:language></metadata></package>`
	writeZip(t, src, []entry{
		{name: "mimetype", body: "application/epub+zip", method: zip.Store},
		{name: "META-INF/container.xml", body: container},
		{name: "OEBPS/content.opf", body: opf},
	})
	got, err := PrepareEPUB(src)
	if err != nil {
		t.Fatal(err)
	}
	_, content := readZip(t, got.Data)
	if content["OEBPS/content.opf"] != opf {
		t.Error("an unsupported language must be reported, not replaced")
	}
	if len(got.Warnings) != 1 {
		t.Errorf("warnings = %v, want one", got.Warnings)
	}
}

// Calibre-era EPUBs (like the fixture, made the same way by Python's zipfile)
// deflate their directory entries, which a raw zip copy cannot write back.
func TestPrepareEPUBHandlesDeflatedDirectoryEntries(t *testing.T) {
	got, err := PrepareEPUB(filepath.Join("testdata", "deflated-dir-entry.epub"))
	if err != nil {
		t.Fatal(err)
	}
	files, content := readZip(t, got.Data)
	names := []string{}
	for _, f := range files {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "mimetype,META-INF/,META-INF/container.xml,content.opf,page.htm" {
		t.Errorf("entries = %v", names)
	}
	if !strings.HasPrefix(content["page.htm"], `<?xml version="1.0" encoding="utf-8"?>`) {
		t.Errorf(".htm page not fixed: %q", content["page.htm"])
	}
}

func TestFixLanguage(t *testing.T) {
	wrap := func(l string) string { return "<metadata><dc:language>" + l + "</dc:language></metadata>" }
	cases := []struct {
		in, want string
		warn     bool
	}{
		{wrap("en"), wrap("en"), false},
		{wrap("EN-gb"), wrap("EN-gb"), false},
		{wrap("und"), wrap("en"), false},
		{wrap("UND"), wrap("en"), false},
		{wrap(""), wrap("en"), false},
		{wrap("US English (en-us)"), wrap("en-us"), false},
		{wrap("ru"), wrap("ru"), true},
		{wrap("AN"), wrap("AN"), true},
		{"<metadata><dc:title>x</dc:title></metadata>", "<metadata><dc:title>x</dc:title><dc:language>en</dc:language>\n</metadata>", false},
	}
	for _, c := range cases {
		got, _, warn := fixLanguage([]byte(c.in))
		if string(got) != c.want || (warn != "") != c.warn {
			t.Errorf("fixLanguage(%q) = %q (warning %q)", c.in, got, warn)
		}
	}
}
