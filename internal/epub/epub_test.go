package epub

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"grimmoryrodelbridge/internal/testfixture"
)

func openFixture(t *testing.T, entries map[string]string) (*Book, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "source.epub")
	if e := os.WriteFile(file, testfixture.ZIP(entries), 0600); e != nil {
		t.Fatal(e)
	}
	b, e := Open(file)
	if e != nil {
		t.Fatal(e)
	}
	return b, file
}
func TestNavigationCFIAndDerivedContent(t *testing.T) {
	b, source := openFixture(t, testfixture.Entries("one"))
	if len(b.Chapters) != 3 || b.Chapters[1].Spine != 0 || b.Chapters[1].Href != "OPS/Text/a.xhtml#two" {
		t.Fatalf("wrong navigation: %+v", b.Chapters)
	}
	cfi := "epubcfi(/6/2[ref-a]!/4/4/4/1:4)"
	if i, e := b.CFIChapter(cfi); e != nil || i != 1 {
		t.Fatalf("CFI chapter = %d %v", i, e)
	}
	for _, bad := range []string{"epubcfi(/6/2[wrong]!/4/4)", "epubcfi(/6/2!/4/4/4/1:999)", "epubcfi(/6/2!/4,/2/4/1:0,/4/4/1:0)", "epubcfi(/6/2!/4/4~2)"} {
		if _, e := b.CFIChapter(bad); e == nil {
			t.Fatalf("accepted invalid/cross-chapter CFI %s", bad)
		}
	}
	if i, e := b.CFIChapter("epubcfi(/6/2!/4/4/4,/1:0,/1:5)"); e != nil || i != 1 {
		t.Fatalf("range: %d %v", i, e)
	}
	if _, _, e := b.CFIRange("epubcfi(/6/2!/4/4/4,/1:5,/1:0)"); e == nil {
		t.Fatal("accepted reversed CFI range")
	}
	derived := filepath.Join(t.TempDir(), "derived.epub")
	if e := b.Derive(source, derived, "http://adapter/sync/test", "http://adapter"); e != nil {
		t.Fatal(e)
	}
	r, e := zip.OpenReader(derived)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if r.File[0].Name != "mimetype" || r.File[0].Method != zip.Store {
		t.Fatal("invalid mimetype entry")
	}
	before := testfixture.Entries("one")
	for _, f := range r.File {
		in, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(in)
		in.Close()
		if e != nil {
			t.Fatal(e)
		}
		switch f.Name {
		case "OPS/content.opf":
			if !bytes.Contains(data, []byte("urn:test:immutable-original-id")) || bytes.Contains(data, []byte("obsolete")) {
				t.Fatal("identifier or metadata changed incorrectly")
			}
		case "OPS/Text/a.xhtml":
			if bytes.Contains(data, []byte("chapter-index=99")) {
				t.Fatal("old marker retained")
			}
		default:
			if f.Name != "OPS/Text/b.xhtml" && string(data) != before[f.Name] {
				t.Fatalf("resource changed: %s", f.Name)
			}
		}
	}
	after, e := Open(derived)
	if e != nil {
		t.Fatal(e)
	}
	p, _ := b.CFIPosition(cfi)
	q, e := after.CFIPosition(cfi)
	if e != nil || p != q {
		t.Fatalf("derivation moved CFI: %+v %+v %v", p, q, e)
	}
	if b.Chapters[2].Percent >= 99.5 {
		t.Fatal("chapter estimation marks completion")
	}
}
func TestNCXFallbackAndUTF16(t *testing.T) {
	entries := testfixture.Entries("one")
	entries["OPS/content.opf"] = strings.Replace(entries["OPS/content.opf"], ` properties="nav"`, "", 1)
	entries["OPS/content.opf"] = strings.Replace(entries["OPS/content.opf"], `</manifest>`, `<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/></manifest>`, 1)
	entries["OPS/toc.ncx"] = `<ncx><navMap><navPoint><navLabel><text>Nested</text></navLabel><content src="Text/a.xhtml#one"/><navPoint><navLabel><text>Second</text></navLabel><content src="Text/a.xhtml#two"/></navPoint></navPoint></navMap></ncx>`
	b, _ := openFixture(t, entries)
	if len(b.Chapters) != 2 || b.Chapters[1].Title != "Second" {
		t.Fatal("NCX failed")
	}
	delete(entries, "OPS/toc.ncx")
	entries["OPS/content.opf"] = strings.Replace(entries["OPS/content.opf"], `<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>`, "", 1)
	b, _ = openFixture(t, entries)
	if len(b.Chapters) != 2 {
		t.Fatal("spine fallback failed")
	}
	raw := `<?xml version="1.0" encoding="UTF-16"?><html><head/><body><p id="a">字𠮷</p></body></html>`
	var data bytes.Buffer
	data.Write([]byte{0xff, 0xfe})
	for _, unit := range utf16.Encode([]rune(raw)) {
		var tmp [2]byte
		binary.LittleEndian.PutUint16(tmp[:], unit)
		data.Write(tmp[:])
	}
	d, e := parseDocument(data.Bytes())
	if e != nil {
		t.Fatal(e)
	}
	edited, e := edits(d, []edit{{d.IDs["a"].Start, d.IDs["a"].Start, "<!-- marker -->"}})
	if e != nil {
		t.Fatal(e)
	}
	again, e := parseDocument(edited)
	if e != nil || again.IDs["a"].Content() != "字𠮷" || !bytes.HasPrefix(edited, []byte{0xff, 0xfe}) {
		t.Fatalf("UTF16 preservation failed: %v", e)
	}
}
func TestRejectUnsafeArchivesAndAnchors(t *testing.T) {
	for _, which := range []string{"path", "anchor", "duplicate-nav"} {
		t.Run(which, func(t *testing.T) {
			entries := testfixture.Entries("one")
			switch which {
			case "path":
				entries["../secret"] = "bad"
			case "anchor":
				entries["OPS/Navigation/nav.xhtml"] = strings.Replace(entries["OPS/Navigation/nav.xhtml"], "#two", "#absent", 1)
			case "duplicate-nav":
				entries["OPS/Navigation/nav.xhtml"] = strings.Replace(entries["OPS/Navigation/nav.xhtml"], "#two", "#one", 1)
			}
			file := filepath.Join(t.TempDir(), "bad.epub")
			os.WriteFile(file, testfixture.ZIP(entries), 0600)
			if _, e := Open(file); e == nil {
				t.Fatal("accepted malformed EPUB")
			}
		})
	}
	for _, ref := range []string{"../../../../etc/passwd", "/etc/passwd", "file:///etc/passwd", "https://outside/", "..%2f..%2f..%2fsecret"} {
		if _, _, e := Resolve("OPS/Text/a.xhtml", ref); e == nil {
			t.Fatalf("unsafe href %q", ref)
		}
	}
}
