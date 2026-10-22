// Package epub maps unmodified EPUB DOM positions to flattened navigation entries.
// It does not render books or synthesize precise CFIs from chapter-only updates.
package epub

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const MappingVersion = "epub-map-1"
const maxEntry = 32 << 20
const maxExpanded = 512 << 20

type Node struct {
	Name                  xml.Name
	Attrs                 []xml.Attr
	Text                  string
	Children              []*Node
	Parent                *Node
	Start, OpenEnd, End   int
	Position, EndPosition int
}

func (n *Node) Attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
func (n *Node) Elements(name string) []*Node {
	var out []*Node
	for _, c := range n.Children {
		if c.Name.Local != "" && (name == "" || c.Name.Local == name) {
			out = append(out, c)
		}
	}
	return out
}
func walk(n *Node, fn func(*Node)) {
	fn(n)
	for _, c := range n.Children {
		walk(c, fn)
	}
}
func (n *Node) Content() string {
	if n.Name.Local == "" {
		return n.Text
	}
	var b strings.Builder
	walk(n, func(c *Node) {
		if c.Name.Local == "" {
			b.WriteString(c.Text)
		}
	})
	return strings.Join(strings.Fields(b.String()), " ")
}

type Document struct {
	Root         *Node
	IDs          map[string]*Node
	Raw          []byte
	Encoding     string
	BOM          bool
	Size         int
	PositionSize int
	Nodes        int
	Markers      []edit
}

// Decode UTF-16 only when marked by BOM or the XML signature. Edits are encoded
// back to the original encoding, with the original XML declaration untouched.
func decodeXML(raw []byte) ([]byte, string, bool, error) {
	enc, bom := "utf8", false
	if bytes.HasPrefix(raw, []byte{0xff, 0xfe}) {
		enc, bom = "le", true
		raw = raw[2:]
	} else if bytes.HasPrefix(raw, []byte{0xfe, 0xff}) {
		enc, bom = "be", true
		raw = raw[2:]
	} else if bytes.HasPrefix(raw, []byte{0xef, 0xbb, 0xbf}) {
		bom = true
		raw = raw[3:]
	} else if len(raw) > 3 && raw[0] == '<' && raw[1] == 0 {
		enc = "le"
	} else if len(raw) > 3 && raw[0] == 0 && raw[1] == '<' {
		enc = "be"
	}
	if enc == "utf8" {
		if !utf8.Valid(raw) {
			return nil, "", false, fmt.Errorf("UNSUPPORTED_EPUB_ENCODING")
		}
		return raw, enc, bom, nil
	}
	if len(raw)%2 != 0 {
		return nil, "", false, fmt.Errorf("INVALID_XML_ENCODING")
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		if enc == "le" {
			units[i] = binary.LittleEndian.Uint16(raw[2*i:])
		} else {
			units[i] = binary.BigEndian.Uint16(raw[2*i:])
		}
	}
	return []byte(string(utf16.Decode(units))), enc, bom, nil
}
func (d *Document) encode(raw []byte) []byte {
	if d.Encoding == "utf8" {
		if d.BOM {
			return append([]byte{0xef, 0xbb, 0xbf}, raw...)
		}
		return raw
	}
	units := utf16.Encode([]rune(string(raw)))
	out := make([]byte, 0, 2*len(units)+2)
	if d.BOM {
		if d.Encoding == "le" {
			out = append(out, 0xff, 0xfe)
		} else {
			out = append(out, 0xfe, 0xff)
		}
	}
	for _, u := range units {
		var b [2]byte
		if d.Encoding == "le" {
			binary.LittleEndian.PutUint16(b[:], u)
		} else {
			binary.BigEndian.PutUint16(b[:], u)
		}
		out = append(out, b[:]...)
	}
	return out
}
func parseDocument(input []byte) (*Document, error) {
	raw, enc, bom, err := decodeXML(input)
	if err != nil {
		return nil, err
	}
	doc := &Document{IDs: map[string]*Node{}, Raw: raw, Encoding: enc, BOM: bom, Size: len(input)}
	d := xml.NewDecoder(bytes.NewReader(raw))
	d.Entity = xml.HTMLEntity
	d.CharsetReader = func(label string, in io.Reader) (io.Reader, error) {
		label = strings.ToLower(label)
		if label == "utf-8" || label == "us-ascii" || (enc != "utf8" && strings.HasPrefix(label, "utf-16")) {
			return in, nil
		}
		return nil, fmt.Errorf("UNSUPPORTED_EPUB_ENCODING")
	}
	var stack []*Node
	position, nodes := 0, 0
	for {
		before := int(d.InputOffset())
		tok, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, fmt.Errorf("INVALID_EPUB_XML")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			nodes++
			if nodes > 200000 || len(stack) > 256 {
				return nil, fmt.Errorf("EPUB_STRUCTURE_LIMIT")
			}
			n := &Node{Name: t.Name, Attrs: append([]xml.Attr(nil), t.Attr...), Start: before, OpenEnd: int(d.InputOffset()), Position: position}
			position++
			if len(stack) > 0 {
				n.Parent = stack[len(stack)-1]
				n.Parent.Children = append(n.Parent.Children, n)
			} else {
				if doc.Root != nil {
					return nil, fmt.Errorf("MULTIPLE_XML_ROOTS")
				}
				doc.Root = n
			}
			if id := n.Attr("id"); id != "" {
				if doc.IDs[id] != nil {
					return nil, fmt.Errorf("DUPLICATE_EPUB_ANCHOR")
				}
				doc.IDs[id] = n
			}
			stack = append(stack, n)
		case xml.CharData:
			if len(stack) > 0 {
				s := string(t)
				parent := stack[len(stack)-1]
				n := &Node{Text: s, Parent: parent, Position: position, Start: before, End: int(d.InputOffset())}
				position += len(utf16.Encode([]rune(s)))
				n.EndPosition = position
				parent.Children = append(parent.Children, n)
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("INVALID_XML_END")
			}
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			n.End = int(d.InputOffset())
			n.EndPosition = position
			position++
		case xml.Comment:
			if strings.HasPrefix(strings.TrimSpace(string(t)), "legado:") {
				doc.Markers = append(doc.Markers, edit{before, int(d.InputOffset()), ""})
			}
		}
	}
	if doc.Root == nil || len(stack) != 0 {
		return nil, fmt.Errorf("INVALID_EPUB_XML")
	}
	doc.PositionSize = position
	doc.Nodes = nodes
	return doc, nil
}

