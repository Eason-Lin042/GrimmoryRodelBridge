package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialsEncryptedAndKeyChecked(t *testing.T) {
	dir := t.TempDir()
	key := bytes.Repeat([]byte{1}, 32)
	s, e := Open(dir, key)
	if e != nil {
		t.Fatal(e)
	}
	v := Session{ID: Random(), TokenHash: Hash("test"), UserID: 1, Username: "alice", Access: "private-access-sentinel", Refresh: "private-refresh-sentinel", Expires: 99}
	if e = s.SaveSession(context.Background(), v); e != nil {
		t.Fatal(e)
	}
	got, e := s.ByToken(context.Background(), "test")
	if e != nil || got.Access != v.Access || got.Refresh != v.Refresh {
		t.Fatal("credential roundtrip", e)
	}
	s.Close()
	for _, name := range []string{"adapter.db", "adapter.db-wal"} {
		raw, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil && os.IsNotExist(e) {
			continue
		}
		if bytes.Contains(raw, []byte(v.Access)) || bytes.Contains(raw, []byte(v.Refresh)) {
			t.Fatal("plaintext credential in SQLite")
		}
	}
	if s, e = Open(dir, bytes.Repeat([]byte{2}, 32)); e == nil {
		s.Close()
		t.Fatal("wrong key accepted")
	}
}
