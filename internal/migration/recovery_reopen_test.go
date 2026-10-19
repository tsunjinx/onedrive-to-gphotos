package migration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"od2gp/internal/googlephotos"
	"od2gp/internal/journal"
	"od2gp/internal/msgraph"
	"od2gp/internal/onedrive"
)

func reopenEngine(t *testing.T, old *Engine) (*Engine, *Meta) {
	t.Helper()
	key := append([]byte(nil), old.Store.Key...)
	if err := old.Store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := journal.Open(filepath.Join(old.Directory, "job.db"), key, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	var meta Meta
	if err := store.Load(0, &meta); err != nil {
		t.Fatal(err)
	}
	return &Engine{Store: store, Source: old.Source, Photos: old.Photos, Directory: old.Directory, Progress: old.Progress, Workers: old.Workers, BatchSize: old.BatchSize}, &meta
}

func TestReopenedPartialBatchKeepsKnownIDsAndRequiresRecovery(t *testing.T) {
	e, meta, p := batchFixture(t, 6)
	p.response = func(items []googlephotos.NewItem) ([]googlephotos.CreateResult, error) {
		results := make([]googlephotos.CreateResult, len(items))
		for i := range results {
			results[i].Media.ID = items[i].Name
		}
		results[1] = googlephotos.CreateResult{Err: &googlephotos.OutcomeUnknownError{}}
		return results, nil
	}
	if err := e.Run(context.Background(), meta); err == nil {
		t.Fatal("unknown result ignored")
	}
	fresh, saved := reopenEngine(t, e)
	if err := fresh.Run(context.Background(), saved); err == nil || len(p.batches) != 1 {
		t.Fatal("normal resume repeated creation")
	}
	fresh.Reconcile = true
	p.response = nil
	if err := fresh.Run(context.Background(), saved); err != nil {
		t.Fatal(err)
	}
	if len(p.batches) != 2 || len(p.batches[1]) != 1 {
		t.Fatalf("known successes repeated: %v", p.batches)
	}
	for id := int64(1); id <= 6; id++ {
		var item Item
		if err := fresh.Store.Load(id, &item); err != nil || item.State != "completed" {
			t.Fatalf("item %d: %+v %v", id, item, err)
		}
	}
}

func TestReopenedVideoPollsSavedIDWithoutUploading(t *testing.T) {
	e, meta, source, p, close := fixture(t)
	defer close()
	var item Item
	if err := e.Store.Load(1, &item); err != nil {
		t.Fatal(err)
	}
	item.Video = true
	if err := e.Store.Save(1, item.State, item); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	fresh, saved := reopenEngine(t, e)
	p.ready = true
	if err := fresh.Run(context.Background(), saved); err != nil {
		t.Fatal(err)
	}
	if err := fresh.Store.Load(1, &item); err != nil || item.State != "completed" || p.creates != 1 || source.reads != 1 {
		t.Fatalf("video replayed or lost: %+v %v", item, err)
	}
}

type pausedSource struct {
	Source
	until time.Time
}

func (s pausedSource) Walk(context.Context, string, func(onedrive.Item) error) error {
	return &msgraph.PauseError{Until: s.until}
}
func TestGraphInventoryPauseSurvivesReopen(t *testing.T) {
	e, meta, _, _, close := fixture(t)
	defer close()
	until := time.Now().Add(time.Hour).UTC()
	meta.InventoryComplete = false
	e.Source = pausedSource{e.Source, until}
	if err := e.Discover(context.Background(), meta); err == nil {
		t.Fatal("ignored source pause")
	}
	fresh, saved := reopenEngine(t, e)
	if !saved.RetryAt.Equal(until) {
		t.Fatalf("lost pause: %v", saved.RetryAt)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := fresh.Discover(ctx, saved); err == nil {
		t.Fatal("bypassed pause")
	}
	if err := fresh.Store.Load(0, saved); err != nil || !saved.RetryAt.Equal(until) {
		t.Fatalf("cleared pause: %v", err)
	}
}
