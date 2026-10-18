package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	aead cipher.AEAD
}
type Session struct {
	ID, TokenHash, Username string
	UserID                  int64
	Access, Refresh         string
	Expires, Created        int64
}
type Binding struct {
	UserID, BookID, FileID        int64
	Revision, State, PositionHash string
	PositionTime, LastSuccess     int64
	Code                          string
}
type Revision struct {
	BookID, FileID       int64
	Revision, SourceHash string
}

func Open(dir string, key []byte) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "adapter.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, aead: aead}
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sessions (id TEXT PRIMARY KEY, token_hash TEXT UNIQUE NOT NULL, user_id INTEGER NOT NULL, username TEXT NOT NULL, credentials BLOB NOT NULL, created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS bindings (user_id INTEGER NOT NULL, book_id INTEGER NOT NULL, file_id INTEGER NOT NULL, revision TEXT NOT NULL, state TEXT NOT NULL, position_hash TEXT NOT NULL, position_time INTEGER NOT NULL, last_success INTEGER NOT NULL, code TEXT NOT NULL, PRIMARY KEY(user_id,book_id));
CREATE TABLE IF NOT EXISTS revisions (book_id INTEGER PRIMARY KEY, file_id INTEGER NOT NULL, revision TEXT NOT NULL, source_hash TEXT NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	// Detect a wrong encryption key before accepting requests, even with no sessions.
	// The legacy proof value is part of the persisted format, independent of branding.
	var proof []byte
	err = db.QueryRow("SELECT value FROM metadata WHERE key='key_check'").Scan(&proof)
	if errors.Is(err, sql.ErrNoRows) {
		proof, err = s.seal([]byte("grimmory-adapter-v1"), "key_check")
		if err == nil {
			_, err = db.Exec("INSERT INTO metadata(key,value) VALUES('key_check',?)", proof)
		}
	} else if err == nil {
		var plain []byte
		plain, err = s.unseal(proof, "key_check")
		if err == nil && string(plain) != "grimmory-adapter-v1" {
			err = fmt.Errorf("invalid key check")
		}
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("cannot unlock adapter store: %w", err)
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func Random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (s *Store) seal(plain []byte, id string) ([]byte, error) {
	n := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(n); err != nil {
		return nil, err
	}
	return s.aead.Seal(n, n, plain, []byte(id)), nil
}
func (s *Store) unseal(data []byte, id string) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(data) < n {
		return nil, fmt.Errorf("invalid encrypted record")
	}
	return s.aead.Open(nil, data[:n], data[n:], []byte(id))
}
func (s *Store) Instance(ctx context.Context) (string, error) {
	_, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO metadata(key,value) VALUES('instance',?)", Random()[:24])
	if err != nil {
		return "", err
	}
	var id string
	err = s.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='instance'").Scan(&id)
	return id, err
}
func (s *Store) SaveSession(ctx context.Context, v Session) error {
	plain, err := json.Marshal(struct {
		Access, Refresh string
		Expires         int64
	}{v.Access, v.Refresh, v.Expires})
	if err != nil {
		return err
	}
	data, err := s.seal(plain, v.ID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO sessions(id,token_hash,user_id,username,credentials,created) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET credentials=excluded.credentials`, v.ID, v.TokenHash, v.UserID, v.Username, data, v.Created)
	return err
}
func (s *Store) session(ctx context.Context, query string, arg string) (Session, error) {
	var v Session
	var data []byte
	err := s.db.QueryRowContext(ctx, query, arg).Scan(&v.ID, &v.TokenHash, &v.UserID, &v.Username, &data, &v.Created)
	if err != nil {
		return v, err
	}
	plain, err := s.unseal(data, v.ID)
	if err != nil {
		return v, err
	}
	var creds struct {
		Access, Refresh string
		Expires         int64
	}
	if err = json.Unmarshal(plain, &creds); err != nil {
		return v, err
	}
	v.Access, v.Refresh, v.Expires = creds.Access, creds.Refresh, creds.Expires
	return v, nil
}
func (s *Store) ByToken(ctx context.Context, token string) (Session, error) {
	return s.session(ctx, "SELECT id,token_hash,user_id,username,credentials,created FROM sessions WHERE token_hash=?", Hash(token))
}
func (s *Store) Session(ctx context.Context, id string) (Session, error) {
	return s.session(ctx, "SELECT id,token_hash,user_id,username,credentials,created FROM sessions WHERE id=?", id)
}
func (s *Store) Revoke(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id=?", id)
	return err
}
func (s *Store) Binding(ctx context.Context, user, book int64) (Binding, bool, error) {
	var b Binding
	err := s.db.QueryRowContext(ctx, "SELECT user_id,book_id,file_id,revision,state,position_hash,position_time,last_success,code FROM bindings WHERE user_id=? AND book_id=?", user, book).Scan(&b.UserID, &b.BookID, &b.FileID, &b.Revision, &b.State, &b.PositionHash, &b.PositionTime, &b.LastSuccess, &b.Code)
	if errors.Is(err, sql.ErrNoRows) {
		return b, false, nil
	}
	return b, err == nil, err
}
func (s *Store) SaveBinding(ctx context.Context, b Binding) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO bindings(user_id,book_id,file_id,revision,state,position_hash,position_time,last_success,code) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(user_id,book_id) DO UPDATE SET file_id=excluded.file_id,revision=excluded.revision,state=excluded.state,position_hash=excluded.position_hash,position_time=excluded.position_time,last_success=excluded.last_success,code=excluded.code`, b.UserID, b.BookID, b.FileID, b.Revision, b.State, b.PositionHash, b.PositionTime, b.LastSuccess, b.Code)
	return err
}
func (s *Store) Invalidate(ctx context.Context, book int64, code string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE bindings SET state='paused',code=? WHERE book_id=?", code, book)
	return err
}
func (s *Store) RecordFailure(ctx context.Context, user, book int64, code string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE bindings SET code=? WHERE user_id=? AND book_id=? AND state NOT IN ('paused','pending')", code, user, book)
	return err
}
func (s *Store) Revision(ctx context.Context, book int64) (Revision, bool, error) {
	var v Revision
	err := s.db.QueryRowContext(ctx, "SELECT book_id,file_id,revision,source_hash FROM revisions WHERE book_id=?", book).Scan(&v.BookID, &v.FileID, &v.Revision, &v.SourceHash)
	if errors.Is(err, sql.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}
func (s *Store) SaveRevision(ctx context.Context, v Revision) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO revisions(book_id,file_id,revision,source_hash) VALUES(?,?,?,?) ON CONFLICT(book_id) DO UPDATE SET file_id=excluded.file_id,revision=excluded.revision,source_hash=excluded.source_hash`, v.BookID, v.FileID, v.Revision, v.SourceHash)
	return err
}
func (s *Store) CheckConfig(ctx context.Context, upstream, publicURL string) error {
	hash := Hash(upstream + "\n" + publicURL)
	_, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO metadata(key,value) VALUES('configuration',?)", hash)
	if err != nil {
		return err
	}
	var existing string
	if err = s.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='configuration'").Scan(&existing); err != nil {
		return err
	}
	if existing != hash {
		return fmt.Errorf("data directory is bound to a different upstream/public URL; use a new data directory")
	}
	return nil
}

type Status struct {
	BookID      int64  `json:"bookId"`
	FileID      int64  `json:"fileId"`
	Revision    string `json:"revision"`
	State       string `json:"state"`
	Code        string `json:"code"`
	LastSuccess int64  `json:"lastSuccess"`
}

func (s *Store) Status(ctx context.Context, user int64) ([]Status, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT book_id,file_id,revision,state,code,last_success FROM bindings WHERE user_id=? ORDER BY book_id", user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Status{}
	for rows.Next() {
		var v Status
		if err = rows.Scan(&v.BookID, &v.FileID, &v.Revision, &v.State, &v.Code, &v.LastSuccess); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func Now() int64 { return time.Now().UnixMilli() }
