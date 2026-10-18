package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"grimmoryrodelbridge/internal/epub"
	"grimmoryrodelbridge/internal/grimmory"
	"grimmoryrodelbridge/internal/store"
)

type cached struct {
	Book                  *epub.Book
	Source, Derived, ETag string
	Bytes                 int64
	Memory                int64
	Used                  time.Time
	Refs                  int
}
type cache struct {
	mu      sync.Mutex
	entries map[string]*cached
	dir     string
	max     int64
	slots   chan struct{}
}

func newCache(dir string, max int64, concurrency int) (*cache, error) {
	dir = filepath.Join(dir, "cache")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	return &cache{entries: map[string]*cached{}, dir: dir, max: max, slots: make(chan struct{}, concurrency)}, nil
}
func (c *cache) release(entry *cached) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry.Refs--
	entry.Used = time.Now()
}
func (c *cache) makeRoom(required, memory int64) bool {
	var used, retained int64
	for _, e := range c.entries {
		used += e.Bytes
		retained += e.Memory
	}
	for used+required > c.max || retained+memory > 128<<20 || len(c.entries) >= 16 {
		var key string
		var oldest *cached
		for k, e := range c.entries {
			if e.Refs == 0 && (oldest == nil || e.Used.Before(oldest.Used)) {
				key, oldest = k, e
			}
		}
		if oldest == nil {
			return false
		}
		os.Remove(oldest.Source)
		os.Remove(oldest.Derived)
		delete(c.entries, key)
		used -= oldest.Bytes
		retained -= oldest.Memory
	}
	return true
}

// The v3.5.0 content endpoint has no strong ETag. Last-Modified/size alone cannot
// prove unchanged content, so hash a streamed GET on each sync operation. Parsing
// and derivation are reused for identical bytes. No direct NAS access is needed.
func (a *Server) content(ctx context.Context, s store.Session, b grimmory.Book) (*cached, error) {
	f, e := b.UniqueEPUB()
	if e != nil {
		a.DB.Invalidate(ctx, b.ID, "BOOK_FILES_CHANGED")
		return nil, fail("UNSUPPORTED_BOOK_FILES", 409)
	}
	select {
	case a.cache.slots <- struct{}{}:
		defer func() { <-a.cache.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	resp, e := a.upstream(ctx, s, "GET", fmt.Sprintf("/api/v1/books/%d/content", b.ID), nil)
	if e != nil {
		return nil, e
	}
	if resp.StatusCode != http.StatusOK {
		return nil, grimmory.Decode(resp, nil)
	}
	defer resp.Body.Close()
	if resp.ContentLength > a.Config.MaxBookBytes {
		return nil, fail("EPUB_TOO_LARGE", 413)
	}
	source, e := os.CreateTemp(a.cache.dir, "source-*")
	if e != nil {
		return nil, e
	}
	temp := source.Name()
	defer os.Remove(temp)
	hash := sha256.New()
	n, e := io.Copy(io.MultiWriter(source, hash), io.LimitReader(resp.Body, a.Config.MaxBookBytes+1))
	closeErr := source.Close()
	if e != nil {
		return nil, e
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if n > a.Config.MaxBookBytes {
		return nil, fail("EPUB_TOO_LARGE", 413)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != n {
		return nil, fail("INCOMPLETE_EPUB_TRANSFER", 503)
	}
	sourceHash := hex.EncodeToString(hash.Sum(nil))
	revision := epub.Revision(sourceHash)
	old, exists, e := a.DB.Revision(ctx, b.ID)
	if e != nil {
		return nil, e
	}
	if exists && (old.FileID != f.ID || old.Revision != revision) {
		if e = a.DB.Invalidate(ctx, b.ID, "EPUB_VERSION_CHANGED"); e != nil {
			return nil, e
		}
	}
	if e = a.DB.SaveRevision(ctx, store.Revision{BookID: b.ID, FileID: f.ID, Revision: revision, SourceHash: sourceHash}); e != nil {
		return nil, e
	}
	// Include book/file identity: identical files in different books need distinct metadata.
	key := fmt.Sprintf("%d-%d-%s", b.ID, f.ID, revision)
	a.cache.mu.Lock()
	defer a.cache.mu.Unlock()
	if entry := a.cache.entries[key]; entry != nil {
		entry.Refs++
		entry.Used = time.Now()
		return entry, nil
	}
	parsed, e := epub.Open(temp)
	if e != nil {
		return nil, fail(e.Error(), 422)
	}
	parsed.SourceHash, parsed.Revision = sourceHash, revision
	memory := parsed.CacheWeight()
	if memory > 128<<20 {
		return nil, fail("EPUB_PARSED_MEMORY_LIMIT", 413)
	}
	dest, e := os.CreateTemp(a.cache.dir, "derived-*")
	if e != nil {
		return nil, e
	}
	derived := dest.Name()
	dest.Close()
	keep := false
	defer func() {
		if !keep {
			os.Remove(derived)
		}
	}()
	if e = parsed.Derive(temp, derived, a.syncURL(b.ID, f.ID, revision), a.Config.PublicURL); e != nil {
		return nil, fail("EPUB_DERIVATION_FAILED", 422)
	}
	info, e := os.Stat(derived)
	if e != nil {
		return nil, e
	}
	if info.Size() > a.Config.MaxBookBytes+(4<<20) {
		return nil, fail("DERIVED_EPUB_TOO_LARGE", 413)
	}
	if !a.cache.makeRoom(n+info.Size(), memory) {
		return nil, fail("CACHE_BUSY", 503)
	}
	permanent := filepath.Join(a.cache.dir, key+".source.epub")
	if e = os.Rename(temp, permanent); e != nil {
		return nil, e
	}
	entry := &cached{Book: parsed, Source: permanent, Derived: derived, Bytes: n + info.Size(), Memory: memory, Used: time.Now(), Refs: 1}
	file, e := os.Open(derived)
	if e != nil {
		os.Remove(permanent)
		return nil, e
	}
	dh := sha256.New()
	_, e = io.Copy(dh, file)
	file.Close()
	if e != nil {
		os.Remove(permanent)
		return nil, e
	}
	entry.ETag = "\"" + hex.EncodeToString(dh.Sum(nil)) + "\""
	a.cache.entries[key] = entry
	keep = true
	return entry, nil
}

func (c *cache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		os.Remove(e.Source)
		os.Remove(e.Derived)
	}
}

// Cache files are disposable and intentionally rebuilt after a restart. Never
// remove anything outside the dedicated cache directory or follow symlinks.
func clearCacheDir(dir string) error {
	entries, e := os.ReadDir(dir)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			name := entry.Name()
			if len(name) > 0 {
				if e = os.Remove(filepath.Join(dir, name)); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