type Chapter struct {
	Index        int     `json:"index"`
	Title        string  `json:"title"`
	Href         string  `json:"url"`
	Spine        int     `json:"-"`
	Position     int     `json:"-"`
	Percent      float64 `json:"-"`
	StartPercent float64 `json:"-"`
}
type Section struct {
	Path, ID string
	Size     int64
	Linear   bool
	Ref      *Node
}
type Book struct {
	SourceHash, Revision, OPF string
	Chapters                  []Chapter
	Sections                  []Section
	Docs                      map[string]*Document
	Files                     map[string]*zip.File
	Package                   *Document
	Metadata                  *Node
}

// CacheWeight bounds retained parsed documents as well as compressed files.
// Include a conservative allowance for DOM strings, attributes and map entries.
func (b *Book) CacheWeight() int64 {
	weight := int64(len(b.Files)*512 + len(b.Chapters)*512)
	count := func(d *Document) {
		weight += int64(len(d.Raw)) * 3
		walk(d.Root, func(n *Node) { weight += int64(256 + len(n.Text)*2 + len(n.Attrs)*128) })
	}
	count(b.Package)
	for _, doc := range b.Docs {
		count(doc)
	}
	return weight
}

func safePath(name string) bool {
	return name != "" && !strings.HasPrefix(name, "/") && !strings.Contains(name, "\\") && !strings.ContainsRune(name, 0) && !strings.Contains(name, ":") && path.Clean(name) == name && name != ".." && !strings.HasPrefix(name, "../")
}
func Resolve(base, ref string) (string, string, error) {
	u, err := url.Parse(ref)
	if err != nil || u.IsAbs() || u.Opaque != "" || u.Host != "" || u.RawQuery != "" || strings.HasPrefix(u.Path, "/") {
		return "", "", fmt.Errorf("INVALID_EPUB_HREF")
	}
	p := u.Path
	if p == "" {
		p = base
	} else {
		p = path.Join(path.Dir(base), p)
	}
	if !safePath(p) {
		return "", "", fmt.Errorf("INVALID_EPUB_PATH")
	}
	return p, u.Fragment, nil
}
func escapedHref(p, fragment string) string {
	u := &url.URL{Path: p, Fragment: fragment}
	return u.String()
}
func readEntry(f *zip.File) ([]byte, error) {
	if f == nil || f.UncompressedSize64 > maxEntry {
		return nil, fmt.Errorf("EPUB_ENTRY_LIMIT")
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, maxEntry+1))
	if len(b) > maxEntry {
		return nil, fmt.Errorf("EPUB_ENTRY_LIMIT")
	}
	return b, err
}

