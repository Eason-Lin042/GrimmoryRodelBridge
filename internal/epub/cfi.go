package epub

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
)

type step struct {
	index, offset int
	id            string
}

// Split outside assertions: commas/! escaped with ^ in assertions are not syntax.
func cfiSplit(s string, delimiter byte) ([]string, error) {
	var out []string
	start, depth := 0, 0
	escaped := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '^' {
			escaped = true
			continue
		}
		if ch == '[' {
			depth++
			if depth > 1 {
				return nil, fmt.Errorf("INVALID_CFI")
			}
		} else if ch == ']' {
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("INVALID_CFI")
			}
		} else if ch == delimiter && depth == 0 {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if escaped || depth != 0 {
		return nil, fmt.Errorf("INVALID_CFI")
	}
	return append(out, s[start:]), nil
}
func cfiSteps(s string) ([]step, error) {
	var out []step
	for len(s) > 0 {
		if s[0] != '/' {
			return nil, fmt.Errorf("UNSUPPORTED_CFI")
		}
		s = s[1:]
		end := 0
		for end < len(s) && s[end] >= '0' && s[end] <= '9' {
			end++
		}
		if end == 0 {
			return nil, fmt.Errorf("INVALID_CFI")
		}
		index, err := strconv.Atoi(s[:end])
		if err != nil || index < 0 {
			return nil, fmt.Errorf("INVALID_CFI")
		}
		s = s[end:]
		v := step{index: index}
		if len(s) > 0 && s[0] == '[' {
			var text strings.Builder
			i := 1
			for ; i < len(s) && s[i] != ']'; i++ {
				if s[i] == '^' {
					i++
					if i >= len(s) {
						return nil, fmt.Errorf("INVALID_CFI")
					}
				}
				text.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, fmt.Errorf("INVALID_CFI")
			}
			v.id = strings.Split(text.String(), ";")[0]
			s = s[i+1:]
		}
		if len(s) > 0 && s[0] == ':' {
			s = s[1:]
			end = 0
			for end < len(s) && s[end] >= '0' && s[end] <= '9' {
				end++
			}
			if end == 0 {
				return nil, fmt.Errorf("INVALID_CFI")
			}
			v.offset, err = strconv.Atoi(s[:end])
			if err != nil {
				return nil, err
			}
			s = s[end:]
			if len(s) > 0 && s[0] == '[' {
				parts, e := cfiSplit(s, '/')
				if e != nil {
					return nil, e
				}
				assertion := parts[0]
				if !strings.HasSuffix(assertion, "]") {
					return nil, fmt.Errorf("UNSUPPORTED_CFI")
				}
				s = s[len(assertion):]
			}
		}
		out = append(out, v)
		if len(out) > 256 {
			return nil, fmt.Errorf("CFI_LIMIT")
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("INVALID_CFI")
	}
	return out, nil
}
func cfiPoint(doc *Document, steps []step) (*Node, int, error) {
	node := doc.Root
	for i, v := range steps {
		if node == nil || node.Name.Local == "" {
			return nil, 0, fmt.Errorf("INVALID_CFI_NODE")
		}
		if v.id != "" {
			target := doc.IDs[v.id]
			if target == nil {
				return nil, 0, fmt.Errorf("INVALID_CFI_ASSERTION")
			} // Require assertions to match the numeric path, rather than jumping to an unrelated ID.
			_ = target
		}
		if v.index%2 == 0 {
			elements := node.Elements("")
			j := v.index/2 - 1
			if j < 0 || j >= len(elements) {
				return nil, 0, fmt.Errorf("UNSUPPORTED_CFI_BOUNDARY")
			}
			node = elements[j]
			if v.id != "" && node.Attr("id") != v.id {
				return nil, 0, fmt.Errorf("INVALID_CFI_ASSERTION")
			}
			if v.offset != 0 {
				return nil, 0, fmt.Errorf("INVALID_CFI_OFFSET")
			}
		} else {
			if i != len(steps)-1 {
				return nil, 0, fmt.Errorf("INVALID_CFI_TEXT")
			}
			chunk := (v.index - 1) / 2
			elements := 0
			var text []*Node
			for _, child := range node.Children {
				if child.Name.Local != "" {
					elements++
				} else if elements == chunk {
					text = append(text, child)
				}
			}
			if len(text) == 0 {
				return nil, 0, fmt.Errorf("UNSUPPORTED_CFI_BOUNDARY")
			}
			offset := v.offset
			for _, n := range text {
				length := len(utf16.Encode([]rune(n.Text)))
				if offset <= length {
					return n, n.Position + offset, nil
				}
				offset -= length
			}
			return nil, 0, fmt.Errorf("INVALID_CFI_OFFSET")
		}
	}
	return node, node.Position, nil
}

func (b *Book) CFIPosition(cfi string) (Position, error) { return b.cfiPosition(cfi, false) }
func (b *Book) cfiPosition(cfi string, toEnd bool) (Position, error) {
	if len(cfi) > 4096 || !strings.HasPrefix(cfi, "epubcfi(") || !strings.HasSuffix(cfi, ")") {
		return Position{}, fmt.Errorf("INVALID_CFI")
	}
	s := cfi[len("epubcfi(") : len(cfi)-1]
	ranges, err := cfiSplit(s, ',')
	if err != nil {
		return Position{}, err
	}
	if len(ranges) == 3 {
		branch := ranges[1]
		if toEnd {
			branch = ranges[2]
		}
		s = ranges[0] + branch
	} else if len(ranges) != 1 {
		return Position{}, fmt.Errorf("INVALID_CFI_RANGE")
	}
	parts, err := cfiSplit(s, '!')
	if err != nil || len(parts) != 2 {
		return Position{}, fmt.Errorf("UNSUPPORTED_CFI_INDIRECTION")
	}
	pkg, err := cfiSteps(parts[0])
	if err != nil {
		return Position{}, err
	}
	ref, _, err := cfiPoint(b.Package, pkg)
	if err != nil {
		return Position{}, err
	}
	spine := -1
	for i, section := range b.Sections {
		if section.Ref == ref {
			spine = i
			break
		}
	}
	if spine < 0 {
		return Position{}, fmt.Errorf("CFI_OUTSIDE_SPINE")
	}
	steps, err := cfiSteps(parts[1])
	if err != nil {
		return Position{}, err
	}
	_, position, err := cfiPoint(b.Docs[b.Sections[spine].Path], steps)
	return Position{spine, position}, err
}

// CFIRange validates both endpoints, including their reading order. Foliate
// saves the visible viewport, which can span multiple navigation chapters.
func (b *Book) CFIRange(cfi string) (Position, Position, error) {
	start, e := b.cfiPosition(cfi, false)
	if e != nil {
		return Position{}, Position{}, e
	}
	end, e := b.cfiPosition(cfi, true)
	if e != nil {
		return Position{}, Position{}, e
	}
	if start.Spine != end.Spine || start.Offset > end.Offset {
		return Position{}, Position{}, fmt.Errorf("INVALID_CFI_RANGE_ORDER")
	}
	return start, end, nil
}

// CFIChapter requires one unambiguous chapter, as needed for CFI preservation.
func (b *Book) CFIChapter(cfi string) (int, error) {
	p, end, e := b.CFIRange(cfi)
	if e != nil {
		return 0, e
	}
	start, e := b.ChapterAt(p)
	if e != nil {
		return 0, e
	}
	endIndex, e := b.ChapterAt(end)
	if e != nil || endIndex != start {
		return 0, fmt.Errorf("CFI_CROSSES_CHAPTER")
	}
	return start, nil
}
