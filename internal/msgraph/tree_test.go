package msgraph

import (
	"context"
	"fmt"
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
		c := Client{Drive: "drive", SkipPackages: true, HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
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

func TestTreeExplicitExclusionReusesCheckpoint(t *testing.T) {
	store, err := journal.Open(filepath.Join(t.TempDir(), "progress.db"), make([]byte, 32), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requests := 0
	c := Client{Drive: "drive", HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		requests++
		return response(`{"value":[{"id":"r","root":{},"folder":{}},{"id":"d","name":"Documents","folder":{},"parentReference":{"id":"r"}},{"id":"n","name":"Notebook","package":{},"folder":{},"parentReference":{"id":"d"}},{"id":"child","name":"section","file":{},"parentReference":{"id":"n"}},{"id":"ok","name":"Notebook2","folder":{},"parentReference":{"id":"d"}}],"@odata.deltaLink":"https://graph.microsoft.com/v1.0/drives/drive/root/delta?done"}`), nil
	})}}
	walk := func() ([]string, error) {
		var paths []string
		err := c.WalkTreeCheckpoint(context.Background(), "/", store, "same-binding", io.Discard, func(n onedrive.Item) error { paths = append(paths, n.Path); return nil })
		return paths, err
	}
	if _, err := walk(); err == nil {
		t.Fatal("unsupported notebook accepted without exclusion")
	}
	c.ExcludedPaths = []string{"Documents/Notebook"}
	got, err := walk()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !strings.Contains(strings.Join(got, "|"), "Documents/Notebook2") {
		t.Fatalf("wrong scope: %v", got)
	}
	if requests != 1 {
		t.Fatalf("downloaded completed checkpoint again: %d", requests)
	}
}

func TestSelectedFolderExclusionsUseFullDrivePath(t *testing.T) {
	c := Client{Drive: "drive", ExcludedPaths: []string{"Documents/Notebook"}, HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/items/") {
			return response(`{"value":[]}`), nil
		}
		return response(`{"value":[{"id":"n","name":"Notebook","package":{}},{"id":"ok","name":"Notebook2","folder":{}}]}`), nil
	})}}
	var paths []string
	err := c.WalkTree(context.Background(), "/Documents", func(n onedrive.Item) error { paths = append(paths, n.Path); return nil })
	if err != nil || len(paths) != 1 || paths[0] != "Notebook2" {
		t.Fatalf("wrong folder scope: %v %v", paths, err)
	}
}

func TestDriveAutoSkipsPackagesButNotOrdinaryData(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		t.Run(fmt.Sprint(checkpoint), func(t *testing.T) {
			store, err := journal.Open(filepath.Join(t.TempDir(), "progress.db"), make([]byte, 32), true)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			c := Client{Drive: "drive", SkipPackages: true, HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/items/") {
					return response(`{"value":[]}`), nil
				}
				entries := `{"id":"n","name":"Notebook","package":{"type":"oneNote"},"parentReference":{"id":"r"}},{"id":"f","name":"report.docx","file":{},"size":5,"eTag":"v","parentReference":{"id":"r"}},{"id":"d","name":"Pictures","folder":{},"specialFolder":{"name":"photos"},"parentReference":{"id":"r"}}`
				if checkpoint {
					entries = `{"id":"r","root":{},"folder":{}},` + entries + `,{"id":"section","name":"section","file":{},"parentReference":{"id":"n"}}`
				}
				return response(`{"value":[` + entries + `],"@odata.deltaLink":"https://graph.microsoft.com/v1.0/drives/drive/root/delta?done"}`), nil
			})}}
			var items []onedrive.Item
			visit := func(n onedrive.Item) error { items = append(items, n); return nil }
			if checkpoint {
				err = c.WalkTreeCheckpoint(context.Background(), "/", store, "auto", io.Discard, visit)
			} else {
				err = c.WalkTree(context.Background(), "/", visit)
			}
			if err != nil {
				t.Fatal(err)
			}
			packages, files, dirs := 0, 0, 0
			for _, n := range items {
				if n.OneDrivePackage {
					packages++
				} else if n.IsDir {
					dirs++
				} else {
					files++
				}
			}
			if packages != 1 || files != 1 || dirs != 1 {
				t.Fatalf("wrong classification: %+v", items)
			}
		})
	}
}