func Open(filename string) (*Book, error) {
	r, err := zip.OpenReader(filename)
	if err != nil {
		return nil, fmt.Errorf("INVALID_EPUB_ZIP")
	}
	defer r.Close()
	b := &Book{Docs: map[string]*Document{}, Files: map[string]*zip.File{}}
	var total uint64
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !safePath(f.Name) || !f.Mode().IsRegular() || b.Files[f.Name] != nil {
			return nil, fmt.Errorf("UNSAFE_EPUB_ENTRY")
		}
		if f.UncompressedSize64 > maxEntry {
			return nil, fmt.Errorf("EPUB_ENTRY_LIMIT")
		}
		total += f.UncompressedSize64
		if total > maxExpanded || len(b.Files) >= 20000 {
			return nil, fmt.Errorf("EPUB_ARCHIVE_LIMIT")
		}
		b.Files[f.Name] = f
	}
	mimetype, e := readEntry(b.Files["mimetype"])
	if e != nil || string(mimetype) != "application/epub+zip" || b.Files["mimetype"].Method != zip.Store {
		return nil, fmt.Errorf("INVALID_EPUB_MIMETYPE")
	}
	// Do not retain zip.File handles beyond this method; all reading documents is eager.
	docBytes, docNodes := 0, 0
	load := func(name string) (*Document, error) {
		raw, e := readEntry(b.Files[name])
		if e != nil {
			return nil, e
		}
		d, e := parseDocument(raw)
		if e != nil {
			return nil, e
		}
		docBytes += len(d.Raw)
		docNodes += d.Nodes
		if docBytes > 32<<20 || docNodes > 200000 {
			return nil, fmt.Errorf("EPUB_DOCUMENT_LIMIT")
		}
		return d, nil
	}
	container, err := load("META-INF/container.xml")
	if err != nil {
		return nil, err
	}
	walk(container.Root, func(n *Node) {
		if n.Name.Local == "rootfile" && b.OPF == "" {
			b.OPF = n.Attr("full-path")
		}
	})
	if !safePath(b.OPF) {
		return nil, fmt.Errorf("INVALID_EPUB_PACKAGE")
	}
	b.Package, err = load(b.OPF)
	if err != nil {
		return nil, err
	}
	if b.Package.Root.Name.Local != "package" {
		return nil, fmt.Errorf("INVALID_EPUB_PACKAGE")
	}
	var manifest, spine *Node
	for _, n := range b.Package.Root.Children {
		switch n.Name.Local {
		case "manifest":
			manifest = n
		case "spine":
			spine = n
		case "metadata":
			b.Metadata = n
		}
	}
	if manifest == nil || spine == nil || b.Metadata == nil {
		return nil, fmt.Errorf("INVALID_EPUB_PACKAGE")
	}
	items := map[string]*Node{}
	var navPath, ncxPath string
	for _, n := range manifest.Elements("item") {
		id := n.Attr("id")
		if id == "" || items[id] != nil {
			return nil, fmt.Errorf("INVALID_MANIFEST_ID")
		}
		items[id] = n
		href, _, e := Resolve(b.OPF, n.Attr("href"))
		if e != nil {
			return nil, e
		}
		if b.Files[href] == nil {
			return nil, fmt.Errorf("MISSING_EPUB_RESOURCE")
		}
		if n.Attr("media-type") == "application/x-dtbncx+xml" {
			ncxPath = href
		}
		if containsWord(n.Attr("properties"), "nav") {
			navPath = href
		}
	}
	for _, n := range spine.Elements("itemref") {
		item := items[n.Attr("idref")]
		if item == nil {
			return nil, fmt.Errorf("INVALID_EPUB_SPINE")
		}
		p, _, e := Resolve(b.OPF, item.Attr("href"))
		if e != nil {
			return nil, e
		}
		if item.Attr("media-type") != "application/xhtml+xml" && item.Attr("media-type") != "text/html" {
			return nil, fmt.Errorf("UNSUPPORTED_SPINE_MEDIA")
		}
		doc, e := load(p)
		if e != nil {
			return nil, e
		}
		b.Docs[p] = doc
		b.Sections = append(b.Sections, Section{Path: p, ID: n.Attr("idref"), Size: int64(doc.Size), Linear: n.Attr("linear") != "no", Ref: n})
	}
	if len(b.Sections) == 0 {
		return nil, fmt.Errorf("EMPTY_EPUB_SPINE")
	}
	if navPath != "" {
		doc, e := load(navPath)
		if e != nil {
			return nil, e
		}
		var nav *Node
		walk(doc.Root, func(n *Node) {
			if n.Name.Local == "nav" && containsWord(n.Attr("type"), "toc") {
				nav = n
			}
		})
		if nav == nil {
			return nil, fmt.Errorf("MISSING_EPUB_NAV")
		}
		var navErr error
		walk(nav, func(n *Node) {
			if n.Name.Local == "a" && n.Attr("href") != "" && navErr == nil {
				navErr = b.addChapter(navPath, n.Attr("href"), n.Content())
			}
		})
		if navErr != nil {
			return nil, navErr
		}
	} else if ncxPath != "" {
		doc, e := load(ncxPath)
		if e != nil {
			return nil, e
		}
		var navErr error
		walk(doc.Root, func(n *Node) {
			if n.Name.Local == "navPoint" && navErr == nil {
				var title, href string
				for _, child := range n.Elements("") {
					if child.Name.Local == "navLabel" {
						title = child.Content()
					}
					if child.Name.Local == "content" {
						href = child.Attr("src")
					}
				}
				if href != "" {
					navErr = b.addChapter(ncxPath, href, title)
				}
			}
		})
		if navErr != nil {
			return nil, navErr
		}
	}
	if len(b.Chapters) == 0 {
		for i, s := range b.Sections {
			if !s.Linear {
				continue
			}
			doc := b.Docs[s.Path]
			title := ""
			walk(doc.Root, func(n *Node) {
				if title == "" && (n.Name.Local == "h1" || n.Name.Local == "title") {
					title = n.Content()
				}
			})
			if title == "" {
				title = fmt.Sprintf("Chapter %d", i+1)
			}
			b.Chapters = append(b.Chapters, Chapter{Title: title, Href: escapedHref(s.Path, ""), Spine: i, Position: 0})
		}
	}
	var sizeTotal float64
	for _, s := range b.Sections {
		if s.Linear {
			sizeTotal += float64(s.Size)
		}
	}
	if sizeTotal == 0 {
		return nil, fmt.Errorf("EMPTY_EPUB_CONTENT")
	}
	for i := range b.Chapters {
		c := &b.Chapters[i]
		c.Index = i
		if i > 0 {
			p := b.Chapters[i-1]
			if c.Spine < p.Spine || (c.Spine == p.Spine && c.Position <= p.Position) {
				return nil, fmt.Errorf("AMBIGUOUS_NAVIGATION")
			}
		}
		before := 0.0
		for j := 0; j < c.Spine; j++ {
			if b.Sections[j].Linear {
				before += float64(b.Sections[j].Size)
			}
		}
		section := b.Sections[c.Spine]
		fraction := float64(c.Position) / float64(b.Docs[section.Path].PositionSize)
		size := float64(section.Size)
		if !section.Linear {
			size = 0
		}
		c.StartPercent = math.Min(100, math.Max(0, 100*(before+fraction*size)/sizeTotal))
		c.Percent = math.Min(99.4, c.StartPercent)
	}
	b.Files = nil
	return b, nil
}
func containsWord(s, word string) bool {
	for _, w := range strings.Fields(s) {
		if w == word {
			return true
		}
	}
	return false
}
func (b *Book) addChapter(base, href, title string) error {
	p, fragment, err := Resolve(base, href)
	if err != nil {
		return err
	}
	spine := -1
	for i, s := range b.Sections {
		if s.Path == p {
			spine = i
			break
		}
	}
	if spine < 0 {
		return fmt.Errorf("NAV_OUTSIDE_SPINE")
	}
	position := 0
	if fragment != "" {
		n := b.Docs[p].IDs[fragment]
		if n == nil {
			return fmt.Errorf("INVALID_EPUB_ANCHOR")
		}
		position = n.Position
	}
	if title == "" {
		title = fmt.Sprintf("Chapter %d", len(b.Chapters)+1)
	}
	b.Chapters = append(b.Chapters, Chapter{Title: title, Href: escapedHref(p, fragment), Spine: spine, Position: position})
	return nil
}

