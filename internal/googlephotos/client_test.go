package googlephotos

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestConnectionFailuresAreTypedRedactedAndCancellationIsPreserved(t *testing.T) {
	client := Client{HTTP: &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("private-session-url and credential-details")}
	})}}
	_, err := client.Start(context.Background(), "image/jpeg", 1)
	var connection *ConnectionError
	if !errors.As(err, &connection) || strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), "credential-") {
		t.Fatalf("unsafe connection error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Start(ctx, "image/jpeg", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestUnknownTransportFailureIsNotClassifiedAsRetryable(t *testing.T) {
	client := Client{HTTP: &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("private refresh credential rejected")
	})}}
	_, err := client.Start(context.Background(), "image/jpeg", 1)
	var connection *ConnectionError
	if err == nil || errors.As(err, &connection) || strings.Contains(err.Error(), "private") {
		t.Fatalf("unknown failure incorrectly retried or disclosed: %v", err)
	}
}

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestUploadProtocolAndVideoStatus(t *testing.T) {
	step := 0
	client := Client{HTTP: &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
		step++
		headers := make(http.Header)
		body := ""
		switch step {
		case 1:
			if r.Header.Get("X-Goog-Upload-Protocol") != "resumable" || r.Header.Get("X-Goog-Upload-Raw-Size") != "3" {
				t.Error("incorrect session start")
			}
			headers.Set("X-Goog-Upload-URL", "https://photoslibrary.googleapis.com/session")
			headers.Set("X-Goog-Upload-Chunk-Granularity", "1")
		case 2:
			if r.Header.Get("X-Goog-Upload-Command") != "query" {
				t.Error("missing offset query")
			}
			headers.Set("X-Goog-Upload-Status", "active")
			headers.Set("X-Goog-Upload-Size-Received", "1")
		case 3:
			if r.Header.Get("X-Goog-Upload-Command") != "upload, finalize" || r.Header.Get("X-Goog-Upload-Offset") != "1" {
				t.Error("incorrect resumed chunk")
			}
			data, _ := io.ReadAll(r.Body)
			if string(data) != "bc" {
				t.Error("wrong upload bytes")
			}
			body = "upload-token"
		case 4:
			data, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(data), `"fileName":"clip.mp4"`) || strings.Contains(string(data), "description") {
				t.Error("incorrect media creation payload")
			}
			body = `{"newMediaItemResults":[{"status":{},"mediaItem":{"id":"media","mediaMetadata":{"video":{"status":"PROCESSING"}}}}]}`
		case 5:
			body = `{"id":"media","mediaMetadata":{"video":{"status":"READY"}}}`
		default:
			t.Fatal("unexpected extra API call")
		}
		return &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	ctx := context.Background()
	session, err := client.Start(ctx, "video/mp4", 3)
	if err != nil {
		t.Fatal(err)
	}
	offset, state, err := client.Query(ctx, session)
	if err != nil || offset != 1 || state != "active" {
		t.Fatal("query failed")
	}
	token, err := client.Chunk(ctx, session, offset, []byte("bc"), true)
	if err != nil {
		t.Fatal(err)
	}
	media, err := client.Create(ctx, token, "clip.mp4")
	if err != nil || media.ID != "media" {
		t.Fatalf("create failed: %v", err)
	}
	media, err = client.Get(ctx, media.ID)
	if err != nil || media.Metadata.Video.Status != "READY" {
		t.Fatalf("status failed: %v", err)
	}
}
func TestSessionHostAndRateLimit(t *testing.T) {
	for _, address := range []string{"https://evil.example/session", "http://photoslibrary.googleapis.com/session", "https://photoslibrary.googleapis.com@evil.example/session"} {
		if validSession(address) == nil {
			t.Fatal("unsafe session accepted")
		}
	}
	client := Client{HTTP: &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"90"}}, Body: io.NopCloser(strings.NewReader("private error details"))}, nil
	})}}
	_, err := client.Start(context.Background(), "image/jpeg", 1)
	api, ok := err.(*APIError)
	if !ok || time.Until(api.RetryAt) < 89*time.Second || strings.Contains(err.Error(), "private") {
		t.Fatalf("rate limit mishandled: %v", err)
	}
}

type brokenResponseBody struct{}

func (brokenResponseBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (brokenResponseBody) Close() error             { return nil }

func TestRetryAfterSurvivesBrokenErrorBodyAndLargeValues(t *testing.T) {
	for _, header := range []string{"120", "20000000000", "9999999999999999999999999999", time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat)} {
		t.Run(header, func(t *testing.T) {
			client := Client{HTTP: &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{header}}, Body: brokenResponseBody{}}, nil
			})}}
			_, err := client.Start(context.Background(), "image/jpeg", 1)
			var api *APIError
			if !errors.As(err, &api) || api.Code != 429 || time.Until(api.RetryAt) < 118*time.Second {
				t.Fatalf("lost or shortened provider pause: %v", err)
			}
		})
	}
}
