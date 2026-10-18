package adapter

import (
	"context"
	"embed"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"grimmoryrodelbridge/internal/config"
	"grimmoryrodelbridge/internal/grimmory"
	"grimmoryrodelbridge/internal/store"
)

//go:embed web/*
var web embed.FS

type attempt struct {
	start time.Time
	count int
}
type Server struct {
	Config               config.Config
	DB                   *store.Store
	Upstream             *grimmory.Client
	Log                  *slog.Logger
	instance             string
	cache                *cache
	authLocks, bookLocks [64]sync.Mutex
	rateMu               sync.Mutex
	attempts             map[string]attempt
}

func New(c config.Config, log *slog.Logger) (*Server, error) {
	db, e := store.Open(c.DataDir, c.Key)
	if e != nil {
		return nil, e
	}
	instance, e := db.Instance(context.Background())
	if e != nil {
		db.Close()
		return nil, e
	}
	if e = db.CheckConfig(context.Background(), c.Upstream, c.PublicURL); e != nil {
		db.Close()
		return nil, e
	}
	cache, e := newCache(c.DataDir, c.CacheBytes, c.BuildConcurrency)
	if e != nil {
		db.Close()
		return nil, e
	}
	if e = clearCacheDir(cache.dir); e != nil {
		db.Close()
		return nil, e
	}
	return &Server{Config: c, DB: db, Upstream: grimmory.New(c.Upstream), Log: log, instance: instance, cache: cache, attempts: map[string]attempt{}}, nil
}
func (a *Server) Close() error { a.cache.cleanup(); return a.DB.Close() }
func (a *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if _, e := a.DB.Instance(r.Context()); e != nil {
			http.Error(w, "storage unavailable", 503)
			return
		}
		w.Write([]byte("ready\n"))
	})
	mux.HandleFunc("GET /{$}", a.webFile)
	mux.HandleFunc("GET /app.js", a.webFile)
	mux.HandleFunc("GET /style.css", a.webFile)
	mux.HandleFunc("GET /api/status", a.status)
	mux.HandleFunc("/reader3/", a.reader)
	mux.HandleFunc("GET /opds", a.opds)
	mux.HandleFunc("GET /opds/", a.opds)
	mux.HandleFunc("GET /books/{id}/epub", a.download)
	mux.HandleFunc("GET /books/{id}/cover", a.cover)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		// No proxy headers are trusted; PUBLIC_BASE_URL is the sole source of links.
		if r.Method != "GET" && r.Method != "HEAD" {
			if origin := r.Header.Get("Origin"); origin != "" && origin != a.Config.PublicURL {
				http.Error(w, "origin denied", 403)
				return
			}
		}
		defer func() {
			if recover() != nil {
				a.Log.Error("request panic", "operation", operation(r))
				http.Error(w, "internal error", 500)
			}
		}()
		mux.ServeHTTP(w, r)
	})
}
func operation(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/reader3/") {
		return strings.TrimPrefix(r.URL.Path, "/reader3/")
	}
	if strings.HasPrefix(r.URL.Path, "/books/") {
		return "book-resource"
	}
	return "http"
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func (a *Server) error(w http.ResponseWriter, r *http.Request, err error, reader bool) {
	code, status := publicError(err)
	a.Log.Warn("request rejected", "operation", operation(r), "code", code, "status", status)
	if reader {
		data := any(nil)
		if status == 401 {
			data = "NEED_LOGIN"
		}
		writeJSON(w, status, map[string]any{"isSuccess": false, "errorMsg": code, "data": data})
	} else {
		writeJSON(w, status, map[string]string{"error": code})
	}
}
func success(w http.ResponseWriter, data any) {
	writeJSON(w, 200, map[string]any{"isSuccess": true, "errorMsg": "", "data": data})
}
func (a *Server) loginAllowed(r *http.Request, username string) bool {
	a.rateMu.Lock()
	defer a.rateMu.Unlock()
	now := time.Now()
	for k, v := range a.attempts {
		if now.Sub(v.start) > 15*time.Minute {
			delete(a.attempts, k)
		}
	}
	if len(a.attempts) > 4096 {
		return false
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	allowed := true
	for _, key := range []string{"ip:" + ip, "user:" + username} {
		v := a.attempts[key]
		if v.start.IsZero() {
			v.start = now
		}
		v.count++
		a.attempts[key] = v
		if v.count > 20 {
			allowed = false
		}
	}
	return allowed
}
func (a *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		a.error(w, r, fail("METHOD_NOT_ALLOWED", 405), true)
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		IsLogin  *bool  `json:"isLogin"`
	}
	if e := decodeBody(w, r, &in); e != nil {
		a.error(w, r, e, true)
		return
	}
	if in.Username == "" || len(in.Username) > 256 || in.Password == "" || len(in.Password) > 4096 || in.IsLogin != nil && !*in.IsLogin {
		a.error(w, r, fail("INVALID_LOGIN_REQUEST", 400), true)
		return
	}
	if !a.loginAllowed(r, in.Username) {
		a.error(w, r, fail("LOGIN_RATE_LIMITED", 429), true)
		return
	}
	t, e := a.Upstream.Login(r.Context(), in.Username, in.Password)
	in.Password = ""
	if e != nil {
		a.error(w, r, e, true)
		return
	}
	var user grimmory.User
	resp, e := a.Upstream.Raw(r.Context(), t.Access, "GET", "/api/v1/users/me", nil)
	if e == nil {
		e = grimmory.Decode(resp, &user)
	}
	if e != nil || user.ID <= 0 {
		if e == nil {
			e = fail("INVALID_USER_RESPONSE", 502)
		}
		a.error(w, r, e, true)
		return
	}
	token := store.Random()
	s := store.Session{ID: store.Random(), TokenHash: store.Hash(token), UserID: user.ID, Username: user.Username, Access: t.Access, Refresh: t.Refresh, Expires: time.Now().Add(time.Duration(t.Expires) * time.Second).Unix(), Created: store.Now()}
	if e = a.DB.SaveSession(r.Context(), s); e != nil {
		a.error(w, r, e, true)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "adapter_session", Value: token, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(a.Config.PublicURL, "https:"), SameSite: http.SameSiteStrictMode})
	success(w, map[string]any{"username": user.Username, "accessToken": token})
}
func decodeBody(w http.ResponseWriter, r *http.Request, out any) error {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		return fail("JSON_REQUIRED", 415)
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	d := json.NewDecoder(r.Body)
	if e := d.Decode(out); e != nil {
		return fail("INVALID_JSON", 400)
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fail("INVALID_JSON", 400)
	}
	return nil
}

