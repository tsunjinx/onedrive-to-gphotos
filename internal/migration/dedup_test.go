package migration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"od2gp/internal/journal"
	"od2gp/internal/onedrive"
)

type mockMultiSource struct {
	items []onedrive.Item
}

func (m *mockMultiSource) DriveID(context.Context) (string, error) {
	return "drive-main", nil
}

func (m *mockMultiSource) Walk(ctx context.Context, folder string, visit func(onedrive.Item) error) error {
	for _, item := range m.items {
		if err := visit(item); err != nil {
			return err
		}
	}
	return nil
}

func (m *mockMultiSource) Stat(context.Context, string) (onedrive.Item, error) {
	return onedrive.Item{}, nil
}

func (m *mockMultiSource) Read(context.Context, string, int64, io.Writer) error {
	return nil
}

func TestCrossJobDeduplicationInDiscover(t *testing.T) {
	jobsDir := t.TempDir()
	id1 := strings.Repeat("1", 32)
	id2 := strings.Repeat("2", 32)

	key1 := bytes.Repeat([]byte{1}, 32)
	key2 := bytes.Repeat([]byte{2}, 32)

	// Setup Job 1: completed earlier with photo1.jpg and photo2.jpg
	job1Dir := filepath.Join(jobsDir, id1)
	store1, err := journal.Open(filepath.Join(job1Dir, "progress.db"), key1, true)
	if err != nil {
		t.Fatal(err)
	}
	meta1 := &Meta{ID: id1, Folder: "Pictures", DriveID: "drive-main", GoogleSubject: "user@test.com", InventoryComplete: true}
	if err := store1.Save(0, "job", meta1); err != nil {
		t.Fatal(err)
	}
	item1 := Item{
		Source:  onedrive.Item{ID: "src-1", Path: "photo1.jpg", Name: "photo1.jpg", Size: 1000, ModTime: "2023-01-01T00:00:00Z"},
		Path:    "Pictures/photo1.jpg",
		State:   "completed",
		MediaID: "gphotos-1",
	}
	item2 := Item{
		Source:  onedrive.Item{ID: "src-2", Path: "photo2.jpg", Name: "photo2.jpg", Size: 2000, ModTime: "2023-01-01T00:00:00Z"},
		Path:    "Pictures/photo2.jpg",
		State:   "completed",
		MediaID: "gphotos-2",
	}
	if err := store1.Save(1, item1.State, &item1); err != nil {
		t.Fatal(err)
	}
	if err := store1.Save(2, item2.State, &item2); err != nil {
		t.Fatal(err)
	}
	store1.Close()

	// Setup Job 2: scanning drive root ("")
	job2Dir := filepath.Join(jobsDir, id2)
	store2, err := journal.Open(filepath.Join(job2Dir, "progress.db"), key2, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()

	meta2 := &Meta{ID: id2, Folder: "", DriveID: "drive-main", GoogleSubject: "user@test.com", CacheBytes: 1 << 30}
	if err := store2.Save(0, "job", meta2); err != nil {
		t.Fatal(err)
	}

	source := &mockMultiSource{
		items: []onedrive.Item{
			// Unchanged photo1: exact ID, path, size, modTime -> should be skipped!
			{ID: "src-1", Path: "Pictures/photo1.jpg", Name: "photo1.jpg", Size: 1000, ModTime: "2023-01-01T00:00:00Z"},
			// Modified photo2: size changed to 2500 -> should NOT be skipped!
			{ID: "src-2", Path: "Pictures/photo2.jpg", Name: "photo2.jpg", Size: 2500, ModTime: "2023-01-02T00:00:00Z"},
			// New photo3 -> should NOT be skipped!
			{ID: "src-3", Path: "Pictures/photo3.jpg", Name: "photo3.jpg", Size: 3000, ModTime: "2023-01-03T00:00:00Z"},
		},
	}

	keyResolver := func(id string) ([]byte, error) {
		switch id {
		case id1:
			return key1, nil
		case id2:
			return key2, nil
		default:
			return nil, fmt.Errorf("unknown job %s", id)
		}
	}

	var progressBuf bytes.Buffer
	engine := &Engine{
		Store:         store2,
		Source:        source,
		Directory:     job2Dir,
		JobsDirectory: jobsDir,
		KeyResolver:   keyResolver,
		Progress:      &progressBuf,
	}

	if err := engine.Discover(context.Background(), meta2); err != nil {
		t.Fatalf("Discover failed: %v", err)
	}

	if !strings.Contains(progressBuf.String(), "Cross-job deduplication: loaded 2 completed files") {
		t.Fatalf("expected cross-job dedup log, got:\n%s", progressBuf.String())
	}

	// Verify items in Job 2
	var r1, r2, r3 Item
	if err := store2.Load(1, &r1); err != nil {
		t.Fatal(err)
	}
	if err := store2.Load(2, &r2); err != nil {
		t.Fatal(err)
	}
	if err := store2.Load(3, &r3); err != nil {
		t.Fatal(err)
	}

	if r1.State != "skipped" || !strings.Contains(r1.Reason, id1) {
		t.Fatalf("expected r1 to be skipped with reason referencing job %s, got state=%q reason=%q", id1, r1.State, r1.Reason)
	}
	if r2.State != "discovered" {
		t.Fatalf("expected modified r2 to be discovered, got state=%q", r2.State)
	}
	if r3.State != "discovered" {
		t.Fatalf("expected new r3 to be discovered, got state=%q", r3.State)
	}
}

func TestCrossJobDeduplicationInRun(t *testing.T) {
	jobsDir := t.TempDir()
	id1 := strings.Repeat("a", 32)
	id2 := strings.Repeat("b", 32)

	key1 := bytes.Repeat([]byte{0xaa}, 32)
	key2 := bytes.Repeat([]byte{0xbb}, 32)

	// Setup Job 1: completed
	job1Dir := filepath.Join(jobsDir, id1)
	store1, err := journal.Open(filepath.Join(job1Dir, "progress.db"), key1, true)
	if err != nil {
		t.Fatal(err)
	}
	meta1 := &Meta{ID: id1, Folder: "Pictures", DriveID: "drive-main", GoogleSubject: "user@test.com", InventoryComplete: true}
	if err := store1.Save(0, "job", meta1); err != nil {
		t.Fatal(err)
	}
	item1 := Item{
		Source:  onedrive.Item{ID: "src-1", Path: "photo1.jpg", Name: "photo1.jpg", Size: 1000, ModTime: "2023-01-01T00:00:00Z"},
		Path:    "Pictures/photo1.jpg",
		State:   "completed",
		MediaID: "gphotos-1",
	}
	if err := store1.Save(1, item1.State, &item1); err != nil {
		t.Fatal(err)
	}
	store1.Close()

	// Setup Job 2: already planned previously (state=discovered and state=staged)
	job2Dir := filepath.Join(jobsDir, id2)
	store2, err := journal.Open(filepath.Join(job2Dir, "progress.db"), key2, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()

	meta2 := &Meta{ID: id2, Folder: "", DriveID: "drive-main", GoogleSubject: "user@test.com", CacheBytes: 1 << 30, InventoryComplete: true}
	if err := store2.Save(0, "job", meta2); err != nil {
		t.Fatal(err)
	}

	// Create a dummy staged file for item 1
	stagedPath := filepath.Join(job2Dir, "stage-1.bin")
	if err := os.WriteFile(stagedPath, []byte("staged-data"), 0600); err != nil {
		t.Fatal(err)
	}

	item2 := Item{
		Source: onedrive.Item{ID: "src-1", Path: "Pictures/photo1.jpg", Name: "photo1.jpg", Size: 1000, ModTime: "2023-01-01T00:00:00Z"},
		Path:   "Pictures/photo1.jpg",
		State:  "staged",
	}
	if err := store2.Save(1, item2.State, &item2); err != nil {
		t.Fatal(err)
	}

	photos := &photosFake{}
	source := &sourceFake{drive: "drive-main"}
	engine := &Engine{
		Store:         store2,
		Source:        source,
		Photos:        photos,
		Directory:     job2Dir,
		JobsDirectory: jobsDir,
		KeyResolver: func(id string) ([]byte, error) {
			if id == id1 {
				return key1, nil
			}
			return key2, nil
		},
		Progress: io.Discard,
	}

	if err := engine.Run(context.Background(), meta2); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	var updated Item
	if err := store2.Load(1, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.State != "skipped" || !strings.Contains(updated.Reason, id1) {
		t.Fatalf("expected item to be transitioned to skipped, got state=%q reason=%q", updated.State, updated.Reason)
	}
	// Verify staged file was removed
	if _, err := os.Stat(stagedPath); !os.IsNotExist(err) {
		t.Fatal("expected staged file to be cleaned up")
	}
	// Verify photos fake was never called to upload or create media
	if photos.creates > 0 || photos.starts > 0 {
		t.Fatalf("unexpected upload/create initiated: starts=%d creates=%d", photos.starts, photos.creates)
	}
}

func TestCrossJobDeduplicationDriveAndSubjectIsolation(t *testing.T) {
	jobsDir := t.TempDir()
	id1 := strings.Repeat("c", 32)
	id2 := strings.Repeat("d", 32)

	key1 := bytes.Repeat([]byte{0xcc}, 32)
	key2 := bytes.Repeat([]byte{0xdd}, 32)

	// Setup Job 1: completed on drive-A
	job1Dir := filepath.Join(jobsDir, id1)
	store1, err := journal.Open(filepath.Join(job1Dir, "progress.db"), key1, true)
	if err != nil {
		t.Fatal(err)
	}
	meta1 := &Meta{ID: id1, Folder: "Pictures", DriveID: "drive-A", GoogleSubject: "user@test.com", InventoryComplete: true}
	if err := store1.Save(0, "job", meta1); err != nil {
		t.Fatal(err)
	}
	item1 := Item{
		Source:  onedrive.Item{ID: "src-1", Path: "photo1.jpg", Size: 1000},
		Path:    "Pictures/photo1.jpg",
		State:   "completed",
		MediaID: "gphotos-1",
	}
	if err := store1.Save(1, item1.State, &item1); err != nil {
		t.Fatal(err)
	}
	store1.Close()

	// Setup Job 2: planned on drive-main (different drive from Job 1's drive-A!)
	job2Dir := filepath.Join(jobsDir, id2)
	store2, err := journal.Open(filepath.Join(job2Dir, "progress.db"), key2, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()

	meta2 := &Meta{ID: id2, Folder: "", DriveID: "drive-main", GoogleSubject: "user@test.com", CacheBytes: 1 << 30}
	if err := store2.Save(0, "job", meta2); err != nil {
		t.Fatal(err)
	}

	source := &mockMultiSource{
		items: []onedrive.Item{
			{ID: "src-1", Path: "Pictures/photo1.jpg", Name: "photo1.jpg", Size: 1000},
		},
	}

	engine := &Engine{
		Store:         store2,
		Source:        source,
		Directory:     job2Dir,
		JobsDirectory: jobsDir,
		KeyResolver: func(id string) ([]byte, error) {
			if id == id1 {
				return key1, nil
			}
			return key2, nil
		},
		Progress: io.Discard,
	}

	if err := engine.Discover(context.Background(), meta2); err != nil {
		t.Fatalf("Discover failed: %v", err)
	}

	var r1 Item
	if err := store2.Load(1, &r1); err != nil {
		t.Fatal(err)
	}
	if r1.State != "discovered" {
		t.Fatalf("expected r1 to remain discovered for different drive, got %s", r1.State)
	}
}

func TestDisableCrossJobDedup(t *testing.T) {
	jobsDir := t.TempDir()
	id1 := strings.Repeat("e", 32)
	id2 := strings.Repeat("f", 32)

	key1 := bytes.Repeat([]byte{0xee}, 32)
	key2 := bytes.Repeat([]byte{0xff}, 32)

	// Setup Job 1: completed
	job1Dir := filepath.Join(jobsDir, id1)
	store1, err := journal.Open(filepath.Join(job1Dir, "progress.db"), key1, true)
	if err != nil {
		t.Fatal(err)
	}
	meta1 := &Meta{ID: id1, Folder: "Pictures", DriveID: "drive-main", GoogleSubject: "user@test.com", InventoryComplete: true}
	if err := store1.Save(0, "job", meta1); err != nil {
		t.Fatal(err)
	}
	item1 := Item{
		Source:  onedrive.Item{ID: "src-1", Path: "photo1.jpg", Size: 1000},
		Path:    "Pictures/photo1.jpg",
		State:   "completed",
		MediaID: "gphotos-1",
	}
	if err := store1.Save(1, item1.State, &item1); err != nil {
		t.Fatal(err)
	}
	store1.Close()

	job2Dir := filepath.Join(jobsDir, id2)
	store2, err := journal.Open(filepath.Join(job2Dir, "progress.db"), key2, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()

	meta2 := &Meta{ID: id2, Folder: "", DriveID: "drive-main", GoogleSubject: "user@test.com", CacheBytes: 1 << 30}
	if err := store2.Save(0, "job", meta2); err != nil {
		t.Fatal(err)
	}

	source := &mockMultiSource{
		items: []onedrive.Item{
			{ID: "src-1", Path: "Pictures/photo1.jpg", Name: "photo1.jpg", Size: 1000},
		},
	}

	engine := &Engine{
		Store:                store2,
		Source:               source,
		Directory:            job2Dir,
		JobsDirectory:        jobsDir,
		DisableCrossJobDedup: true, // Explicitly disabled
		KeyResolver: func(id string) ([]byte, error) {
			if id == id1 {
				return key1, nil
			}
			return key2, nil
		},
		Progress: io.Discard,
	}

	if err := engine.Discover(context.Background(), meta2); err != nil {
		t.Fatalf("Discover failed: %v", err)
	}

	var r1 Item
	if err := store2.Load(1, &r1); err != nil {
		t.Fatal(err)
	}
	if r1.State != "discovered" {
		t.Fatalf("expected r1 to be discovered when deduplication is disabled, got %s", r1.State)
	}
}

func TestCompletedMatchRequiresIdentityAndVersionEvidence(t *testing.T) {
	ref := CompletedRef{SourceID: "source", MediaID: "media", Size: 3, ModTime: "time"}
	src := onedrive.Item{ID: "source", Size: 3, ModTime: "time"}
	idx := NewCompletedIndex()
	idx.byID["source"] = ref
	idx.byPath["file"] = ref
	if _, ok := idx.Match(src, "file"); !ok {
		t.Fatal("valid history rejected")
	}
	src.ID = "different"
	if _, ok := idx.Match(src, "file"); ok {
		t.Fatal("path bypassed source identity")
	}
	src.ID = "source"
	src.ModTime = ""
	if _, ok := idx.Match(src, "file"); ok {
		t.Fatal("missing version treated as match")
	}
	ref.MediaID = ""
	src.ModTime = "time"
	if matchesRef(ref, src) {
		t.Fatal("missing destination ID treated as completed")
	}
}
