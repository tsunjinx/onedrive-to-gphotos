package migration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"od2gp/internal/msgraph"
)

type deltaTransport func(*http.Request) (*http.Response, error)

func (f deltaTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDiscoverDeltaResumePreservesIncompleteGate(t *testing.T) {
	e, meta, _, photos, closeStore := fixture(t)
	defer closeStore()
	meta.InventoryComplete = false
	meta.Folder = "/"
	meta.SourceBackend = "graph"
	var output bytes.Buffer
	e.Progress = &output
	calls := 0
	resume := false
	e.Source = &msgraph.Client{Drive: meta.DriveID, HTTP: &http.Client{Transport: deltaTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		status := 200
		body := `{"value":[{"id":"root","root":{},"folder":{}}],"@odata.nextLink":"https://graph.microsoft.com/v1.0/drives/drive/root/delta?page=2"}`
		if strings.Contains(r.URL.RawQuery, "page=2") {
			if !resume {
				status = 403
				body = ""
			} else {
				body = `{"value":[{"id":"p","name":"photo.jpg","size":12,"file":{"mimeType":"image/jpeg"},"eTag":"v1","parentReference":{"id":"root"}}],"@odata.deltaLink":"https://graph.microsoft.com/v1.0/drives/drive/root/delta?token=end"}`
			}
		} else if resume {
			t.Error("restarted initial page")
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	if err := e.Discover(context.Background(), meta); err == nil || meta.InventoryComplete {
		t.Fatalf("partial inventory accepted: %v", err)
	}
	if photos.starts != 0 || photos.creates != 0 {
		t.Fatal("scan uploaded media")
	}
	resume = true
	meta.CacheBytes *= 2
	if err := e.Discover(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if !meta.InventoryComplete || calls != 3 {
		t.Fatalf("resume failed: complete=%v calls=%d", meta.InventoryComplete, calls)
	}
	var item Item
	if err := e.Store.Load(1, &item); err != nil || item.Path != "photo.jpg" {
		t.Fatalf("resolved item: %+v %v", item, err)
	}
	if err := e.Discover(context.Background(), meta); err != nil || calls != 3 {
		t.Fatalf("completed inventory rescanned: %v", err)
	}
}
