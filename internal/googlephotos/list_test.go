package googlephotos

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestListAppMediaContinuesThroughEmptyPageAndRejectsCycle(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		calls := 0
		client := Client{HTTP: &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			body := `{"mediaItems":[],"nextPageToken":"second page"}`
			if calls > 1 {
				if r.URL.Query().Get("pageToken") != "second page" {
					t.Error("bad page token")
				}
				body = `{"mediaItems":[{"id":"one","filename":"photo.jpg","mediaMetadata":{"width":"400","height":"300","creationTime":"2020-01-01T00:00:00Z"}}]}`
				if cycle {
					body = `{"mediaItems":[],"nextPageToken":"second page"}`
				}
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})}}
		found := 0
		err := client.ListAppMedia(context.Background(), func(m Media) error {
			found++
			if m.Metadata.Width != "400" {
				t.Fatal("lost metadata")
			}
			return nil
		})
		if cycle && err == nil {
			t.Fatal("accepted cycle")
		}
		if !cycle && (err != nil || found != 1) {
			t.Fatalf("%v %d", err, found)
		}
	}
}
