package msgraph

import (
	"context"
	"io"
	"net/http"
	"od2gp/internal/journal"
	"od2gp/internal/onedrive"
	"path/filepath"
	"strings"
	"testing"
)

func TestTreeListsEmptyFoldersAndRejectsMissingSize(t *testing.T) {
	for _, value := range []string{`{"id":"file","name":"empty","size":0,"file":{},"eTag":"v"}`, `{"id":"file","name":"unknown","file":{},"eTag":"v"}`} {
		c := Client{Drive: "drive", HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.Path, "/items/folder/") {
				return response(`{"value":[]}`), nil
			}
			return response(`{"value":[{"id":"folder","name":"folder","folder":{}},` + value + `]}`), nil
		})}}
		dirs, empty := 0, 0
		err := c.WalkTree(context.Background(), "/", func(n onedrive.Item) error {
			if n.IsDir {
				dirs++
			}
			if !n.IsDir && n.Size == 0 {
				empty++
			}
			return nil
		})
		if strings.Contains(value, "unknown") {
			if err == nil {
				t.Fatal("missing size treated as zero")
			}
		} else { // A folder needs its own empty children response.
			if dirs == 0 || empty == 0 {
				t.Fatalf("missing folder/empty file: %d/%d (%v)", dirs, empty, err)
			}
		}
	}
}

func TestRootTreeCheckpointRetainsEmptyFolders(t *testing.T) {
	store, err := journal.Open(filepath.Join(t.TempDir(), "progress.db"), make([]byte, 32), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requests := 0
	c := Client{Drive: "drive", HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		requests++
		return response(`{"value":[{"id":"r","name":"root","root":{},"folder":{}},{"id":"d","name":"rỗng","folder":{},"parentReference":{"id":"r","driveId":"drive"}},{"id":"f","name":"zero","file":{},"size":0,"eTag":"v","parentReference":{"id":"d","driveId":"drive"}}],"@odata.deltaLink":"https://graph.microsoft.com/v1.0/drives/drive/root/delta?token=done"}`), nil
	})}}
	for range 2 {
		var got []onedrive.Item
		if err = c.WalkTreeCheckpoint(context.Background(), "/", store, "tree", io.Discard, func(n onedrive.Item) error { got = append(got, n); return nil }); err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("lost tree: %+v", got)
		}
	}
	if requests != 1 {
		t.Fatal("root checkpoint was not reused")
	}
}