// Position describes a DOM point using UTF-16 text offsets, as browser CFIs do.
type Position struct{ Spine, Offset int }

func (b *Book) HrefPosition(href string) (Position, error) {
	// Grimmory/Foliate publishes ZIP-root-relative hrefs, not OPF-relative hrefs.
	u, e := url.Parse(href)
	if e != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" {
		return Position{}, fmt.Errorf("INVALID_PROGRESS_HREF")
	}
	p := strings.TrimPrefix(u.Path, "./")
	if !safePath(p) {
		return Position{}, fmt.Errorf("INVALID_PROGRESS_HREF")
	}
	for i, s := range b.Sections {
		if s.Path == p {
			offset := 0
			if u.Fragment != "" {
				n := b.Docs[p].IDs[u.Fragment]
				if n == nil {
					return Position{}, fmt.Errorf("INVALID_PROGRESS_ANCHOR")
				}
				offset = n.Position
			}
			return Position{i, offset}, nil
		}
	}
	return Position{}, fmt.Errorf("PROGRESS_OUTSIDE_EPUB")
}
func (b *Book) ChapterAt(p Position) (int, error) {
	if p.Spine < 0 || p.Spine >= len(b.Sections) || p.Offset < 0 || p.Offset > b.Docs[b.Sections[p.Spine].Path].PositionSize {
		return 0, fmt.Errorf("INVALID_EPUB_POSITION")
	}
	index := -1
	for i, c := range b.Chapters {
		if c.Spine < p.Spine || c.Spine == p.Spine && c.Position <= p.Offset {
			index = i
		} else {
			break
		}
	}
	if index < 0 {
		return 0, fmt.Errorf("POSITION_BEFORE_NAVIGATION")
	}
	return index, nil
}
func (b *Book) PercentChapter(percent float64) (int, error) {
	if math.IsNaN(percent) || math.IsInf(percent, 0) || percent < 0 || percent > 100 {
		return 0, fmt.Errorf("INVALID_PROGRESS_PERCENT")
	}
	index := 0
	for i, c := range b.Chapters {
		if c.StartPercent <= percent {
			index = i
		} else {
			break
		}
	}
	return index, nil
}

