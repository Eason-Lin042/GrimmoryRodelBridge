package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"grimmoryrodelbridge/internal/epub"
	"grimmoryrodelbridge/internal/grimmory"
	"grimmoryrodelbridge/internal/store"
)

func (a *Server) syncURL(book, file int64, rev string) string {
	return fmt.Sprintf("%s/sync/%s/books/%d/files/%d/revisions/%s", a.Config.PublicURL, a.instance, book, file, rev)
}
func (a *Server) parseSync(raw string) (book, file int64, rev string, err error) {
	prefix := a.Config.PublicURL + "/sync/" + a.instance + "/books/"
	if !strings.HasPrefix(raw, prefix) {
		return 0, 0, "", fail("INVALID_SYNC_IDENTITY", 400)
	}
	parts := strings.Split(strings.TrimPrefix(raw, prefix), "/")
	if len(parts) != 5 || parts[1] != "files" || parts[3] != "revisions" {
		return 0, 0, "", fail("INVALID_SYNC_IDENTITY", 400)
	}
	book, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil || book <= 0 {
		return 0, 0, "", fail("INVALID_SYNC_IDENTITY", 400)
	}
	file, err = strconv.ParseInt(parts[2], 10, 64)
	if err != nil || file <= 0 {
		return 0, 0, "", fail("INVALID_SYNC_IDENTITY", 400)
	}
	rev = parts[4]
	decoded, e := hex.DecodeString(rev)
	if e != nil || len(decoded) != 32 || raw != a.syncURL(book, file, rev) {
		return 0, 0, "", fail("INVALID_SYNC_IDENTITY", 400)
	}
	return book, file, rev, nil
}
func positionHash(p *grimmory.Progress) string {
	data, _ := json.Marshal(p)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
func progressChapter(p *grimmory.Progress, book *epub.Book) (int, error) {
	if p == nil {
		return 0, fail("PROGRESS_SOURCE_UNKNOWN", 409)
	}
	if p.Percent != nil && (math.IsNaN(*p.Percent) || math.IsInf(*p.Percent, 0) || *p.Percent < 0 || *p.Percent > 100) {
		return 0, fail("INVALID_PROGRESS_PERCENT", 409)
	}
	if p.CFI != "" {
		i, e := book.CFIChapter(p.CFI)
		if e != nil {
			// A real Foliate spread may start in the previous chapter and end
			// inside the current one. An exact navigation anchor contained in
			// that validated viewport disambiguates chapter-level restoration.
			// This does not authorize preserving a cross-chapter CFI on save.
			start, end, rangeErr := book.CFIRange(p.CFI)
			if rangeErr == nil && strings.Contains(p.Href, "#") {
				point, hrefErr := book.HrefPosition(p.Href)
				if hrefErr == nil && point.Spine == start.Spine && point.Offset >= start.Offset && point.Offset <= end.Offset {
					if index, chapterErr := book.ChapterAt(point); chapterErr == nil {
						return index, nil
					}
				}
			}
			return 0, fail("INVALID_PROGRESS_CFI", 409)
		}
		if p.Href != "" {
			point, e := book.HrefPosition(p.Href)
			if e != nil {
				return 0, fail("INVALID_PROGRESS_HREF", 409)
			} // A document-only href identifies a section, not necessarily its first chapter.
			if point.Spine != book.Chapters[i].Spine {
				return 0, fail("CONFLICTING_PROGRESS_POSITION", 409)
			}
			if strings.Contains(p.Href, "#") {
				j, e := book.ChapterAt(point)
				if e != nil || i != j {
					return 0, fail("CONFLICTING_PROGRESS_POSITION", 409)
				}
			}
		}
		return i, nil
	}
	if p.Href != "" {
		point, e := book.HrefPosition(p.Href)
		if e != nil {
			return 0, fail("INVALID_PROGRESS_HREF", 409)
		}
		if p.InSection != nil {
			if math.IsNaN(*p.InSection) || math.IsInf(*p.InSection, 0) || *p.InSection < 0 || *p.InSection > 100 {
				return 0, fail("INVALID_SECTION_PROGRESS", 409)
			}
			if *p.InSection > 0 {
				if strings.Contains(p.Href, "#") {
					return 0, fail("AMBIGUOUS_FRAGMENT_PROGRESS", 409)
				}
				point.Offset = int(float64(book.Docs[book.Sections[point.Spine].Path].PositionSize) * (*p.InSection) / 100)
			}
		}
		i, e := book.ChapterAt(point)
		if e != nil {
			return 0, fail("INVALID_PROGRESS_POSITION", 409)
		}
		return i, nil
	}
	if p.Percent != nil {
		i, e := book.PercentChapter(*p.Percent)
		if e == nil {
			return i, nil
		}
	}
	return 0, fail("PROGRESS_SOURCE_UNKNOWN", 409)
}
func (a *Server) pause(ctx context.Context, binding store.Binding, code string) error {
	binding.State, binding.Code = "paused", code
	if e := a.DB.SaveBinding(ctx, binding); e != nil {
		return e
	}
	return fail(code, 409)
}

// eligibility adopts valid existing progress under the single-EPUB contract;
// Grimmory does not supply file-level provenance. Only the obsolete historical
// pause can recover automatically, never a reset or a previously used binding.
// History is adopted on reads, so the first save cannot overwrite it unseen.
func (a *Server) eligibility(ctx context.Context, s store.Session, b grimmory.Book, entry *cached, adoptHistory bool) (store.Binding, *int, error) {
	f, e := b.UniqueEPUB()
	if e != nil {
		return store.Binding{}, nil, fail("UNSUPPORTED_BOOK_FILES", 409)
	}
	bound, exists, e := a.DB.Binding(ctx, s.UserID, b.ID)
	if e != nil {
		return bound, nil, e
	}
	if !exists {
		bound = store.Binding{UserID: s.UserID, BookID: b.ID, FileID: f.ID, Revision: entry.Book.Revision, State: "new"}
	}
	if exists && (bound.FileID != f.ID || bound.Revision != entry.Book.Revision) {
		return bound, nil, a.pause(ctx, bound, "EPUB_VERSION_CHANGED")
	}
	legacyHistory := bound.State == "paused" && bound.Code == "HISTORICAL_PROGRESS_UNBOUND" && bound.PositionHash == "" && bound.PositionTime == 0 && bound.LastSuccess == 0
	if legacyHistory {
		bound.State, bound.Code = "new", ""
	}
	if bound.State == "paused" {
		return bound, nil, fail(bound.Code, 409)
	}
	if b.Conflicting() {
		return bound, nil, a.pause(ctx, bound, "PROGRESS_SOURCE_UNKNOWN")
	}
	if bound.State == "pending" {
		return bound, nil, a.pause(ctx, bound, "INCOMPLETE_BINDING_WRITE")
	}
	if bound.State == "new" && b.NoHistory() && !legacyHistory {
		if e = a.DB.SaveBinding(ctx, bound); e != nil {
			return bound, nil, e
		}
		return bound, nil, nil
	}
	if b.EPUB == nil {
		if bound.State == "new" {
			return bound, nil, a.pause(ctx, bound, "PROGRESS_SOURCE_UNKNOWN")
		}
		return bound, nil, a.pause(ctx, bound, "UPSTREAM_PROGRESS_RESET")
	}
	index, e := progressChapter(b.EPUB, entry.Book)
	if e != nil {
		code, _ := publicError(e)
		return bound, nil, a.pause(ctx, bound, code)
	}
	if bound.State == "new" {
		if b.LastRead == nil || b.LastRead.UnixMilli() <= 0 {
			return bound, nil, a.pause(ctx, bound, "PROGRESS_TIME_UNKNOWN")
		}
		if !adoptHistory {
			return bound, nil, fail("UPSTREAM_PROGRESS_AVAILABLE", 409)
		}
		bound.State, bound.Code = "bound", ""
	}
	hash := positionHash(b.EPUB)
	if hash != bound.PositionHash {
		if b.LastRead == nil || b.LastRead.UnixMilli() <= 0 {
			return bound, nil, a.pause(ctx, bound, "PROGRESS_TIME_UNKNOWN")
		}
		if b.LastRead.UnixMilli() <= bound.PositionTime {
			return bound, nil, a.pause(ctx, bound, "PROGRESS_TIME_AMBIGUOUS")
		}
		bound.PositionHash, bound.PositionTime = hash, b.LastRead.UnixMilli()
		if e = a.DB.SaveBinding(ctx, bound); e != nil {
			return bound, nil, e
		}
	}
	if bound.PositionTime <= 0 {
		return bound, nil, a.pause(ctx, bound, "PROGRESS_TIME_UNKNOWN")
	}
	if bound.Code != "" {
		bound.Code = ""
		if e = a.DB.SaveBinding(ctx, bound); e != nil {
			return bound, nil, e
		}
	}
	return bound, &index, nil
}

func (a *Server) saveProgress(ctx context.Context, s store.Session, raw string, index int) (result error) {
	id, file, revision, e := a.parseSync(raw)
	if e != nil {
		return e
	}
	defer func() {
		if result != nil {
			code, _ := publicError(result)
			a.DB.RecordFailure(ctx, s.UserID, id, code)
		}
	}()
	lock := &a.bookLocks[uint64(id)%64]
	lock.Lock()
	defer lock.Unlock()
	b, e := a.book(ctx, s, id)
	if e != nil {
		return e
	}
	f, e := b.UniqueEPUB()
	if e != nil {
		a.DB.Invalidate(ctx, id, "BOOK_FILES_CHANGED")
		return fail("UNSUPPORTED_BOOK_FILES", 409)
	}
	if f.ID != file {
		return fail("EPUB_VERSION_MISMATCH", 409)
	}
	entry, e := a.content(ctx, s, b)
	if e != nil {
		return e
	}
	defer a.cache.release(entry)
	if entry.Book.Revision != revision {
		return fail("EPUB_VERSION_MISMATCH", 409)
	}
	if index < 0 || index >= len(entry.Book.Chapters) {
		return fail("INVALID_CHAPTER_INDEX", 400)
	}
	// Fetch permission and provenance again immediately before writing; never accept
	// a login-time file list or a cache hit as authorization.
	b, e = a.book(ctx, s, id)
	if e != nil {
		return e
	}
	f, e = b.UniqueEPUB()
	if e != nil || f.ID != file {
		a.DB.Invalidate(ctx, id, "BOOK_FILES_CHANGED")
		return fail("BOOK_FILES_CHANGED", 409)
	}
	bound, _, e := a.eligibility(ctx, s, b, entry, false)
	if e != nil {
		return e
	}
	chapter := entry.Book.Chapters[index]
	write := grimmory.WriteProgress{BookID: id, File: grimmory.FileProgress{FileID: file, Href: chapter.Href, Percent: chapter.Percent}}
	if b.EPUB != nil && b.EPUB.CFI != "" {
		if i, e := entry.Book.CFIChapter(b.EPUB.CFI); e == nil && i == index && b.EPUB.Percent != nil {
			cfi := b.EPUB.CFI
			write.File.CFI = &cfi
			write.File.Percent = *b.EPUB.Percent
			if b.EPUB.TTS != "" {
				tts := b.EPUB.TTS
				write.File.TTS = &tts
			}
		}
	}
	if math.IsNaN(write.File.Percent) || math.IsInf(write.File.Percent, 0) || write.File.Percent < 0 || write.File.Percent > 100 {
		return fail("INVALID_PROGRESS_PERCENT", 409)
	}
	previousState := bound.State
	bound.State = "pending"
	bound.Code = "WRITE_READBACK_PENDING"
	if e = a.DB.SaveBinding(ctx, bound); e != nil {
		return e
	}
	e = a.upstreamJSON(ctx, s, "POST", "/api/v1/books/progress", write, nil)
	if e != nil { // A failed/ambiguous write is not retried as a fresh enrollment.
		bound.State, bound.Code = "paused", "UPSTREAM_WRITE_UNCONFIRMED"
		a.DB.SaveBinding(ctx, bound)
		return e
	}
	readback, e := a.book(ctx, s, id)
	if e != nil {
		bound.State, bound.Code = "paused", "WRITE_READBACK_FAILED"
		a.DB.SaveBinding(ctx, bound)
		return e
	}
	rf, e := readback.UniqueEPUB()
	if e != nil || rf.ID != file || readback.Conflicting() {
		return a.pause(ctx, bound, "WRITE_READBACK_MISMATCH")
	}
	resolved, e := progressChapter(readback.EPUB, entry.Book)
	if e != nil || resolved != index || readback.LastRead == nil || readback.LastRead.UnixMilli() <= 0 {
		return a.pause(ctx, bound, "WRITE_READBACK_MISMATCH")
	}
	// Initial binding must read back the fields just written. A racing Web save is
	// left untouched and pauses enrollment instead of being overwritten again.
	if previousState == "new" && (readback.EPUB.Href != write.File.Href || (write.File.CFI == nil && readback.EPUB.CFI != "") || readback.EPUB.Percent == nil || math.Abs(*readback.EPUB.Percent-write.File.Percent) > 0.051 || readback.EPUB.InSection != nil || readback.EPUB.TTS != "") {
		return a.pause(ctx, bound, "INITIAL_BINDING_RACE")
	}
	bound.State, bound.Code = "bound", ""
	bound.PositionHash = positionHash(readback.EPUB)
	bound.PositionTime = readback.LastRead.UnixMilli()
	bound.LastSuccess = store.Now()
	return a.DB.SaveBinding(ctx, bound)
}
