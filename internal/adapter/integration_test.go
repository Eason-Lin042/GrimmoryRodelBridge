package adapter

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"grimmoryrodelbridge/internal/config"
	"grimmoryrodelbridge/internal/grimmory"
	"grimmoryrodelbridge/internal/store"
	"grimmoryrodelbridge/internal/testfixture"
)

type mockUser struct {
	name                  string
	download, admin, opds bool
	progress              *grimmory.Progress
	pdf                   json.RawMessage
	last                  *time.Time
	blocked               bool
}
type mockGrimmory struct {
	mu              sync.Mutex
	users           map[int64]*mockUser
	access, refresh map[string]int64
	generation      int
	writes          []grimmory.WriteProgress
	refreshes       int
	content         []byte
	extra           bool
	failRead        bool
	race            bool
	clock           time.Time
}

func newMock() *mockGrimmory {
	return &mockGrimmory{users: map[int64]*mockUser{1: {name: "alice", download: true}, 2: {name: "bob", download: true}, 3: {name: "admin", admin: true}}, access: map[string]int64{}, refresh: map[string]int64{}, content: testfixture.EPUB("R1"), clock: time.Now().UTC()}
}
func (m *mockGrimmory) tokens(user int64) grimmory.Tokens {
	m.generation++
	a, r := fmt.Sprintf("access-%d-%d", user, m.generation), fmt.Sprintf("refresh-%d-%d", user, m.generation)
	m.access[a], m.refresh[r] = user, user
	return grimmory.Tokens{Access: a, Refresh: r, Expires: 3600}
}
func (m *mockGrimmory) book(id int64) grimmory.Book {
	u := m.users[id]
	b := grimmory.Book{ID: 42, Title: "Synthetic", Primary: &grimmory.File{ID: 901, BookID: 42, Type: "EPUB", IsBook: true}, Metadata: grimmory.Metadata{Title: "Synthetic", Authors: []string{"Author"}}, EPUB: u.progress, PDF: u.pdf, LastRead: u.last}
	if m.extra {
		b.Alternatives = []grimmory.File{{ID: 902, BookID: 42, Type: "PDF", IsBook: true}}
	}
	return b
}
func (m *mockGrimmory) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	respond := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
	if r.URL.Path == "/api/v1/auth/login" {
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in)
		if in["password"] != "test-password" {
			w.WriteHeader(401)
			return
		}
		for id, u := range m.users {
			if in["username"] == u.name {
				respond(m.tokens(id))
				return
			}
		}
		w.WriteHeader(401)
		return
	}
	if r.URL.Path == "/api/v1/auth/refresh" {
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in)
		id := m.refresh[in["refreshToken"]]
		if id == 0 {
			w.WriteHeader(401)
			return
		}
		delete(m.refresh, in["refreshToken"])
		m.refreshes++
		respond(m.tokens(id))
		return
	}
	id := m.access[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	u := m.users[id]
	if u == nil {
		w.WriteHeader(401)
		return
	}
	switch r.URL.Path {
	case "/api/v1/users/me":
		respond(map[string]any{"id": id, "username": u.name, "permissions": map[string]bool{"admin": u.admin, "canDownload": u.download, "canAccessOpds": u.opds}})
	case "/api/v1/books/page":
		if !r.URL.Query().Has("sort") || r.URL.Query().Get("sort") != "" {
			w.WriteHeader(400) // v3.5.0 reserves 'id' as its implicit tiebreaker.
			return
		}
		p := grimmory.Page{}
		p.Page.TotalPages = 1
		if !u.blocked {
			p.Content = []grimmory.Book{m.book(id)}
		}
		respond(p)
	case "/api/v1/books/42":
		if u.blocked {
			w.WriteHeader(403)
			return
		}
		if m.failRead {
			w.WriteHeader(503)
			return
		}
		respond(m.book(id))
	case "/api/v1/books/42/content":
		if u.blocked {
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/epub+zip")
		w.Header().Set("Last-Modified", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(http.TimeFormat))
		w.Write(m.content)
	case "/api/v1/books/progress":
		var in grimmory.WriteProgress
		if e := json.NewDecoder(r.Body).Decode(&in); e != nil {
			w.WriteHeader(400)
			return
		}
		m.writes = append(m.writes, in)
		m.clock = m.clock.Add(time.Second)
		stamp := m.clock
		u.last = &stamp
		cfi := ""
		if in.File.CFI != nil {
			cfi = *in.File.CFI
		}
		percent := in.File.Percent
		if m.race {
			percent += 5
		}
		u.progress = &grimmory.Progress{CFI: cfi, Href: in.File.Href, Percent: &percent, InSection: in.File.InSection}
		w.WriteHeader(204)
	default:
		w.WriteHeader(404)
	}
}

type rig struct {
	a        *Server
	upstream *httptest.Server
	m        *mockGrimmory
	logs     bytes.Buffer
	cfg      config.Config
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{m: newMock()}
	r.upstream = httptest.NewServer(r.m)
	r.cfg = config.Config{Listen: "127.0.0.1:0", Upstream: r.upstream.URL, PublicURL: "http://adapter.test", DataDir: t.TempDir(), Key: bytes.Repeat([]byte{7}, 32), CacheBytes: 32 << 20, MaxBookBytes: 8 << 20, BuildConcurrency: 2}
	var e error
	r.a, e = New(r.cfg, slog.New(slog.NewTextHandler(&r.logs, nil)))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { r.a.Close(); r.upstream.Close() })
	return r
}
func (r *rig) request(method, path, token string, body any, headers map[string]string) *httptest.ResponseRecorder {
	var input io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		input = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, "http://adapter.test"+path, input)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	r.a.Handler().ServeHTTP(rr, req)
	return rr
}
func (r *rig) login(t *testing.T, name string) string {
	t.Helper()
	resp := r.request("POST", "/reader3/login", "", map[string]any{"username": name, "password": "test-password", "isLogin": true}, nil)
	if resp.Code != 200 {
		t.Fatal(resp.Code, resp.Body.String())
	}
	var v struct {
		Data struct {
			Token string `json:"accessToken"`
		}
	}
	json.Unmarshal(resp.Body.Bytes(), &v)
	if len(v.Data.Token) != 64 {
		t.Fatal("missing token")
	}
	return v.Data.Token
}
func (r *rig) acquire(t *testing.T, token string) (string, *httptest.ResponseRecorder) {
	t.Helper()
	resp := r.request("GET", "/books/42/epub", token, nil, nil)
	if resp.Code != 200 {
		t.Fatal(resp.Code, resp.Body.String())
	}
	archive, e := zip.NewReader(bytes.NewReader(resp.Body.Bytes()), int64(resp.Body.Len()))
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range archive.File {
		if f.Name == "OPS/content.opf" {
			in, _ := f.Open()
			data, _ := io.ReadAll(in)
			in.Close()
			needle := `name="legado:book-url" content="`
			parts := strings.Split(string(data), needle)
			if len(parts) != 2 {
				t.Fatal("missing or duplicate sync metadata")
			}
			return strings.Split(parts[1], `"`)[0], resp
		}
	}
	t.Fatal("missing package")
	return "", nil
}
func (r *rig) save(token, identity string, index int) *httptest.ResponseRecorder {
	return r.request("POST", "/reader3/saveBookProgress", token, map[string]any{"url": identity, "index": index}, nil)
}
func assertCode(t *testing.T, rr *httptest.ResponseRecorder, code int) {
	t.Helper()
	if rr.Code != code {
		t.Fatalf("HTTP %d expected %d: %s", rr.Code, code, rr.Body.String())
	}
}
func number(v float64) *float64 { return &v }

