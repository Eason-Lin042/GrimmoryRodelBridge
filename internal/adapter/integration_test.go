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
	"grimmoryrodelbridge/internal/testfixture"
)

type mockUser struct {
	name                  string
	download, admin, opds bool
	progress              *grimmory.Progress
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
	b := grimmory.Book{ID: 42, Title: "Synthetic", Primary: &grimmory.File{ID: 901, BookID: 42, Type: "EPUB", IsBook: true}, Metadata: grimmory.Metadata{Title: "Synthetic", Authors: []string{"Author"}}, EPUB: u.progress, LastRead: u.last}
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

func TestHistoricalUnknownAndVersionChangesNeverWrite(t *testing.T) {
	t.Run("historical", func(t *testing.T) {
		r := newRig(t)
		token := r.login(t, "alice")
		r.m.mu.Lock()
		r.m.users[1].progress = &grimmory.Progress{Href: "OPS/Text/a.xhtml#two", Percent: number(40)}
		stamp := time.Now()
		r.m.users[1].last = &stamp
		r.m.mu.Unlock()
		identity, download := r.acquire(t, token)
		if download.Header().Get("X-Adapter-Sync-Status") != "HISTORICAL_PROGRESS_UNBOUND" {
			t.Fatal("historical position not quarantined")
		}
		assertCode(t, r.save(token, identity, 0), 409)
		r.m.mu.Lock()
		defer r.m.mu.Unlock()
		if len(r.m.writes) != 0 || *r.m.users[1].progress.Percent != 40 {
			t.Fatal("historical progress overwritten")
		}
	})
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
