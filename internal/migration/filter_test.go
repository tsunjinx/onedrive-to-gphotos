package migration

import (
	"context"
	"strings"
	"testing"
)

func TestExcludedFoldersValidationAndBoundaries(t *testing.T) {
	for _, bad := range []string{"/", "Pictures", "/Pictures/../Documents", "/Pictures/./x", "/bad\\path", "/bad\x00name"} {
		if _, err := NormalizeExcludedFolders([]string{bad}); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	folders, err := NormalizeExcludedFolders([]string{"/Documents/Projects/", "/Ảnh riêng", "/K"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		p    string
		want bool
	}{{"documents/PROJECTS/icon.png", true}, {"Documents/Projects2/photo.jpg", false}, {"ẢNH RIÊNG/video.mov", true}, {"Pictures/photo.jpg", false}, {"K/photo.jpg", true}} {
		if got := ExcludedFolder(tc.p, folders); (got != "") != tc.want {
			t.Fatalf("%q matched %q", tc.p, got)
		}
	}
}

func TestDiscoverPersistsExcludedFilesAsSkipped(t *testing.T) {
	e, meta, _, _, close := fixture(t)
	defer close()
	meta.InventoryComplete = false
	meta.ExcludedFolders = []string{"/Private"}
	meta.Folder = "Private"
	if err := e.Discover(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	var item Item
	if err := e.Store.Load(1, &item); err != nil {
		t.Fatal(err)
	}
	if item.State != "skipped" || !strings.Contains(item.Reason, "/Private") {
		t.Fatalf("excluded file eligible: %+v", item)
	}
	var saved Meta
	if err := e.Store.Load(0, &saved); err != nil || len(saved.ExcludedFolders) != 1 {
		t.Fatalf("lost policy: %+v %v", saved, err)
	}
}
