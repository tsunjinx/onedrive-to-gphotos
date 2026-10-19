package migration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"od2gp/internal/googlephotos"
)

type batchingPhotos struct {
	*concurrentPhotos
	batchMu  sync.Mutex
	batches  [][]googlephotos.NewItem
	response func([]googlephotos.NewItem) ([]googlephotos.CreateResult, error)
}

func (p *batchingPhotos) CreateBatch(ctx context.Context, items []googlephotos.NewItem) ([]googlephotos.CreateResult, error) {
	p.batchMu.Lock()
	defer p.batchMu.Unlock()
	p.batches = append(p.batches, append([]googlephotos.NewItem(nil), items...))
	if p.response != nil {
		return p.response(items)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.creating++
	p.maxCreating = max(p.maxCreating, p.creating)
	defer func() { p.creating-- }()
	results := make([]googlephotos.CreateResult, len(items))
	for i := range results {
		p.created++
		results[i].Media.ID = fmt.Sprint(p.created)
	}
	return results, nil
}
func batchFixture(t *testing.T, capFiles int) (*Engine, *Meta, *batchingPhotos) {
	e, meta, p := parallelFixture(t, capFiles, 1)
	batch := &batchingPhotos{concurrentPhotos: p}
	e.Photos = batch
	e.BatchSize = 50
	e.BatchWait = time.Second
	return e, meta, batch
}
func TestBatchCommitFlushesAtCapacityAndEndWithoutDeadlock(t *testing.T) {
	for _, capFiles := range []int{1, 3, 6} {
		t.Run(fmt.Sprint(capFiles), func(t *testing.T) {
			e, meta, p := batchFixture(t, capFiles)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := e.Run(ctx, meta); err != nil {
				t.Fatal(err)
			}
			if p.created != 6 {
				t.Fatalf("lost completed items: %d", p.created)
			}
			if capFiles == 6 && len(p.batches) != 1 {
				t.Fatalf("did not batch queued uploads: %d calls", len(p.batches))
			}
			for _, batch := range p.batches {
				if len(batch) > capFiles {
					t.Fatalf("staging cap bypassed: %d", len(batch))
				}
			}
			if err := e.Run(ctx, meta); err != nil || p.created != 6 {
				t.Fatalf("repeated confirmed items: %v", err)
			}
		})
	}
}
func TestBatchLimitAndTail(t *testing.T) {
	e, meta, p := batchFixture(t, 100)
	e.BatchWait = time.Minute
	for id := int64(7); id <= 61; id++ {
		var item Item
		if err := e.Store.Load(1, &item); err != nil {
			t.Fatal(err)
		}
		item.Path = fmt.Sprintf("photo-%d.jpg", id)
		if err := e.Store.Save(id, item.State, item); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if len(p.batches) != 2 || len(p.batches[0]) != 50 || len(p.batches[1]) != 11 {
		t.Fatalf("wrong batch sizes: %v", p.batches)
	}
}
func TestBatchPartialResultsPreserveEverySuccessfulID(t *testing.T) {
	e, meta, p := batchFixture(t, 6)
	p.response = func(items []googlephotos.NewItem) ([]googlephotos.CreateResult, error) {
		results := make([]googlephotos.CreateResult, len(items))
		for i := range results {
			results[i].Media.ID = fmt.Sprintf("media-%d", i)
		}
		results[1] = googlephotos.CreateResult{Err: &googlephotos.ItemError{Code: 3}}
		return results, nil
	}
	if err := e.Run(context.Background(), meta); err == nil {
		t.Fatal("ignored rejection")
	}
	completed, uploaded := 0, 0
	for id := int64(1); id <= 6; id++ {
		var item Item
		if err := e.Store.Load(id, &item); err != nil {
			t.Fatal(err)
		}
		switch item.State {
		case "completed":
			completed++
			if item.MediaID == "" {
				t.Fatal("lost media ID")
			}
		case "uploaded":
			uploaded++
		default:
			t.Fatalf("wrong partial state: %s", item.State)
		}
	}
	if completed != 5 || uploaded != 1 {
		t.Fatalf("lost partial successes: %d/%d", completed, uploaded)
	}
}
func TestLostBatchResponseRemainsUncertainAndRetainsStaging(t *testing.T) {
	e, meta, p := batchFixture(t, 6)
	e.MaxRetries = 2
	p.response = func([]googlephotos.NewItem) ([]googlephotos.CreateResult, error) {
		return nil, &googlephotos.APIError{Code: 503}
	}
	if err := e.Run(context.Background(), meta); err == nil {
		t.Fatal("ignored ambiguous response")
	}
	if len(p.batches) != 1 {
		t.Fatal("blindly retried uncertain creation")
	}
	for id := int64(1); id <= 6; id++ {
		var item Item
		if err := e.Store.Load(id, &item); err != nil || item.State != "creating" {
			t.Fatalf("unsafe checkpoint: %s %v", item.State, err)
		}
		if _, err := os.Stat(filepath.Join(e.Directory, fmt.Sprintf("stage-%d.bin", id))); err != nil {
			t.Fatal("lost staged bytes")
		}
	}
	meta.RetryAt = time.Time{}
	if err := e.Run(context.Background(), meta); err == nil {
		t.Fatal("resumed without reconciliation")
	}
	if len(p.batches) != 1 {
		t.Fatal("retried without consent")
	}
}
func TestBatchCancellationLeavesUploadedQueueResumable(t *testing.T) {
	e, meta, p := batchFixture(t, 6)
	p.response = func([]googlephotos.NewItem) ([]googlephotos.CreateResult, error) { return nil, context.Canceled }
	if err := e.Run(context.Background(), meta); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

type slowLastSource struct {
	parallelSource
	released <-chan struct{}
}

func (s slowLastSource) Read(ctx context.Context, name string, offset int64, w io.Writer) error {
	if name == "photo-6.jpg" {
		select {
		case <-s.released:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.parallelSource.Read(ctx, name, offset, w)
}
func TestBatchTimerFlushesWhileAnotherUploadIsWaiting(t *testing.T) {
	e, meta, p := batchFixture(t, 6)
	e.BatchWait = 20 * time.Millisecond
	released := make(chan struct{})
	var once sync.Once
	e.Source = slowLastSource{parallelSource: e.Source.(parallelSource), released: released}
	p.response = func(items []googlephotos.NewItem) ([]googlephotos.CreateResult, error) {
		once.Do(func() { close(released) })
		results := make([]googlephotos.CreateResult, len(items))
		for i := range results {
			results[i].Media.ID = items[i].Name
		}
		return results, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := e.Run(ctx, meta); err != nil {
		t.Fatal(err)
	}
	if len(p.batches) < 2 || len(p.batches[0]) >= 6 {
		t.Fatalf("timer did not flush a partial batch: %v", p.batches)
	}
}
func TestRejectedBatch429PersistsPauseAndRetriesOnlyCreation(t *testing.T) {
	e, meta, p := batchFixture(t, 6)
	e.MaxRetries = 1
	requests := 0
	p.response = func(items []googlephotos.NewItem) ([]googlephotos.CreateResult, error) {
		requests++
		for id := int64(1); id <= 6; id++ {
			var item Item
			if err := e.Store.Load(id, &item); err != nil || item.State != "creating" {
				t.Errorf("batch not checkpointed before request: %s %v", item.State, err)
			}
		}
		if requests == 1 {
			return nil, &googlephotos.APIError{Code: 429, Operation: "media creation", RetryAt: time.Now().Add(time.Minute)}
		}
		results := make([]googlephotos.CreateResult, len(items))
		for i := range results {
			results[i].Media.ID = items[i].Name
		}
		return results, nil
	}
	e.retryWait = func(ctx context.Context, delay time.Duration) error {
		if delay < 59*time.Second {
			t.Errorf("Retry-After shortened: %s", delay)
		}
		var saved Meta
		if err := e.Store.Load(0, &saved); err != nil || !saved.RetryAt.After(time.Now()) {
			t.Errorf("pause not durable: %v", err)
		}
		for id := int64(1); id <= 6; id++ {
			var item Item
			if err := e.Store.Load(id, &item); err != nil || item.State != "uploaded" {
				t.Errorf("safe rejection lost token: %s %v", item.State, err)
			}
		}
		return nil
	}
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("wrong retry count: %d", requests)
	}
}
func TestPartialBatchRetriesOnlyExplicitTemporaryFailures(t *testing.T) {
	e, meta, p := batchFixture(t, 6)
	e.MaxRetries = 1
	e.retryWait = func(context.Context, time.Duration) error { return nil }
	first := true
	p.response = func(items []googlephotos.NewItem) ([]googlephotos.CreateResult, error) {
		results := make([]googlephotos.CreateResult, len(items))
		for i := range results {
			results[i].Media.ID = items[i].Name
		}
		if first {
			first = false
			results[1] = googlephotos.CreateResult{Err: &googlephotos.ItemError{Code: 8}}
		}
		return results, nil
	}
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if len(p.batches) != 2 || len(p.batches[0]) != 6 || len(p.batches[1]) != 1 {
		t.Fatalf("retried successful items: %v", p.batches)
	}
}
func TestLostBatchReconciliationVerifiesOriginalStaging(t *testing.T) {
	e, meta, p := batchFixture(t, 6)
	p.response = func([]googlephotos.NewItem) ([]googlephotos.CreateResult, error) {
		return nil, &googlephotos.ConnectionError{}
	}
	if err := e.Run(context.Background(), meta); err == nil {
		t.Fatal("ignored missing response")
	}
	e.Reconcile = true
	p.response = nil
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if p.created != 6 {
		t.Fatalf("reconciliation incomplete: %d", p.created)
	}
}
