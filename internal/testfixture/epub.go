// Package testfixture contains only synthetic, redistributable protocol fixtures.
package testfixture

import (
	"archive/zip"
	"bytes"
	"fmt"
	"sort"
)

func Entries(version string) map[string]string {
	return map[string]string{
		"mimetype":                 "application/epub+zip",
		"META-INF/container.xml":   `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"OPS/content.opf":          `<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="uid" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="uid">urn:test:immutable-original-id</dc:identifier><dc:title>Synthetic fixture</dc:title><dc:language>en</dc:language><meta name="legado:book-url" content="obsolete"/></metadata><manifest><item id="a" href="Text/a.xhtml" media-type="application/xhtml+xml"/><item id="b" href="Text/b.xhtml" media-type="application/xhtml+xml"/><item id="nav" href="Navigation/nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="css" href="Styles/main.css" media-type="text/css"/><item id="svg" href="Images/test.svg" media-type="image/svg+xml"/><item id="font" href="Fonts/test.woff2" media-type="font/woff2"/></manifest><spine><itemref id="ref-a" idref="a"/><itemref id="ref-b" idref="b"/></spine></package>`,
		"OPS/Navigation/nav.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc"><ol><li><a href="../Text/a.xhtml#one">One</a><ol><li><a href="../Text/a.xhtml#two">Two</a></li></ol></li><li><a href="../Text/b.xhtml#three">Three</a></li></ol></nav></body></html>`,
		"OPS/Text/a.xhtml":         fmt.Sprintf(`<html xmlns="http://www.w3.org/1999/xhtml"><head><title>A</title><link rel="stylesheet" href="../Styles/main.css"/></head><body><section id="one"><h1>One</h1><p>First %s.</p><img src="../Images/test.svg"/></section><!-- legado:chapter-index=99 --><section id="two"><h1>Two</h1><p>abc𠮷def</p></section></body></html>`, version),
		"OPS/Text/b.xhtml":         `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>B</title></head><body><section id="three"><h1>Three</h1><p>Last chapter is not completion.</p></section></body></html>`,
		"OPS/Styles/main.css":      `@font-face{font-family:Test;src:url('../Fonts/test.woff2')}body{font-family:Test}h1{color:blue}`,
		"OPS/Images/test.svg":      `<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10" fill="green"/></svg>`,
		"OPS/Fonts/test.woff2":     "wOF2\x00synthetic-byte-preservation-test",
	}
}
func ZIP(entries map[string]string) []byte {
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	f, _ := w.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	f.Write([]byte(entries["mimetype"]))
	keys := make([]string, 0, len(entries))
	for k := range entries {
		if k != "mimetype" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		f, _ := w.Create(k)
		f.Write([]byte(entries[k]))
	}
	w.Close()
	return out.Bytes()
}
func EPUB(version string) []byte { return ZIP(Entries(version)) }
