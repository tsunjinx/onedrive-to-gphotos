// Package googlephotos implements the Photos Library resumable upload protocol.
package googlephotos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const base = "https://photoslibrary.googleapis.com/v1/"

type Client struct{ HTTP *http.Client }
type Session struct {
	URL         string
	Granularity int64
	Created     time.Time
}
type Media struct {
	ID       string `json:"id"`
	Metadata struct {
		Video *struct {
			Status string `json:"status"`
		} `json:"video"`
	} `json:"mediaMetadata"`
}
type APIError struct {
	Code      int
	Operation string
	RetryAt   time.Time
}

// ConnectionError deliberately excludes request URLs and provider details.
type ConnectionError struct{}

func (*ConnectionError) Error() string {
	return "Google Photos connection interrupted; progress is saved"
}

func (e *APIError) Error() string {
	operation := "request"
	switch e.Operation {
	case "upload start", "upload chunk", "upload query", "media creation", "media status":
		operation = e.Operation
	}
	category := ""
	if e.Code == 429 {
		category = "; rate limit or quota"
	} else if e.Code >= 500 {
		category = "; server error"
	}
	return fmt.Sprintf("Google Photos %s failed (HTTP %d%s); progress is saved", operation, e.Code, category)
}
func (c Client) request(ctx context.Context, method, address string, headers map[string]string, body []byte) (http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if err != nil {
		return nil, nil, errors.New("invalid Google Photos request")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		var network *net.OpError
		if errors.As(err, &network) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) {
			return nil, nil, &ConnectionError{}
		}
		return nil, nil, errors.New("Google Photos request failed; check sign-in and connectivity")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		when := time.Now().Add(30 * time.Second)
		if seconds, err := strconv.ParseUint(strings.TrimSpace(resp.Header.Get("Retry-After")), 10, 64); err == nil || errors.Is(err, strconv.ErrRange) {
			// Saturate very large values instead of overflowing into a short pause.
			seconds = min(seconds, uint64((1<<63-1)/int64(time.Second)))
			when = time.Now().Add(max(time.Duration(seconds)*time.Second, 30*time.Second))
		} else if date, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil && date.After(when) {
			when = date
		}
		operation := "upload chunk"
		switch {
		case address == base+"uploads":
			operation = "upload start"
		case address == base+"mediaItems:batchCreate":
			operation = "media creation"
		case method == "GET":
			operation = "media status"
		case headers["X-Goog-Upload-Command"] == "query":
			operation = "upload query"
		}
		return resp.Header, nil, &APIError{Code: resp.StatusCode, RetryAt: when, Operation: operation}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, &ConnectionError{}
	}
	return resp.Header, data, nil
}
func (c Client) Start(ctx context.Context, mime string, size int64) (Session, error) {
	headers, _, err := c.request(ctx, "POST", base+"uploads", map[string]string{"X-Goog-Upload-Protocol": "resumable", "X-Goog-Upload-Command": "start", "X-Goog-Upload-Content-Type": mime, "X-Goog-Upload-Raw-Size": strconv.FormatInt(size, 10)}, nil)
	if err != nil {
		return Session{}, err
	}
	address := headers.Get("X-Goog-Upload-URL")
	if err := validSession(address); err != nil {
		return Session{}, err
	}
	granularity, err := strconv.ParseInt(headers.Get("X-Goog-Upload-Chunk-Granularity"), 10, 64)
	if err != nil || granularity <= 0 || granularity > 64<<20 {
		return Session{}, errors.New("Google returned unsupported chunk granularity")
	}
	return Session{URL: address, Granularity: granularity, Created: time.Now().UTC()}, nil
}
func validSession(address string) error {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "photoslibrary.googleapis.com" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("unexpected Google upload session host")
	}
	return nil
}
func (c Client) Query(ctx context.Context, s Session) (int64, string, error) {
	if err := validSession(s.URL); err != nil {
		return 0, "", err
	}
	headers, _, err := c.request(ctx, "POST", s.URL, map[string]string{"X-Goog-Upload-Command": "query"}, nil)
	if err != nil {
		return 0, "", err
	}
	offset, err := strconv.ParseInt(headers.Get("X-Goog-Upload-Size-Received"), 10, 64)
	if err != nil && headers.Get("X-Goog-Upload-Status") == "active" {
		return 0, "", errors.New("Google upload progress is missing")
	}
	return offset, headers.Get("X-Goog-Upload-Status"), nil
}
func (c Client) Chunk(ctx context.Context, s Session, offset int64, data []byte, final bool) (string, error) {
	if err := validSession(s.URL); err != nil {
		return "", err
	}
	command := "upload"
	if final {
		command = "upload, finalize"
	}
	_, body, err := c.request(ctx, "POST", s.URL, map[string]string{"Content-Type": "application/octet-stream", "X-Goog-Upload-Command": command, "X-Goog-Upload-Offset": strconv.FormatInt(offset, 10)}, data)
	if err != nil {
		return "", err
	}
	if !final {
		return "", nil
	}
	token := strings.TrimSpace(string(body))
	if token == "" || len(token) > 65536 {
		return "", errors.New("Google upload token missing")
	}
	return token, nil
}
func (c Client) Create(ctx context.Context, token, name string) (Media, error) {
	results, err := c.CreateBatch(ctx, []NewItem{{Token: token, Name: name}})
	if err != nil {
		return Media{}, err
	}
	return results[0].Media, results[0].Err
}
func (c Client) Get(ctx context.Context, id string) (Media, error) {
	_, body, err := c.request(ctx, "GET", base+"mediaItems/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return Media{}, err
	}
	var media Media
	if json.Unmarshal(body, &media) != nil || media.ID != id {
		return Media{}, errors.New("invalid Google media status response")
	}
	return media, nil
}