func TestBilingualUIAssets(t *testing.T) {
	r := newRig(t)
	for _, path := range []string{"/app.js", "/i18n.mjs"} {
		response := r.request("GET", path, "", nil, nil)
		assertCode(t, response, 200)
		if !strings.HasPrefix(response.Header().Get("Content-Type"), "text/javascript") {
			t.Fatalf("module %s was not served as JavaScript", path)
		}
	}
	page := r.request("GET", "/", "", nil, nil)
	assertCode(t, page, 200)
	if !strings.Contains(page.Body.String(), `data-language="zh-CN"`) || !strings.Contains(page.Body.String(), `type="module"`) {
		t.Fatal("page is missing its language controls or module entry point")
	}
	if !strings.Contains(page.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatal("language support weakened the script policy")
	}
}

func TestEndToEndProgressPermissionsAndRange(t *testing.T) {
	r := newRig(t)
	alice := r.login(t, "alice")
	// canAccessOpds=false does not gate this adapter, while canDownload does.
	feed := r.request("GET", "/opds", alice, nil, nil)
	assertCode(t, feed, 200)
	if !strings.Contains(feed.Body.String(), "/books/42/epub") {
		t.Fatal("missing acquisition link")
	}
	identity, download := r.acquire(t, alice)
	if !strings.Contains(identity, "/files/901/revisions/") {
		t.Fatal(identity)
	}
	chapters := r.request("GET", "/reader3/getChapterList?url="+url.QueryEscape(identity), alice, nil, nil)
	assertCode(t, chapters, 200)
	shelf := r.request("GET", "/reader3/getBookshelf", alice, nil, nil)
	assertCode(t, shelf, 200)
	if strings.Contains(shelf.Body.String(), "durChapter") {
		t.Fatal("invented initial cloud progress")
	}
	assertCode(t, r.save(alice, identity, 1), 200)
	r.m.mu.Lock()
	if len(r.m.writes) != 1 || r.m.writes[0].BookID != 42 || r.m.writes[0].File.FileID != 901 {
		t.Fatal("wrong upstream identity")
	}
	r.m.clock = r.m.clock.Add(time.Minute)
	stamp := r.m.clock
	r.m.users[1].progress = &grimmory.Progress{CFI: "epubcfi(/6/2[ref-a]!/4/4/4/1:4)", Href: "OPS/Text/a.xhtml#two", Percent: number(44.5)}
	r.m.users[1].last = &stamp
	r.m.mu.Unlock()
	info := r.request("GET", "/reader3/getShelfBook?url="+url.QueryEscape(identity), alice, nil, nil)
	assertCode(t, info, 200)
	var result struct{ Data map[string]any }
	json.Unmarshal(info.Body.Bytes(), &result)
	if result.Data["durChapterIndex"] != float64(1) || result.Data["durChapterTime"] != float64(stamp.UnixMilli()) {
		t.Fatal("Web update not restored", info.Body.String())
	}
	assertCode(t, r.save(alice, identity, 1), 200)
	r.m.mu.Lock()
	write := r.m.writes[len(r.m.writes)-1]
	r.m.mu.Unlock()
	if write.File.CFI == nil || write.File.Percent != 44.5 {
		t.Fatal("same chapter lost precise progress")
	}
	assertCode(t, r.save(alice, identity, 2), 200)
	r.m.mu.Lock()
	write = r.m.writes[len(r.m.writes)-1]
	r.m.mu.Unlock()
	if write.File.CFI != nil || write.File.InSection != nil || write.File.Href != "OPS/Text/b.xhtml#three" || write.File.Percent >= 99.5 {
		t.Fatal("conflicting or completed chapter progress")
	}
	assertCode(t, r.save(alice, identity, 0), 200) // Explicit backwards progress is valid.
	rangeResp := r.request("GET", "/books/42/epub", alice, nil, map[string]string{"Range": "bytes=0-9"})
	assertCode(t, rangeResp, 206)
	if rangeResp.Body.Len() != 10 || !bytes.Equal(rangeResp.Body.Bytes(), download.Body.Bytes()[:10]) {
		t.Fatal("wrong derived Range")
	}
	assertCode(t, r.request("GET", "/books/42/epub", alice, nil, map[string]string{"Range": "bytes=99999999-"}), 416)
	assertCode(t, r.request("GET", "/books/42/epub", alice, nil, map[string]string{"If-None-Match": download.Header().Get("ETag")}), 304)
	r.m.mu.Lock()
	r.m.users[1].download = false
	r.m.users[1].opds = true
	r.m.mu.Unlock()
	for _, headers := range []map[string]string{nil, {"Range": "bytes=0-9"}, {"If-None-Match": download.Header().Get("ETag")}} {
		assertCode(t, r.request("GET", "/books/42/epub", alice, nil, headers), 403)
	}
	assertCode(t, r.save(alice, identity, 1), 200)
	r.m.mu.Lock()
	r.m.users[1].blocked = true
	count := len(r.m.writes)
	r.m.mu.Unlock()
	assertCode(t, r.save(alice, identity, 2), 403)
	r.m.mu.Lock()
	if len(r.m.writes) != count {
		t.Fatal("write after access revocation")
	}
	r.m.mu.Unlock()
	if strings.Contains(r.logs.String(), alice) || strings.Contains(r.logs.String(), "test-password") || strings.Contains(r.logs.String(), "access-1-") {
		t.Fatal("credential leaked into application logs")
	}
}