type readerRequest struct {
	URL   string `json:"url"`
	Index *int   `json:"index"`
	Book  *struct {
		URL string `json:"bookUrl"`
	} `json:"book"`
	SearchBook *struct {
		URL string `json:"bookUrl"`
	} `json:"searchBook"`
}

func (a *Server) reader(w http.ResponseWriter, r *http.Request) {
	endpoint := strings.TrimPrefix(r.URL.Path, "/reader3/")
	if endpoint == "login" {
		a.login(w, r)
		return
	}
	methods := map[string]string{"getUserInfo": "GET", "logout": "POST", "getBookshelf": "GET", "getShelfBook": "GET", "getBookInfo": "GET POST", "getChapterList": "GET POST", "saveBookProgress": "POST", "getBookContent": "GET POST"}
	if !containsMethod(methods[endpoint], r.Method) {
		a.error(w, r, fail("UNSUPPORTED_READER_OPERATION", 405), true)
		return
	}
	s, e := a.authenticate(r)
	if e != nil {
		a.error(w, r, e, true)
		return
	}
	if endpoint == "logout" {
		lock := &a.authLocks[lockIndex(s.ID)]
		lock.Lock()
		e = a.DB.Revoke(r.Context(), s.ID)
		lock.Unlock()
		if e != nil {
			a.error(w, r, e, true)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "adapter_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(a.Config.PublicURL, "https:"), SameSite: http.SameSiteStrictMode})
		success(w, "")
		return
	}
	if endpoint == "getUserInfo" {
		if _, e = a.user(r.Context(), s); e != nil {
			a.error(w, r, e, true)
			return
		}
		success(w, map[string]any{"userInfo": map[string]string{"username": s.Username}, "secure": true, "secureKey": false})
		return
	}
	if endpoint == "getBookContent" {
		a.error(w, r, fail("USE_OPDS_EPUB_IMPORT", 409), true)
		return
	}
	if endpoint == "getBookshelf" {
		books, e := a.readerShelf(r.Context(), s)
		if e != nil {
			a.error(w, r, e, true)
			return
		}
		success(w, books)
		return
	}
	var in readerRequest
	if r.Method == "POST" {
		if e = decodeBody(w, r, &in); e != nil {
			a.error(w, r, e, true)
			return
		}
	} else {
		in.URL = r.URL.Query().Get("url")
	}
	if in.URL == "" && in.Book != nil {
		in.URL = in.Book.URL
	}
	if in.URL == "" && in.SearchBook != nil {
		in.URL = in.SearchBook.URL
	}
	if endpoint == "saveBookProgress" {
		if in.Index == nil {
			a.error(w, r, fail("INVALID_CHAPTER_INDEX", 400), true)
			return
		}
		e = a.saveProgress(r.Context(), s, in.URL, *in.Index)
		if e != nil {
			a.error(w, r, e, true)
			return
		}
		success(w, "")
		return
	}
	id, file, rev, e := a.parseSync(in.URL)
	if e != nil {
		a.error(w, r, e, true)
		return
	}
	lock := &a.bookLocks[uint64(id)%64]
	lock.Lock()
	defer lock.Unlock()
	b, e := a.book(r.Context(), s, id)
	if e != nil {
		a.error(w, r, e, true)
		return
	}
	entry, e := a.content(r.Context(), s, b)
	if e != nil {
		a.error(w, r, e, true)
		return
	}
	defer a.cache.release(entry)
	if b.Primary.ID != file || entry.Book.Revision != rev {
		a.error(w, r, fail("EPUB_VERSION_MISMATCH", 409), true)
		return
	}
	bound, index, e := a.eligibility(r.Context(), s, b, entry)
	if e != nil {
		a.error(w, r, e, true)
		return
	}
	if endpoint == "getChapterList" {
		chapters := make([]map[string]any, 0, len(entry.Book.Chapters))
		for _, c := range entry.Book.Chapters {
			chapters = append(chapters, map[string]any{"url": c.Href, "title": c.Title, "index": c.Index, "bookUrl": in.URL, "isVolume": false, "isVip": false, "isPay": false})
		}
		success(w, chapters)
	} else {
		success(w, a.readerBook(b, entry, bound, index))
	}
}
func containsMethod(methods, method string) bool {
	for _, m := range strings.Fields(methods) {
		if m == method {
			return true
		}
	}
	return false
}
func (a *Server) readerBook(b grimmory.Book, e *cached, bound store.Binding, index *int) map[string]any {
	raw := a.syncURL(b.ID, b.Primary.ID, e.Book.Revision)
	v := map[string]any{"bookUrl": raw, "tocUrl": raw, "origin": "loc_book", "originName": fmt.Sprintf("grimmory-%d.epub", b.ID), "name": b.Name(), "author": strings.Join(b.Metadata.Authors, ", "), "intro": b.Metadata.Description, "coverUrl": fmt.Sprintf("%s/books/%d/cover", a.Config.PublicURL, b.ID), "kind": "EPUB", "type": 0, "totalChapterNum": len(e.Book.Chapters), "latestChapterTitle": e.Book.Chapters[len(e.Book.Chapters)-1].Title, "canUpdate": false}
	if index != nil {
		v["durChapterIndex"] = *index
		v["durChapterTitle"] = e.Book.Chapters[*index].Title
		v["durChapterTime"] = bound.PositionTime
		v["durChapterPos"] = 0
	}
	return v
}
func (a *Server) readerShelf(ctx context.Context, s store.Session) ([]map[string]any, error) {
	out := []map[string]any{}
	for number := 0; number < 10000; number++ {
		p, e := a.page(ctx, s, number)
		if e != nil {
			return nil, e
		}
		for _, listed := range p.Content {
			if _, ok, e := a.DB.Revision(ctx, listed.ID); e != nil {
				return nil, e
			} else if !ok {
				continue
			}
			lock := &a.bookLocks[uint64(listed.ID)%64]
			lock.Lock()
			model, e := a.shelfItem(ctx, s, listed.ID)
			lock.Unlock()
			if e != nil {
				_, status := publicError(e)
				if status >= 500 {
					return nil, e
				}
				continue
			}
			out = append(out, model)
		}
		if number+1 >= p.Page.TotalPages {
			return out, nil
		}
	}
	return nil, fail("BOOK_PAGE_LIMIT", 503)
}
func (a *Server) shelfItem(ctx context.Context, s store.Session, id int64) (result map[string]any, resultErr error) {
	defer func() {
		if resultErr != nil {
			code, _ := publicError(resultErr)
			a.DB.RecordFailure(ctx, s.UserID, id, code)
		}
	}()
	b, e := a.book(ctx, s, id)
	if e != nil {
		return nil, e
	}
	entry, e := a.content(ctx, s, b)
	if e != nil {
		return nil, e
	}
	defer a.cache.release(entry)
	bound, index, e := a.eligibility(ctx, s, b, entry)
	if e != nil {
		return nil, e
	}
	return a.readerBook(b, entry, bound, index), nil
}

func parseID(r *http.Request) (int64, error) {
	v, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e != nil || v <= 0 {
		return 0, fail("INVALID_BOOK_ID", 400)
	}
	return v, nil
}
func (a *Server) download(w http.ResponseWriter, r *http.Request) {
	s, e := a.authenticate(r)
	if e != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="GrimmoryRodelBridge"`)
		a.error(w, r, e, false)
		return
	}
	id, e := parseID(r)
	if e != nil {
		a.error(w, r, e, false)
		return
	}
	lock := &a.bookLocks[uint64(id)%64]
	lock.Lock()
	defer lock.Unlock()
	u, e := a.user(r.Context(), s)
	if e != nil {
		a.error(w, r, e, false)
		return
	}
	if !u.Permissions.Admin && !u.Permissions.Download {
		a.error(w, r, fail("DOWNLOAD_PERMISSION_REQUIRED", 403), false)
		return
	}
	b, e := a.book(r.Context(), s, id)
	if e != nil {
		a.error(w, r, e, false)
		return
	}
	entry, e := a.content(r.Context(), s, b)
	if e != nil {
		a.error(w, r, e, false)
		return
	}
	defer a.cache.release(entry)
	// Check again after the potentially long fetch, including cache hits/Range/304.
	u, e = a.user(r.Context(), s)
	if e != nil {
		a.error(w, r, e, false)
		return
	}
	if !u.Permissions.Admin && !u.Permissions.Download {
		a.error(w, r, fail("DOWNLOAD_PERMISSION_REQUIRED", 403), false)
		return
	}
	fresh, e := a.book(r.Context(), s, id)
	if e != nil {
		a.error(w, r, e, false)
		return
	}
	f, e := fresh.UniqueEPUB()
	if e != nil || f.ID != b.Primary.ID {
		a.error(w, r, fail("BOOK_FILES_CHANGED", 409), false)
		return
	}
	// Preparing a book records its synchronization state without granting a binding.
	_, _, e = a.eligibility(r.Context(), s, fresh, entry)
	if e != nil {
		code, status := publicError(e)
		if status >= 500 {
			a.error(w, r, e, false)
			return
		}
		w.Header().Set("X-Adapter-Sync-Status", code)
	}
	file, e := os.Open(entry.Derived)
	if e != nil {
		a.error(w, r, e, false)
		return
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		a.error(w, r, e, false)
		return
	}
	w.Header().Set("Content-Type", "application/epub+zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="grimmory-%d.epub"`, id))
	w.Header().Set("ETag", entry.ETag)
	w.Header().Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, "book.epub", info.ModTime(), file)
}
func (a *Server) cover(w http.ResponseWriter, r *http.Request) {
	s, e := a.authenticate(r)
	if e != nil {
		a.error(w, r, e, false)
		return
	}
	id, e := parseID(r)
	if e != nil {
		a.error(w, r, e, false)
		return
	}
	if _, e = a.book(r.Context(), s, id); e != nil {
		a.error(w, r, e, false)
		return
	}
	resp, e := a.upstream(r.Context(), s, "GET", fmt.Sprintf("/api/v1/media/book/%d/cover", id), nil)
	if e != nil {
		a.error(w, r, e, false)
		return
	}
	if resp.StatusCode != 200 {
		e = grimmory.Decode(resp, nil)
		a.error(w, r, e, false)
		return
	}
	defer resp.Body.Close()
	kind := strings.Split(resp.Header.Get("Content-Type"), ";")[0]
	switch kind {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
	default:
		a.error(w, r, fail("UNSUPPORTED_COVER_MEDIA", 415), false)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "private, no-cache")
	io.Copy(w, io.LimitReader(resp.Body, 16<<20))
}

type atomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
	Type string `xml:"type,attr,omitempty"`
}
type atomEntry struct {
	ID      string `xml:"id"`
	Title   string `xml:"title"`
	Updated string `xml:"updated"`
	Author  struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Summary string     `xml:"summary"`
	Links   []atomLink `xml:"link"`
}
type atomFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	ID      string      `xml:"id"`
	Title   string      `xml:"title"`
	Updated string      `xml:"updated"`
	Links   []atomLink  `xml:"link"`
	Entries []atomEntry `xml:"entry"`
}

const atomType = "application/atom+xml;profile=opds-catalog;kind=acquisition"

func (a *Server) opds(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/opds" && r.URL.Path != "/opds/" {
		http.NotFound(w, r)
		return
	}
	s, e := a.authenticate(r)
	if e != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="GrimmoryRodelBridge"`)
		a.error(w, r, e, false)
		return
	}
	number := 0
	if v := r.URL.Query().Get("page"); v != "" {
		number, e = strconv.Atoi(v)
		if e != nil || number < 0 || number >= 10000 {
			a.error(w, r, fail("INVALID_PAGE", 400), false)
			return
		}
	}
	page, e := a.page(r.Context(), s, number)
	if e != nil {
		a.error(w, r, e, false)
		return
	}
	// Keep the existing feed identity so a branding change does not create a new catalog.
	feed := atomFeed{ID: "urn:grimmory-adapter:" + a.instance, Title: "GrimmoryRodelBridge", Updated: time.Now().UTC().Format(time.RFC3339), Links: []atomLink{{Rel: "self", Href: fmt.Sprintf("%s/opds?page=%d", a.Config.PublicURL, number), Type: atomType}, {Rel: "start", Href: a.Config.PublicURL + "/opds", Type: atomType}}}
	if number+1 < page.Page.TotalPages {
		feed.Links = append(feed.Links, atomLink{Rel: "next", Href: fmt.Sprintf("%s/opds?page=%d", a.Config.PublicURL, number+1), Type: atomType})
	}
	for _, listed := range page.Content { // Details perform Grimmory's content-restriction access check.
		b, e := a.book(r.Context(), s, listed.ID)
		if e != nil {
			_, status := publicError(e)
			if status == 403 || status == 404 {
				continue
			}
			a.error(w, r, e, false)
			return
		}
		if _, e = b.UniqueEPUB(); e != nil {
			continue
		}
		entry := atomEntry{ID: fmt.Sprintf("urn:grimmory:%s:book:%d", a.instance, b.ID), Title: b.Name(), Updated: feed.Updated, Summary: b.Metadata.Description}
		entry.Author.Name = strings.Join(b.Metadata.Authors, ", ")
		entry.Links = []atomLink{{Rel: "http://opds-spec.org/acquisition", Href: fmt.Sprintf("%s/books/%d/epub", a.Config.PublicURL, b.ID), Type: "application/epub+zip"}, {Rel: "http://opds-spec.org/image", Href: fmt.Sprintf("%s/books/%d/cover", a.Config.PublicURL, b.ID), Type: "image/jpeg"}}
		if b.Added != nil {
			entry.Updated = b.Added.UTC().Format(time.RFC3339)
		}
		feed.Entries = append(feed.Entries, entry)
	}
	w.Header().Set("Content-Type", atomType+";charset=utf-8")
	w.Write([]byte(xml.Header))
	xml.NewEncoder(w).Encode(feed)
}
func (a *Server) webFile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	data, e := web.ReadFile("web/" + name)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	kind := map[string]string{"index.html": "text/html; charset=utf-8", "app.js": "text/javascript; charset=utf-8", "style.css": "text/css; charset=utf-8"}[name]
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'")
	w.Write(data)
}
func (a *Server) status(w http.ResponseWriter, r *http.Request) {
	s, e := a.authenticate(r)
	if e != nil {
		a.error(w, r, e, false)
		return
	}
	u, e := a.user(r.Context(), s)
	if e != nil {
		a.error(w, r, e, false)
		return
	}
	bindings, e := a.DB.Status(r.Context(), s.UserID)
	if e != nil {
		a.error(w, r, e, false)
		return
	}
	writeJSON(w, 200, map[string]any{"username": u.Username, "canDownload": u.Permissions.Admin || u.Permissions.Download, "readerURL": a.Config.PublicURL, "opdsURL": a.Config.PublicURL + "/opds", "books": bindings, "contract": "唯一 EPUB、同一内容版本，仅支持 Grimmory Web 和 Adapter 的正常进度更新；来源不明或版本变化时暂停。"})
}

// Exported only for a CLI readiness probe; credentials are never part of its URL.
func HealthURL(listen string) string {
	host, port, e := net.SplitHostPort(listen)
	if e != nil {
		return ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), Path: "/readyz"}).String()
}
