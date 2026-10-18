package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"od2gp/internal/googlephotos"
	"od2gp/internal/journal"
	"od2gp/internal/onedrive"
)

type sourceFake struct {
	item  onedrive.Item
	data  []byte
	reads int
	drive string
}

func (s *sourceFake) DriveID(context.Context) (string, error) { return s.drive, nil }
func (s *sourceFake) Walk(_ context.Context, _ string, visit func(onedrive.Item) error) error {
	return visit(s.item)
}
func (s *sourceFake) Stat(context.Context, string) (onedrive.Item, error) { return s.item, nil }
func (s *sourceFake) Read(_ context.Context, _ string, offset int64, w io.Writer) error {
	s.reads++
	_, err := w.Write(s.data[offset:])
	return err
}

type photosFake struct {
	offset                   int64
	queries, creates, starts int
	loseChunk, loseCreate    bool
	ready                    bool
}

func (p *photosFake) Start(context.Context, string, int64) (googlephotos.Session, error) {
	p.starts++
	p.offset = 0
	return googlephotos.Session{URL: "session", Granularity: 1, Created: time.Now()}, nil
}
func (p *photosFake) Query(context.Context, googlephotos.Session) (int64, string, error) {
	p.queries++
	return p.offset, "active", nil
}
func (p *photosFake) Chunk(_ context.Context, _ googlephotos.Session, offset int64, data []byte, final bool) (string, error) {
	if offset != p.offset {
		return "", errors.New("wrong remote offset")
	}
	p.offset += int64(len(data))
	if p.loseChunk {
		p.loseChunk = false
		return "", errors.New("response lost after server accepted bytes")
	}
	if final {
		return "token", nil
	}
	return "", nil
}
func (p *photosFake) Create(context.Context, string, string) (googlephotos.Media, error) {
	p.creates++
	if p.loseCreate {
		return googlephotos.Media{}, errors.New("creation response lost")
	}
	return googlephotos.Media{ID: "created-id"}, nil
}
func (p *photosFake) Get(context.Context, string) (googlephotos.Media, error) {
	raw := `{"id":"created-id","mediaMetadata":{"video":{"status":"PROCESSING"}}}`
	if p.ready {
		raw = `{"id":"created-id","mediaMetadata":{"video":{"status":"READY"}}}`
	}
	var media googlephotos.Media
	err := json.Unmarshal([]byte(raw), &media)
	return media, err
}
func fixture(t *testing.T) (*Engine, *Meta, *sourceFake, *photosFake, func()) {
	t.Helper()
	directory := t.TempDir()
	key := bytes.Repeat([]byte{4}, 32)
	store, err := journal.Open(filepath.Join(directory, "job.db"), key, true)
	if err != nil {
		t.Fatal(err)
	}
	source := &sourceFake{drive: "drive", data: []byte("original media"), item: onedrive.Item{ID: "source-id", Path: "photo.jpg", Name: "photo.jpg", Size: 14, ModTime: "original-version", Hashes: map[string]string{"hash": "original"}}}
	photos := &photosFake{}
	engine := &Engine{Store: store, Source: source, Photos: photos, Directory: directory, Progress: io.Discard}
	meta := &Meta{ID: "job", DriveID: "drive", CacheBytes: 1 << 30}
	if err := store.Save(0, "job", meta); err != nil {
		t.Fatal(err)
	}
	if err := engine.Discover(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	return engine, meta, source, photos, func() { store.Close() }
}
func TestResumeUsesServerOffsetAndSkipsCompletedFiles(t *testing.T) {
	e, meta, source, photos, closeStore := fixture(t)
	defer closeStore()
	photos.loseChunk = true
	if err := e.Run(context.Background(), meta); err == nil {
		t.Fatal("lost response ignored")
	}
	var item Item
	if err := e.Store.Load(1, &item); err != nil {
		t.Fatal(err)
	}
	if item.State != "uploading" || item.Offset != 0 {
		t.Fatalf("unsafe checkpoint: %+v", item)
	}
	// Close/reopen the database: recovery cannot rely on old process memory.
	path := filepath.Join(e.Directory, "job.db")
	key := e.Store.Key
	e.Store.Close()
	reopened, err := journal.Open(path, key, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	e.Store = reopened
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if source.reads != 1 || photos.queries != 1 || photos.creates != 1 {
		t.Fatalf("resumed incorrectly: reads=%d queries=%d creates=%d", source.reads, photos.queries, photos.creates)
	}
	if _, err := os.Stat(filepath.Join(e.Directory, "stage-1.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("completed staging not cleaned")
	}
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if photos.creates != 1 || source.reads != 1 {
		t.Fatal("completed file retransferred")
	}
}

func TestInvalidSavedTransferStateIsRefused(t *testing.T) {
	for _, invalid := range []string{"unknown-state", "zero-granularity"} {
		t.Run(invalid, func(t *testing.T) {
			e, meta, _, photos, closeStore := fixture(t)
			defer closeStore()
			photos.loseChunk = true
			if err := e.Run(context.Background(), meta); err == nil {
				t.Fatal("expected interrupted upload")
			}
			var item Item
			if err := e.Store.Load(1, &item); err != nil {
				t.Fatal(err)
			}
			if invalid == "unknown-state" {
				item.State = "unknown"
			} else {
				item.Upload.Granularity = 0
			}
			if err := e.Store.Save(1, item.State, item); err != nil {
				t.Fatal(err)
			}
			if err := e.Run(context.Background(), meta); err == nil {
				t.Fatal("invalid saved transfer silently accepted")
			}
			if photos.creates != 0 {
				t.Fatal("created media from invalid saved state")
			}
		})
	}
}
func TestLostCreationResponseRemainsExplicitlyUncertain(t *testing.T) {
	e, meta, _, photos, closeStore := fixture(t)
	defer closeStore()
	photos.loseCreate = true
	if err := e.Run(context.Background(), meta); err == nil {
		t.Fatal("lost create response ignored")
	}
	if err := e.Run(context.Background(), meta); err == nil {
		t.Fatal("uncertain creation silently retried")
	}
	if photos.creates != 1 {
		t.Fatal("blindly duplicated creation")
	}
	var item Item
	e.Store.Load(1, &item)
	if item.State != "creating" {
		t.Fatal("uncertain item marked successful")
	}
}
func TestVideoPendingResumesProcessingWithoutReupload(t *testing.T) {
	e, meta, _, photos, closeStore := fixture(t)
	defer closeStore()
	var item Item
	e.Store.Load(1, &item)
	item.Video = true
	e.Store.Save(1, item.State, item)
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	e.Store.Load(1, &item)
	if item.State != "processing" {
		t.Fatalf("video marked ready prematurely: %s", item.State)
	}
	photos.ready = true
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	e.Store.Load(1, &item)
	if item.State != "completed" || photos.creates != 1 {
		t.Fatal("video processing resume uploaded again")
	}
}
func TestChangedSourceRefusesResume(t *testing.T) {
	e, meta, source, _, closeStore := fixture(t)
	defer closeStore()
	source.item.ModTime = "edited"
	if err := e.Run(context.Background(), meta); err == nil {
		t.Fatal("source edit was ignored")
	}
	if source.reads != 0 {
		t.Fatal("edited source downloaded under old identity")
	}
}

func TestSwitchingDrivesPreservesTheOriginalUploadCheckpoint(t *testing.T) {
	e, meta, source, photos, closeStore := fixture(t)
	defer closeStore()
	photos.loseChunk = true
	if err := e.Run(context.Background(), meta); err == nil {
		t.Fatal("expected interrupted upload")
	}
	var before Item
	if err := e.Store.Load(1, &before); err != nil {
		t.Fatal(err)
	}
	source.drive = "another-drive"
	if err := e.Run(context.Background(), meta); err == nil {
		t.Fatal("resumed against the wrong drive")
	}
	var after Item
	if err := e.Store.Load(1, &after); err != nil {
		t.Fatal(err)
	}
	if after.State != before.State || after.Upload.URL != before.Upload.URL || after.Offset != before.Offset || source.reads != 1 || photos.queries != 0 || photos.creates != 0 {
		t.Fatal("changing drives modified or continued the original transfer")
	}
	source.drive = meta.DriveID
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if source.reads != 1 || photos.queries != 1 || photos.creates != 1 {
		t.Fatal("original drive could not resume its checkpoint")
	}
}

func TestMissingStageAfterRebootIsDownloadedAgainWithoutResettingJob(t *testing.T) {
	e, meta, source, photos, closeStore := fixture(t)
	defer closeStore()
	photos.loseChunk = true
	if e.Run(context.Background(), meta) == nil {
		t.Fatal("expected lost response")
	}
	if err := os.Remove(filepath.Join(e.Directory, "stage-1.bin")); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if source.reads != 2 || photos.creates != 1 {
		t.Fatal("missing cache did not recover safely")
	}
}

func TestExplicitReconciliationRetriesVerifiedIdenticalBytes(t *testing.T) {
	e, meta, _, photos, closeStore := fixture(t)
	defer closeStore()
	photos.loseCreate = true
	if e.Run(context.Background(), meta) == nil {
		t.Fatal("expected uncertain creation")
	}
	photos.loseCreate = false
	e.Reconcile = true
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	var item Item
	if err := e.Store.Load(1, &item); err != nil {
		t.Fatal(err)
	}
	if item.State != "completed" || photos.creates != 2 {
		t.Fatal("explicit reconciliation did not complete identical-byte retry")
	}
}
func TestIncompleteInventoryCanBeRestartedWithoutDuplicateItems(t *testing.T) {
	e, meta, _, _, closeStore := fixture(t)
	defer closeStore()
	meta.InventoryComplete = false
	if err := e.Discover(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	ids, err := e.Store.IDs()
	if err != nil || len(ids) != 1 {
		t.Fatalf("inventory restart duplicated records: %v %v", ids, err)
	}
}

func TestProviderPausePreventsImmediateResume(t *testing.T) {
	e, meta, _, _, closeStore := fixture(t)
	defer closeStore()
	meta.RetryAt = time.Now().Add(time.Minute)
	if err := e.Run(context.Background(), meta); err == nil {
		t.Fatal("provider pause ignored")
	}
}
func TestMediaEligibilityAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name            string
		size            int64
		video, eligible bool
	}{
		{"PHOTO.JPG", 1, false, true}, {"clip.MOV", 1, true, true}, {"archive.zip", 10, false, false},
		{"large.jpg", 200_000_001, false, false}, {"large.mp4", 20_000_000_001, true, false}, {"empty.png", 0, false, false},
	} {
		_, video, reason := classify(onedrive.Item{ID: "id", Path: tc.name, Size: tc.size})
		if video != tc.video || (reason == "") != tc.eligible {
			t.Fatalf("wrong eligibility for %s", tc.name)
		}
	}
}

type inventorySource struct {
	*sourceFake
	count int
}

func (s inventorySource) Walk(_ context.Context, _ string, visit func(onedrive.Item) error) error {
	for i := 0; i < s.count; i++ {
		item := s.item
		item.Path = fmt.Sprintf("photo-%d.jpg", i)
		item.ID = fmt.Sprint(i)
		if err := visit(item); err != nil {
			return err
		}
	}
	return nil
}
func TestTenThousandItemInventoryStaysMetadataOnly(t *testing.T) {
	e, meta, source, _, closeStore := fixture(t)
	defer closeStore()
	meta.InventoryComplete = false
	e.Source = inventorySource{sourceFake: source, count: 10000}
	if err := e.Discover(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	ids, err := e.Store.IDs()
	if err != nil || len(ids) != 10000 {
		t.Fatalf("inventory size=%d err=%v", len(ids), err)
	}
	if source.reads != 0 {
		t.Fatal("inventory downloaded source bytes")
	}
	for _, id := range []int64{1, 5000, 10000} {
		var item Item
		if err := e.Store.Load(id, &item); err != nil || item.State != "discovered" {
			t.Fatalf("inventory record %d invalid: %v", id, err)
		}
	}
}
