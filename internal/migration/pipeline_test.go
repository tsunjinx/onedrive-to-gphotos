package migration

import (
	"context"
	"od2gp/internal/googlephotos"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type observedBatchPhotos struct {
	*batchingPhotos
	checkStatus func()
	uploaded    atomic.Int32
	second      chan struct{}
}

func (p *observedBatchPhotos) Chunk(ctx context.Context, s googlephotos.Session, o int64, b []byte, f bool) (string, error) {
	token, err := p.batchingPhotos.Chunk(ctx, s, o, b, f)
	if err == nil && f && p.uploaded.Add(1) == 2 {
		close(p.second)
	}
	return token, err
}
func TestUploadContinuesDuringBlockedBatch(t *testing.T) {
	e, meta, batch := batchFixture(t, 6)
	e.Workers = 1
	e.BatchSize = 1
	p := &observedBatchPhotos{batchingPhotos: batch, second: make(chan struct{})}
	e.Photos = p
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	batch.response = func(items []googlephotos.NewItem) ([]googlephotos.CreateResult, error) {
		once.Do(func() { close(started); <-release })
		results := make([]googlephotos.CreateResult, len(items))
		for i := range results {
			results[i].Media.ID = items[i].Name
		}
		return results, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx, meta) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("batch never started")
	}
	progressed := false
	select {
	case <-p.second:
		progressed = true
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !progressed {
		t.Fatal("idle worker could not upload another file during batch creation")
	}
}

func (p *observedBatchPhotos) Get(ctx context.Context, id string) (googlephotos.Media, error) {
	if p.checkStatus != nil {
		p.checkStatus()
		var media googlephotos.Media
		media.ID = id
		media.Metadata.Video = &struct {
			Status string `json:"status"`
		}{Status: "READY"}
		return media, nil
	}
	return p.batchingPhotos.Get(ctx, id)
}
func TestVideoCacheFreedBeforeStatusPolling(t *testing.T) {
	e, meta, batch := batchFixture(t, 1)
	e.BatchSize = 1
	var item Item
	if err := e.Store.Load(1, &item); err != nil {
		t.Fatal(err)
	}
	item.Video = true
	if err := e.Store.Save(1, item.State, item); err != nil {
		t.Fatal(err)
	}
	p := &observedBatchPhotos{batchingPhotos: batch, second: make(chan struct{})}
	e.Photos = p
	polled := false
	p.checkStatus = func() {
		polled = true
		if p.uploaded.Load() != 6 {
			t.Error("video status polling blocked remaining uploads")
		}
		files, err := filepath.Glob(filepath.Join(e.Directory, "stage-*.bin"))
		if err != nil || len(files) != 0 {
			t.Errorf("cache retained after durable media creation: %v %v", files, err)
		}
	}
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if !polled {
		t.Fatal("video status was not checked")
	}
	if err := e.Store.Load(1, &item); err != nil || item.State != "completed" {
		t.Fatalf("video not completed: %v %s", err, item.State)
	}
	if _, err := os.Stat(filepath.Join(e.Directory, "stage-1.bin")); !os.IsNotExist(err) {
		t.Fatal("staging was not removed")
	}
}
