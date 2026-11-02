// Package googledrive implements Drive v3 binary uploads without conversion.
package googledrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const api = "https://www.googleapis.com/drive/v3/"
const fields = "id,name,mimeType,parents,size,sha256Checksum,modifiedTime,trashed,appProperties"
const Folder = "application/vnd.google-apps.folder"

type File struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	MIME       string            `json:"mimeType,omitempty"`
	Parents    []string          `json:"parents,omitempty"`
	Size       int64             `json:"size,string,omitempty"`
	SHA256     string            `json:"sha256Checksum,omitempty"`
	Modified   string            `json:"modifiedTime,omitempty"`
	Trashed    bool              `json:"trashed,omitempty"`
	Properties map[string]string `json:"appProperties,omitempty"`
}
type About struct {
	Storage struct {
		Limit int64 `json:"limit,string"`
		Usage int64 `json:"usage,string"`
	} `json:"storageQuota"`
	User struct {
		Email string `json:"emailAddress"`
	} `json:"user"`
}
type Error struct {
	Code    int
	Reason  string
	RetryAt time.Time
}

func (e *Error) Error() string { return fmt.Sprintf("Google Drive HTTP %d (%s)", e.Code, e.Reason) }
func (e *Error) Temporary() bool {
	return e.Code == 0 || e.Code == 429 || e.Code >= 500 || e.Code == 403 && (e.Reason == "rateLimitExceeded" || e.Reason == "userRateLimitExceeded" || e.Reason == "dailyLimitExceeded")
}

type Client struct{ HTTP *http.Client }

func safe(address string) bool {
	u, e := url.Parse(address)
	return e == nil && u.Scheme == "https" && u.Host == "www.googleapis.com" && u.User == nil && u.Fragment == "" && (strings.HasPrefix(u.Path, "/drive/v3/") || strings.HasPrefix(u.Path, "/upload/drive/v3/"))
}
func (c Client) request(ctx context.Context, method, address string, body io.Reader, headers map[string]string) (*http.Response, error) {
	if c.HTTP == nil || !safe(address) {
		return nil, errors.New("invalid Google Drive client or endpoint")
	}
	req, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, errors.New("invalid Drive request")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	h := *c.HTTP
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := h.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &Error{Reason: "network interruption"}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 || resp.StatusCode == 308 {
		return resp, nil
	}
	defer resp.Body.Close()
	var wire struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&wire)
	reason := "request rejected"
	if len(wire.Error.Errors) > 0 {
		switch r := wire.Error.Errors[0].Reason; r {
		case "rateLimitExceeded", "userRateLimitExceeded", "dailyLimitExceeded", "storageQuotaExceeded", "insufficientPermissions", "notFound":
			reason = r
		}
	}
	e := &Error{Code: resp.StatusCode, Reason: reason}
	if seconds, err := strconv.ParseInt(resp.Header.Get("Retry-After"), 10, 32); err == nil && seconds > 0 {
		e.RetryAt = time.Now().Add(time.Duration(seconds) * time.Second)
	} else if t, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
		e.RetryAt = t
	}
	return nil, e
}
func decode(resp *http.Response, v any) error {
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(v); err != nil {
		return errors.New("invalid Drive response")
	}
	return nil
}
func (c Client) About(ctx context.Context) (About, error) {
	var a About
	r, e := c.request(ctx, "GET", api+"about?fields=storageQuota,user(emailAddress)", nil, nil)
	if e == nil {
		e = decode(r, &a)
	}
	return a, e
}
func (c Client) GenerateID(ctx context.Context) (string, error) {
	r, e := c.request(ctx, "GET", api+"files/generateIds?count=1&space=drive&type=files", nil, nil)
	if e != nil {
		return "", e
	}
	var out struct {
		IDs []string `json:"ids"`
	}
	if e = decode(r, &out); e != nil {
		return "", e
	}
	if len(out.IDs) != 1 || out.IDs[0] == "" {
		return "", errors.New("Drive did not return an ID")
	}
	return out.IDs[0], nil
}
func (c Client) Get(ctx context.Context, id string) (File, error) {
	var f File
	r, e := c.request(ctx, "GET", api+"files/"+url.PathEscape(id)+"?fields="+url.QueryEscape(fields), nil, nil)
	if e == nil {
		e = decode(r, &f)
	}
	if e == nil && f.ID != id {
		return f, errors.New("Drive returned wrong file ID")
	}
	return f, e
}
func (c Client) CreateFolder(ctx context.Context, f File) (File, error) {
	f.MIME = Folder
	b, e := json.Marshal(f)
	if e != nil {
		return File{}, e
	}
	r, e := c.request(ctx, "POST", api+"files?fields="+url.QueryEscape(fields), bytes.NewReader(b), map[string]string{"Content-Type": "application/json"})
	if e != nil {
		return File{}, e
	}
	var out File
	e = decode(r, &out)
	return out, e
}
func (c Client) Start(ctx context.Context, f File, size int64) (string, error) {
	b, e := json.Marshal(f)
	if e != nil {
		return "", e
	}
	r, e := c.request(ctx, "POST", "https://www.googleapis.com/upload/drive/v3/files?uploadType=resumable&fields="+url.QueryEscape(fields), bytes.NewReader(b), map[string]string{"Content-Type": "application/json", "X-Upload-Content-Type": f.MIME, "X-Upload-Content-Length": strconv.FormatInt(size, 10)})
	if e != nil {
		return "", e
	}
	defer r.Body.Close()
	address := r.Header.Get("Location")
	if !safe(address) {
		return "", errors.New("Drive returned untrusted upload session")
	}
	return address, nil
}

// Query returns server-acknowledged bytes or a final file, never assumes sent bytes were accepted.
func (c Client) Query(ctx context.Context, address string, size int64) (int64, *File, error) {
	return c.put(ctx, address, nil, fmt.Sprintf("bytes */%d", size))
}
func (c Client) Chunk(ctx context.Context, address string, offset, size int64, data []byte) (int64, *File, error) {
	if offset < 0 || len(data) == 0 || offset+int64(len(data)) > size {
		return 0, nil, errors.New("invalid Drive chunk")
	}
	return c.put(ctx, address, data, fmt.Sprintf("bytes %d-%d/%d", offset, offset+int64(len(data))-1, size))
}
func (c Client) put(ctx context.Context, address string, data []byte, contentRange string) (int64, *File, error) {
	r, e := c.request(ctx, "PUT", address, bytes.NewReader(data), map[string]string{"Content-Type": "application/octet-stream", "Content-Range": contentRange})
	if e != nil {
		return 0, nil, e
	}
	if r.StatusCode == 308 {
		defer r.Body.Close()
		v := r.Header.Get("Range")
		if v == "" {
			return 0, nil, nil
		}
		if !strings.HasPrefix(v, "bytes=0-") {
			return 0, nil, errors.New("invalid Drive upload range")
		}
		last, e := strconv.ParseInt(strings.TrimPrefix(v, "bytes=0-"), 10, 64)
		if e != nil || last < 0 {
			return 0, nil, errors.New("invalid Drive upload range")
		}
		return last + 1, nil, nil
	}
	var f File
	e = decode(r, &f)
	return f.Size, &f, e
}