type edit struct {
	start, end int
	text       string
}

func edits(d *Document, changes []edit) ([]byte, error) {
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].start < changes[j].start })
	var out bytes.Buffer
	last := 0
	for _, e := range changes {
		if e.start < last || e.end < e.start || e.end > len(d.Raw) {
			return nil, fmt.Errorf("INVALID_EPUB_EDIT")
		}
		out.Write(d.Raw[last:e.start])
		out.WriteString(e.text)
		last = e.end
	}
	out.Write(d.Raw[last:])
	return d.encode(out.Bytes()), nil
}
func escape(s string) string { var b bytes.Buffer; xml.EscapeText(&b, []byte(s)); return b.String() }
func (b *Book) Derive(source, destination, syncURL, serverURL string) error {
	metadata := map[string]string{"legado:book-url": syncURL, "legado:server-url": serverURL, "legado:book-source": "Grimmory", "legado:chapter-count": strconv.Itoa(len(b.Chapters))}
	metaTag := "meta"
	opening := string(b.Package.Raw[b.Metadata.Start:b.Metadata.OpenEnd])
	tag := strings.Fields(strings.TrimPrefix(opening, "<"))[0]
	if i := strings.IndexByte(tag, ':'); i >= 0 {
		metaTag = tag[:i+1] + "meta"
	}
	var injected strings.Builder
	keys := []string{"legado:book-url", "legado:server-url", "legado:book-source", "legado:chapter-count"}
	for _, key := range keys {
		fmt.Fprintf(&injected, "<%s name=\"%s\" content=\"%s\"/>", metaTag, key, escape(metadata[key]))
	}
	var changes []edit
	for _, n := range b.Metadata.Elements("meta") {
		if strings.HasPrefix(n.Attr("name"), "legado:") {
			changes = append(changes, edit{n.Start, n.End, ""})
		}
	}
	closeStart := bytes.LastIndex(b.Package.Raw[b.Metadata.OpenEnd:b.Metadata.End], []byte("</"))
	if closeStart < 0 {
		return fmt.Errorf("EMPTY_EPUB_METADATA")
	}
	closeStart += b.Metadata.OpenEnd
	changes = append(changes, edit{closeStart, closeStart, injected.String()})
	opf, e := edits(b.Package, changes)
	if e != nil {
		return e
	}
	modified := map[string][]byte{b.OPF: opf}
	for _, s := range b.Sections {
		d := b.Docs[s.Path]
		changes := append([]edit{}, d.Markers...)
		for _, c := range b.Chapters {
			if c.Spine >= len(b.Sections) || b.Sections[c.Spine].Path != s.Path {
				continue
			}
			_, fragment, e := Resolve("root.xml", c.Href)
			if e != nil {
				return e
			}
			at := 0
			if fragment != "" {
				at = d.IDs[fragment].Start
			} else {
				var body *Node
				walk(d.Root, func(n *Node) {
					if n.Name.Local == "body" {
						body = n
					}
				})
				if body == nil {
					return fmt.Errorf("MISSING_XHTML_BODY")
				}
				at = body.OpenEnd
			}
			changes = append(changes, edit{at, at, fmt.Sprintf("<!-- legado:chapter-index=%d --><!-- legado:status=downloaded -->", c.Index)})
		}
		if len(changes) > 0 {
			modified[s.Path], e = edits(d, changes)
			if e != nil {
				return e
			}
		}
	}
	return rewriteZIP(source, destination, modified)
}

