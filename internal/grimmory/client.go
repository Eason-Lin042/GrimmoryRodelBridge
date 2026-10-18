package grimmory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Tokens struct {
	Access  string `json:"accessToken"`
	Refresh string `json:"refreshToken"`
	Expires int64  `json:"expires"`
}
type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Permissions struct {
		Admin    bool `json:"admin"`
		Download bool `json:"canDownload"`
		OPDS     bool `json:"canAccessOpds"`
	} `json:"permissions"`
}
type File struct {
	ID       int64  `json:"id"`
	BookID   int64  `json:"bookId"`
	Type     string `json:"bookType"`
	IsBook   bool   `json:"book"`
	FileName string `json:"fileName"`
}
type Progress struct {
	CFI       string   `json:"cfi"`
	Href      string   `json:"href"`
	Percent   *float64 `json:"percentage"`
	InSection *float64 `json:"contentSourceProgressPercent"`
	TTS       string   `json:"ttsPositionCfi"`
}
type Metadata struct {
	Title       string   `json:"title"`
	Authors     []string `json:"authors"`
	Description string   `json:"description"`
	Language    string   `json:"language"`
}
type Book struct {
	ID            int64           `json:"id"`
	Title         string          `json:"title"`
	Primary       *File           `json:"primaryFile"`
	Alternatives  []File          `json:"alternativeFormats"`
	Supplementary []File          `json:"supplementaryFiles"`
	Metadata      Metadata        `json:"metadata"`
	LastRead      *time.Time      `json:"lastReadTime"`
	Added         *time.Time      `json:"addedOn"`
	Status        string          `json:"readStatus"`
	Finished      *time.Time      `json:"dateFinished"`
	EPUB          *Progress       `json:"epubProgress"`
	PDF           json.RawMessage `json:"pdfProgress"`
	CBX           json.RawMessage `json:"cbxProgress"`
	Audio         json.RawMessage `json:"audiobookProgress"`
	KO            json.RawMessage `json:"koreaderProgress"`
	Kobo          json.RawMessage `json:"koboProgress"`
}
type Page struct {
	Content []Book `json:"content"`
	Page    struct {
		Number     int `json:"number"`
		TotalPages int `json:"totalPages"`
	} `json:"page"`
}

func (b Book) Name() string {
	if b.Metadata.Title != "" {
		return b.Metadata.Title
	}
	return b.Title
}
func (b Book) UniqueEPUB() (File, error) {
	if b.Primary == nil || b.Primary.ID <= 0 || !strings.EqualFold(b.Primary.Type, "EPUB") {
		return File{}, fmt.Errorf("UNSUPPORTED_BOOK_FILES")
	}
	seen := map[int64]bool{b.Primary.ID: true}
	for _, f := range append(append([]File{}, b.Alternatives...), b.Supplementary...) {
		if f.ID == b.Primary.ID {
			continue
		}
		if f.IsBook || f.Type != "" {
			seen[f.ID] = true
		}
	}
	if len(seen) != 1 || b.Primary.BookID != b.ID {
		return File{}, fmt.Errorf("UNSUPPORTED_BOOK_FILES")
	}
	return *b.Primary, nil
}
func (b Book) Conflicting() bool {
	for _, v := range []json.RawMessage{b.PDF, b.CBX, b.Audio, b.KO, b.Kobo} {
		if len(v) > 0 && string(v) != "null" {
			return true
		}
	}
	return false
}
func (b Book) NoHistory() bool {
	return b.EPUB == nil && !b.Conflicting() && b.LastRead == nil && b.Finished == nil && (b.Status == "" || b.Status == "UNSET" || b.Status == "UNREAD")
}

type FileProgress struct {
	FileID    int64    `json:"bookFileId"`
	CFI       *string  `json:"positionData"`
	Href      string   `json:"positionHref"`
	Percent   float64  `json:"progressPercent"`
	InSection *float64 `json:"contentSourceProgressPercent"`
	TTS       *string  `json:"ttsPositionCfi"`
}
type WriteProgress struct {
	BookID int64        `json:"bookId"`
	File   FileProgress `json:"fileProgress"`
}
type Error struct{ Status int }

func (e *Error) Error() string { return fmt.Sprintf("Grimmory HTTP %d", e.Status) }

type Client struct {
	Base string
	HTTP *http.Client
}

func New(base string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) Raw(ctx context.Context, token, method, path string, body any) (*http.Response, error) {
	var in io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		in = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, in)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.HTTP.Do(req)
}
func Decode(resp *http.Response, out any) error {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return &Error{resp.StatusCode}
	}
	if out == nil {
		_, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return err
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}
func (c *Client) Login(ctx context.Context, username, password string) (Tokens, error) {
	var t Tokens
	r, err := c.Raw(ctx, "", "POST", "/api/v1/auth/login", map[string]string{"username": username, "password": password})
	if err != nil {
		return t, err
	}
	err = Decode(r, &t)
	if err == nil && (t.Access == "" || t.Refresh == "" || t.Expires <= 0) {
		err = fmt.Errorf("invalid authentication response")
	}
	return t, err
}
func (c *Client) Refresh(ctx context.Context, refresh string) (Tokens, error) {
	var t Tokens
	r, err := c.Raw(ctx, "", "POST", "/api/v1/auth/refresh", map[string]string{"refreshToken": refresh})
	if err != nil {
		return t, err
	}
	err = Decode(r, &t)
	if err == nil && (t.Access == "" || t.Refresh == "" || t.Expires <= 0) {
		err = fmt.Errorf("invalid refresh response")
	}
	return t, err
}