func assertReaderProgress(t *testing.T, response *httptest.ResponseRecorder, index int, stamp time.Time) {
	t.Helper()
	assertCode(t, response, 200)
	var result struct{ Data map[string]any }
	if e := json.Unmarshal(response.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if result.Data["durChapterIndex"] != float64(index) || result.Data["durChapterTime"] != float64(stamp.UnixMilli()) {
		t.Fatal("incorrect cloud position or time", response.Body.String())
	}
}

func TestAdoptsExistingGrimmoryProgress(t *testing.T) {
	for _, tc := range []struct {
		name     string
		progress *grimmory.Progress
	}{
		{"href", &grimmory.Progress{Href: "OPS/Text/a.xhtml#two", Percent: number(40)}},
		{"cfi", &grimmory.Progress{CFI: "epubcfi(/6/2[ref-a]!/4/4/4/1:4)", Href: "OPS/Text/a.xhtml#two", Percent: number(44.5)}},
		{"percentage", &grimmory.Progress{Percent: number(40)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			token := r.login(t, "alice")
			stamp := time.Date(2025, 6, 2, 3, 4, 5, 0, time.UTC)
			r.m.users[1].progress, r.m.users[1].last = tc.progress, &stamp
			before := positionHash(tc.progress)
			identity, download := r.acquire(t, token)
			if code := download.Header().Get("X-Adapter-Sync-Status"); code != "" {
				t.Fatal("valid existing progress was rejected", code)
			}
			path := "/reader3/getShelfBook?url=" + url.QueryEscape(identity)
			assertReaderProgress(t, r.request("GET", path, token, nil, nil), 1, stamp)
			shelf := r.request("GET", "/reader3/getBookshelf", token, nil, nil)
			assertCode(t, shelf, 200)
			var listed struct{ Data []map[string]any }
			if e := json.Unmarshal(shelf.Body.Bytes(), &listed); e != nil || len(listed.Data) != 1 || listed.Data[0]["durChapterIndex"] != float64(1) || listed.Data[0]["durChapterTime"] != float64(stamp.UnixMilli()) {
				t.Fatal("adopted book missing from shelf", shelf.Body.String(), e)
			}
			bound, exists, e := r.a.DB.Binding(context.Background(), 1, 42)
			if e != nil || !exists || bound.State != "bound" || bound.Code != "" || bound.FileID != 901 || bound.PositionHash != before || bound.PositionTime != stamp.UnixMilli() || bound.LastSuccess != 0 {
				t.Fatalf("incorrect adopted binding: %+v %v", bound, e)
			}
			if len(r.m.writes) != 0 || positionHash(r.m.users[1].progress) != before || !r.m.users[1].last.Equal(stamp) {
				t.Fatal("adoption changed the upstream record")
			}
			if e = r.a.Close(); e != nil {
				t.Fatal(e)
			}
			r.a, e = New(r.cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if e != nil {
				t.Fatal(e)
			}
			assertReaderProgress(t, r.request("GET", path, token, nil, nil), 1, stamp)
			stamp = stamp.Add(time.Minute)
			r.m.users[1].progress = &grimmory.Progress{Href: "OPS/Text/b.xhtml#three", Percent: number(75)}
			assertReaderProgress(t, r.request("GET", path, token, nil, nil), 2, stamp)
			assertCode(t, r.save(token, identity, 0), 200)
			if len(r.m.writes) != 1 || r.m.writes[0].File.FileID != 901 || r.m.writes[0].File.Href != "OPS/Text/a.xhtml#one" {
				t.Fatal("adopted binding did not support a backward save")
			}
		})
	}
}

func TestFirstSaveCannotOverwriteUnadoptedProgress(t *testing.T) {
	for _, state := range []string{"absent", "new", "legacy"} {
		t.Run(state, func(t *testing.T) {
			r := newRig(t)
			token := r.login(t, "alice")
			identity, _ := r.acquire(t, token)
			userID := int64(1)
			if state == "absent" {
				token, userID = r.login(t, "bob"), 2
			} else if state == "legacy" {
				bound, _, _ := r.a.DB.Binding(context.Background(), userID, 42)
				bound.State, bound.Code = "paused", "HISTORICAL_PROGRESS_UNBOUND"
				if e := r.a.DB.SaveBinding(context.Background(), bound); e != nil {
					t.Fatal(e)
				}
			}
			stamp := r.m.clock
			r.m.users[userID].progress = &grimmory.Progress{Href: "OPS/Text/a.xhtml#two", Percent: number(40)}
			r.m.users[userID].last = &stamp
			before := positionHash(r.m.users[userID].progress)
			for i := 0; i < 2; i++ {
				response := r.save(token, identity, 0)
				assertCode(t, response, 409)
				if !strings.Contains(response.Body.String(), "UPSTREAM_PROGRESS_AVAILABLE") {
					t.Fatal(response.Body.String())
				}
			}
			bound, _, e := r.a.DB.Binding(context.Background(), userID, 42)
			if e != nil || bound.State == "bound" || len(r.m.writes) != 0 || positionHash(r.m.users[userID].progress) != before || !r.m.users[userID].last.Equal(stamp) {
				t.Fatalf("save adopted or overwrote unseen history: %+v %v", bound, e)
			}
			path := "/reader3/getShelfBook?url=" + url.QueryEscape(identity)
			assertReaderProgress(t, r.request("GET", path, token, nil, nil), 1, stamp)
			assertCode(t, r.save(token, identity, 0), 200)
			if len(r.m.writes) != 1 {
				t.Fatal("save remained blocked after reading the upstream position")
			}
		})
	}
}

func TestUnboundProgressMustBeValid(t *testing.T) {
	stamp := time.Date(2025, 6, 2, 3, 4, 5, 0, time.UTC)
	epoch := time.Unix(0, 0)
	valid := &grimmory.Progress{Href: "OPS/Text/a.xhtml#two", Percent: number(40)}
	for _, tc := range []struct {
		name, code string
		progress   *grimmory.Progress
		time       *time.Time
		pdf        json.RawMessage
	}{
		{name: "missing-position", code: "PROGRESS_SOURCE_UNKNOWN", time: &stamp},
		{name: "empty-position", code: "PROGRESS_SOURCE_UNKNOWN", progress: &grimmory.Progress{}, time: &stamp},
		{name: "missing-time", code: "PROGRESS_TIME_UNKNOWN", progress: valid},
		{name: "nonpositive-time", code: "PROGRESS_TIME_UNKNOWN", progress: valid, time: &epoch},
		{name: "invalid-cfi", code: "INVALID_PROGRESS_CFI", progress: &grimmory.Progress{CFI: "invalid", Href: valid.Href}, time: &stamp},
		{name: "invalid-href", code: "INVALID_PROGRESS_HREF", progress: &grimmory.Progress{Href: "OPS/Text/a.xhtml#missing"}, time: &stamp},
		{name: "conflicting-position", code: "CONFLICTING_PROGRESS_POSITION", progress: &grimmory.Progress{CFI: "epubcfi(/6/2[ref-a]!/4/4/4/1:4)", Href: "OPS/Text/b.xhtml#three"}, time: &stamp},
		{name: "invalid-percent", code: "INVALID_PROGRESS_PERCENT", progress: &grimmory.Progress{Href: valid.Href, Percent: number(101)}, time: &stamp},
		{name: "other-format-progress", code: "PROGRESS_SOURCE_UNKNOWN", progress: valid, time: &stamp, pdf: json.RawMessage(`{"page":2}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			token := r.login(t, "alice")
			r.m.users[1].progress, r.m.users[1].last, r.m.users[1].pdf = tc.progress, tc.time, tc.pdf
			before := positionHash(tc.progress)
			identity, download := r.acquire(t, token)
			if download.Header().Get("X-Adapter-Sync-Status") != tc.code {
				t.Fatal(download.Header())
			}
			response := r.request("GET", "/reader3/getShelfBook?url="+url.QueryEscape(identity), token, nil, nil)
			assertCode(t, response, 409)
			if !strings.Contains(response.Body.String(), tc.code) || strings.Contains(response.Body.String(), "durChapterIndex") {
				t.Fatal("invalid history became a cloud position", response.Body.String())
			}
			assertCode(t, r.save(token, identity, 0), 409)
			if len(r.m.writes) != 0 || positionHash(r.m.users[1].progress) != before || r.m.users[1].last != tc.time {
				t.Fatal("invalid history was overwritten")
			}
		})
	}
}

func TestLegacyHistoricalPauseRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		change     func(*store.Binding)
		noHistory  bool
	}{
		{name: "untouched-history"},
		{name: "changed-file", code: "EPUB_VERSION_CHANGED", change: func(b *store.Binding) { b.FileID++ }},
		{name: "changed-version", code: "EPUB_VERSION_CHANGED", change: func(b *store.Binding) { b.Revision = strings.Repeat("0", 64) }},
		{name: "position-hash", code: "HISTORICAL_PROGRESS_UNBOUND", change: func(b *store.Binding) { b.PositionHash = "previously-used" }},
		{name: "position-time", code: "HISTORICAL_PROGRESS_UNBOUND", change: func(b *store.Binding) { b.PositionTime = 1 }},
		{name: "successful-save", code: "HISTORICAL_PROGRESS_UNBOUND", change: func(b *store.Binding) { b.LastSuccess = 1 }},
		{name: "reset-pause", code: "UPSTREAM_PROGRESS_RESET", change: func(b *store.Binding) { b.Code = "UPSTREAM_PROGRESS_RESET" }},
		{name: "time-pause", code: "PROGRESS_TIME_AMBIGUOUS", change: func(b *store.Binding) { b.Code = "PROGRESS_TIME_AMBIGUOUS" }},
		{name: "pending-write", code: "INCOMPLETE_BINDING_WRITE", change: func(b *store.Binding) { b.State = "pending" }},
		{name: "cleared-history", code: "PROGRESS_SOURCE_UNKNOWN", noHistory: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			token := r.login(t, "alice")
			identity, _ := r.acquire(t, token)
			bound, _, e := r.a.DB.Binding(context.Background(), 1, 42)
			if e != nil {
				t.Fatal(e)
			}
			bound.State, bound.Code = "paused", "HISTORICAL_PROGRESS_UNBOUND"
			if tc.change != nil {
				tc.change(&bound)
			}
			if e = r.a.DB.SaveBinding(context.Background(), bound); e != nil {
				t.Fatal(e)
			}
			stamp := r.m.clock
			if !tc.noHistory {
				r.m.users[1].progress = &grimmory.Progress{Href: "OPS/Text/a.xhtml#two", Percent: number(40)}
				r.m.users[1].last = &stamp
			}
			response := r.request("GET", "/reader3/getShelfBook?url="+url.QueryEscape(identity), token, nil, nil)
			if tc.code == "" {
				assertReaderProgress(t, response, 1, stamp)
			} else {
				assertCode(t, response, 409)
				if !strings.Contains(response.Body.String(), tc.code) {
					t.Fatal(response.Body.String())
				}
				assertCode(t, r.save(token, identity, 0), 409)
			}
			if len(r.m.writes) != 0 {
				t.Fatal("legacy binding recovery wrote upstream")
			}
		})
	}
}

func TestVersionChangesAndResetNeverWrite(t *testing.T) {
	t.Run("revision", func(t *testing.T) {
		r := newRig(t)
		token := r.login(t, "alice")
		old, _ := r.acquire(t, token)
		assertCode(t, r.save(token, old, 1), 200)
		r.m.mu.Lock()
		r.m.content = testfixture.EPUB("R2 changed bytes, same size validators")
		count := len(r.m.writes)
		r.m.mu.Unlock()
		assertCode(t, r.save(token, old, 0), 409)
		next, _ := r.acquire(t, token)
		if next == old {
			t.Fatal("version identity unchanged")
		}
		assertCode(t, r.save(token, next, 0), 409)
		r.m.mu.Lock()
		defer r.m.mu.Unlock()
		if len(r.m.writes) != count {
			t.Fatal("old mapping wrote into new content")
		}
	})
	t.Run("multiformat", func(t *testing.T) {
		r := newRig(t)
		token := r.login(t, "alice")
		identity, _ := r.acquire(t, token)
		r.m.mu.Lock()
		r.m.extra = true
		r.m.mu.Unlock()
		assertCode(t, r.save(token, identity, 0), 409)
		r.m.mu.Lock()
		defer r.m.mu.Unlock()
		if len(r.m.writes) != 0 {
			t.Fatal("multi-file write")
		}
	})
	t.Run("reset", func(t *testing.T) {
		r := newRig(t)
		token := r.login(t, "alice")
		identity, _ := r.acquire(t, token)
		assertCode(t, r.save(token, identity, 1), 200)
		r.m.mu.Lock()
		r.m.users[1].progress = nil
		r.m.users[1].last = nil
		r.m.mu.Unlock()
		assertCode(t, r.save(token, identity, 0), 409)
		bound, _, _ := r.a.DB.Binding(context.Background(), 1, 42)
		if bound.Code != "UPSTREAM_PROGRESS_RESET" {
			t.Fatal(bound)
		}
	})
}

func TestUsersTokensRefreshAndRestart(t *testing.T) {
	r := newRig(t)
	alice, bob, admin := r.login(t, "alice"), r.login(t, "bob"), r.login(t, "admin")
	identity, _ := r.acquire(t, alice)
	assertCode(t, r.save(alice, identity, 1), 200)
	assertCode(t, r.request("GET", "/reader3/getShelfBook?url="+url.QueryEscape(identity), bob, nil, nil), 200)
	bound, exists, e := r.a.DB.Binding(context.Background(), 2, 42)
	if e != nil || !exists || bound.State != "new" {
		t.Fatalf("user progress leaked: %+v %v", bound, e)
	}
	assertCode(t, r.request("GET", "/books/42/epub", admin, nil, nil), 200)
	assertCode(t, r.save(bob, strings.Replace(identity, "/files/901/", "/files/902/", 1), 0), 409)
	assertCode(t, r.save(bob, identity, 999), 400)
	assertCode(t, r.request("GET", "/books/42/epub", "", nil, nil), 401)
	assertCode(t, r.request("GET", "/api/status", alice, nil, map[string]string{"Cookie": "adapter_session=" + bob}), 401)
	ctx := context.Background()
	s, _ := r.a.DB.ByToken(ctx, alice)
	s.Expires = 0
	r.a.DB.SaveSession(ctx, s)
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- r.request("GET", "/reader3/getUserInfo", alice, nil, nil).Code }()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 200 {
			t.Fatal("concurrent refresh failed", code)
		}
	}
	r.m.mu.Lock()
	refreshes := r.m.refreshes
	r.m.mu.Unlock()
	if refreshes != 1 {
		t.Fatal("refresh was not serialized", refreshes)
	}
	r.a.Close()
	a, e := New(r.cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e != nil {
		t.Fatal(e)
	}
	r.a = a
	assertCode(t, r.request("GET", "/reader3/getShelfBook?url="+url.QueryEscape(identity), alice, nil, nil), 200)
	assertCode(t, r.request("POST", "/reader3/logout", alice, nil, nil), 200)
	assertCode(t, r.request("GET", "/api/status", alice, nil, nil), 401)
	assertCode(t, r.request("GET", "/api/status", bob, nil, nil), 200)
}

func TestReadFailureAndInitialBindingRace(t *testing.T) {
	t.Run("upstream-read", func(t *testing.T) {
		r := newRig(t)
		token := r.login(t, "alice")
		identity, _ := r.acquire(t, token)
		assertCode(t, r.save(token, identity, 1), 200)
		before, _, _ := r.a.DB.Binding(context.Background(), 1, 42)
		r.m.mu.Lock()
		r.m.failRead = true
		count := len(r.m.writes)
		r.m.mu.Unlock()
		assertCode(t, r.save(token, identity, 0), 503)
		after, _, _ := r.a.DB.Binding(context.Background(), 1, 42)
		if before.State != after.State || before.PositionHash != after.PositionHash {
			t.Fatal("read failure destroyed binding")
		}
		if after.Code == "" {
			t.Fatal("read failure was not visible in synchronization status")
		}
		r.m.mu.Lock()
		defer r.m.mu.Unlock()
		if len(r.m.writes) != count {
			t.Fatal("wrote after read failure")
		}
	})
	t.Run("binding-race", func(t *testing.T) {
		r := newRig(t)
		token := r.login(t, "alice")
		identity, _ := r.acquire(t, token)
		r.m.mu.Lock()
		r.m.race = true
		r.m.mu.Unlock()
		assertCode(t, r.save(token, identity, 1), 409)
		bound, _, _ := r.a.DB.Binding(context.Background(), 1, 42)
		if bound.State != "paused" || bound.Code != "INITIAL_BINDING_RACE" {
			t.Fatal(bound)
		}
	})
}

func TestRealWebSpreadCFIUsesContainedHrefAndClearsOnSave(t *testing.T) {
	r := newRig(t)
	token := r.login(t, "alice")
	identity, _ := r.acquire(t, token)
	assertCode(t, r.save(token, identity, 0), 200)
	r.m.mu.Lock()
	r.m.clock = r.m.clock.Add(time.Minute)
	stamp := r.m.clock
	r.m.users[1].progress = &grimmory.Progress{CFI: "epubcfi(/6/2!/4,/2/4/1:0,/4/4/1:0)", Href: "OPS/Text/a.xhtml#two", Percent: number(44.5)}
	r.m.users[1].last = &stamp
	r.m.mu.Unlock()
	info := r.request("GET", "/reader3/getShelfBook?url="+url.QueryEscape(identity), token, nil, nil)
	assertCode(t, info, 200)
	var result struct{ Data map[string]any }
	json.Unmarshal(info.Body.Bytes(), &result)
	if result.Data["durChapterIndex"] != float64(1) {
		t.Fatal("viewport's valid href did not resolve current chapter")
	}
	assertCode(t, r.save(token, identity, 1), 200)
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	write := r.m.writes[len(r.m.writes)-1].File
	if write.CFI != nil || write.Href != "OPS/Text/a.xhtml#two" {
		t.Fatal("cross-chapter viewport CFI was preserved")
	}
}

func TestWebSpreadRejectsUnrelatedOrInvalidHref(t *testing.T) {
	for _, href := range []string{"OPS/Text/b.xhtml#three", "OPS/Text/a.xhtml#missing"} {
		t.Run(href, func(t *testing.T) {
			r := newRig(t)
			token := r.login(t, "alice")
			identity, _ := r.acquire(t, token)
			assertCode(t, r.save(token, identity, 0), 200)
			r.m.mu.Lock()
			r.m.clock = r.m.clock.Add(time.Minute)
			stamp := r.m.clock
			r.m.users[1].progress = &grimmory.Progress{CFI: "epubcfi(/6/2!/4,/2/4/1:0,/4/4/1:0)", Href: href, Percent: number(44.5)}
			r.m.users[1].last = &stamp
			r.m.mu.Unlock()
			assertCode(t, r.request("GET", "/reader3/getShelfBook?url="+url.QueryEscape(identity), token, nil, nil), 409)
			assertCode(t, r.save(token, identity, 1), 409)
			r.m.mu.Lock()
			defer r.m.mu.Unlock()
			if len(r.m.writes) != 1 {
				t.Fatal("invalid viewport position allowed an upstream write")
			}
		})
	}
}
