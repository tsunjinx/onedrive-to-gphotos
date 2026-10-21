package msgraph

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

type pacedBody struct {
	ctx       context.Context
	remaining int
	delay     time.Duration
}

func (b *pacedBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-time.After(b.delay):
	}
	b.remaining--
	p[0] = 'x'
	return 1, nil
}
func (*pacedBody) Close() error { return nil }
func downloadClient(delay time.Duration, count int) Client {
	return Client{Drive: "drive", downloadIdleTimeout: 100 * time.Millisecond, HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		r := response("")
		r.StatusCode = 302
		r.Header.Set("Location", "https://cdn.example/file")
		return r, nil
	})}, downloadTransport: transport(func(r *http.Request) (*http.Response, error) {
		v := response("")
		v.Body = &pacedBody{r.Context(), count, delay}
		return v, nil
	})}
}
func TestDownloadAllowsContinuousProgressBeyondIdleWindow(t *testing.T) {
	c := downloadClient(20*time.Millisecond, 12)
	var out bytes.Buffer
	if err := c.Read(context.Background(), "video.mov", 0, &out); err != nil || out.Len() != 12 {
		t.Fatalf("healthy long stream stopped: %d %v", out.Len(), err)
	}
}
func TestDownloadCancelsStallAndPreservesParentCancellation(t *testing.T) {
	c := downloadClient(time.Second, 1)
	started := time.Now()
	if err := c.Read(context.Background(), "video.mov", 0, io.Discard); err == nil || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("idle download not stopped: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Read(ctx, "video.mov", 0, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancel: %v", err)
	}
}