func rewriteZIP(source, destination string, modified map[string][]byte) (err error) {
	r, e := zip.OpenReader(source)
	if e != nil {
		return e
	}
	defer r.Close()
	out, e := createPrivate(destination)
	if e != nil {
		return e
	}
	defer func() {
		if e := out.Close(); err == nil {
			err = e
		}
	}()
	w := zip.NewWriter(out)
	defer func() {
		if e := w.Close(); err == nil {
			err = e
		}
	}()
	// EPUB requires the uncompressed mimetype entry first.
	for pass := 0; pass < 2; pass++ {
		for _, f := range r.File {
			if (f.Name == "mimetype") != (pass == 0) {
				continue
			}
			if f.FileInfo().IsDir() {
				// Empty directories may still contain a nonempty compressed stream.
				// Copy cannot write that stream to a directory; recreate its header
				// instead, preserving metadata while normalizing it to empty Store.
				h := f.FileHeader
				h.CRC32 = 0
				if _, e = w.CreateHeader(&h); e != nil {
					return e
				}
				continue
			}
			data, changed := modified[f.Name]
			if !changed {
				if e = w.Copy(f); e != nil {
					return e
				}
				continue
			}
			h := f.FileHeader
			h.CRC32 = 0
			h.CompressedSize64 = 0
			h.UncompressedSize64 = 0
			entry, e := w.CreateHeader(&h)
			if e != nil {
				return e
			}
			if _, e = entry.Write(data); e != nil {
				return e
			}
		}
	}
	return nil
}
func Revision(sourceHash string) string {
	sum := sha256.Sum256([]byte(MappingVersion + ":" + sourceHash))
	return hex.EncodeToString(sum[:])
}
