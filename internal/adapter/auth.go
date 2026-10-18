package adapter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"grimmoryrodelbridge/internal/grimmory"
	"grimmoryrodelbridge/internal/store"
)

type fault struct {
	Code   string
	Status int
}

func (e *fault) Error() string           { return e.Code }
func fail(code string, status int) error { return &fault{code, status} }
func publicError(err error) (string, int) {
	var f *fault
	if errors.As(err, &f) {
		return f.Code, f.Status
	}
	var up *grimmory.Error
	if errors.As(err, &up) {
		switch up.Status {
		case 401:
			return "NEED_LOGIN", 401
		case 403:
			return "BOOK_ACCESS_DENIED", 403
		case 404:
			return "BOOK_NOT_FOUND", 404
		case 429:
			return "UPSTREAM_RATE_LIMITED", 503
		}
	}
	return "UPSTREAM_OR_STORAGE_UNAVAILABLE", 503
}

func (a *Server) authenticate(r *http.Request) (store.Session, error) {
	var tokens []string
	if s := r.URL.Query().Get("accessToken"); s != "" {
		tokens = append(tokens, s)
	}
	if _, password, ok := r.BasicAuth(); ok {
		tokens = append(tokens, password)
	} else if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tokens = append(tokens, strings.TrimPrefix(h, "Bearer "))
	}
	if c, e := r.Cookie("adapter_session"); e == nil {
		tokens = append(tokens, c.Value)
	}
	if len(tokens) == 0 {
		return store.Session{}, fail("NEED_LOGIN", 401)
	}
	token := tokens[0]
	for _, s := range tokens {
		if s != token {
			return store.Session{}, fail("AMBIGUOUS_CREDENTIALS", 401)
		}
	}
	if len(token) != 64 {
		return store.Session{}, fail("NEED_LOGIN", 401)
	}
	session, err := a.DB.ByToken(r.Context(), token)
	if err != nil {
		return session, fail("NEED_LOGIN", 401)
	}
	return session, nil
}

// Every outgoing request reloads the session. Only refresh/dispatch is serialized;
// stream consumption does not hold the lock. Credentials never appear in errors.
func (a *Server) upstream(ctx context.Context, session store.Session, method, path string, body any) (*http.Response, error) {
	lock := &a.authLocks[lockIndex(session.ID)]
	lock.Lock()
	defer lock.Unlock()
	current, err := a.DB.Session(ctx, session.ID)
	if err != nil {
		return nil, fail("NEED_LOGIN", 401)
	}
	refresh := func() error {
		t, e := a.Upstream.Refresh(ctx, current.Refresh)
		if e != nil {
			var u *grimmory.Error
			if errors.As(e, &u) && (u.Status == 400 || u.Status == 401 || u.Status == 403) {
				a.DB.Revoke(ctx, current.ID)
				return fail("NEED_LOGIN", 401)
			}
			return e
		}
		current.Access, current.Refresh, current.Expires = t.Access, t.Refresh, time.Now().Add(time.Duration(t.Expires)*time.Second).Unix()
		return a.DB.SaveSession(ctx, current)
	}
	if current.Expires <= time.Now().Add(30*time.Second).Unix() {
		if err = refresh(); err != nil {
			return nil, err
		}
	}
	resp, err := a.Upstream.Raw(ctx, current.Access, method, path, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 401 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if err = refresh(); err != nil {
			return nil, err
		}
		return a.Upstream.Raw(ctx, current.Access, method, path, body)
	}
	return resp, nil
}
func (a *Server) upstreamJSON(ctx context.Context, s store.Session, method, path string, body, out any) error {
	resp, err := a.upstream(ctx, s, method, path, body)
	if err != nil {
		return err
	}
	return grimmory.Decode(resp, out)
}
func (a *Server) user(ctx context.Context, s store.Session) (grimmory.User, error) {
	var u grimmory.User
	err := a.upstreamJSON(ctx, s, "GET", "/api/v1/users/me", nil, &u)
	if err == nil && (u.ID <= 0 || u.ID != s.UserID) {
		err = fail("USER_ID_CHANGED", 401)
	}
	return u, err
}
func (a *Server) book(ctx context.Context, s store.Session, id int64) (grimmory.Book, error) {
	var b grimmory.Book
	if id <= 0 {
		return b, fail("INVALID_BOOK_ID", 400)
	}
	err := a.upstreamJSON(ctx, s, "GET", "/api/v1/books/"+strconv.FormatInt(id, 10)+"?withDescription=true", nil, &b)
	if err == nil && b.ID != id {
		err = fail("UPSTREAM_BOOK_ID_MISMATCH", 502)
	}
	return b, err
}
func (a *Server) page(ctx context.Context, s store.Session, page int) (grimmory.Page, error) {
	var p grimmory.Page
	// An explicit empty sort selects browse mode with its implicit ascending ID
	// tiebreaker. v3.5.0 reserves 'id', and rejects both 'id' and 'id,asc'.
	err := a.upstreamJSON(ctx, s, "GET", "/api/v1/books/page?page="+strconv.Itoa(page)+"&size=50&sort=", nil, &p)
	return p, err
}
func lockIndex(s string) uint32 {
	var v uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		v = (v ^ uint32(s[i])) * 16777619
	}
	return v % 64
}
