package migration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"od2gp/internal/googlephotos"
	"od2gp/internal/onedrive"
	"od2gp/internal/staging"
)

type parallelSource struct {
	item onedrive.Item
	data []byte
}

func (s parallelSource) DriveID(context.Context) (string, error)                       { return "drive", nil }
func (s parallelSource) Walk(context.Context, string, func(onedrive.Item) error) error { return nil }
func (s parallelSource) Stat(context.Context, string) (onedrive.Item, error)           { return s.item, nil }
func (s parallelSource) Read(ctx context.Context, _ string, _ int64, w io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := w.Write(s.data)
	return err
}

type concurrentPhotos struct {
	mu                                                sync.Mutex
	active, maxActive, creating, maxCreating, created int
	target                                            int
	barrier                                           chan struct{}
	once                                              sync.Once
	startFailures                                     int
	fail                                              bool
}

func (p *concurrentPhotos) Start(context.Context, string, int64) (googlephotos.Session, error) {
	p.mu.Lock()
	if p.startFailures > 0 {
		p.startFailures--
		p.mu.Unlock()
		return googlephotos.Session{}, &googlephotos.APIError{Code: 429, RetryAt: time.Now().Add(time.Minute)}
	}
	p.mu.Unlock()
	return googlephotos.Session{URL: "session", Granularity: 1, Created: time.Now()}, nil
}
func (p *concurrentPhotos) Query(context.Context, googlephotos.Session) (int64, string, error) {
	return 0, "active", nil
}
func (p *concurrentPhotos) Chunk(ctx context.Context, _ googlephotos.Session, _ int64, _ []byte, _ bool) (string, error) {
	p.mu.Lock()
	p.active++
	p.maxActive = max(p.maxActive, p.active)
	if p.active >= p.target {
		p.once.Do(func() { close(p.barrier) })
	}
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.active--; p.mu.Unlock() }()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-p.barrier:
	}
	if p.fail {
		return "", errors.New("simulated upload interruption")
	}
	return "token", nil
}
func (p *concurrentPhotos) Create(ctx context.Context, _, _ string) (googlephotos.Media, error) {
	p.mu.Lock()
	p.creating++
	p.maxCreating = max(p.maxCreating, p.creating)
	p.mu.Unlock()
	time.Sleep(time.Millisecond)
	p.mu.Lock()
	p.creating--
	p.created++
	id := p.created
	p.mu.Unlock()
	return googlephotos.Media{ID: fmt.Sprint(id)}, nil
}
func (p *concurrentPhotos) Get(context.Context, string) (googlephotos.Media, error) {
	return googlephotos.Media{}, nil
}

func parallelFixture(t *testing.T, capFiles, target int) (*Engine, *Meta, *concurrentPhotos) {
	t.Helper()
	e, meta, source, _, closeStore := fixture(t)
	t.Cleanup(closeStore)
	e.Source = parallelSource{item: source.item, data: source.data}
	e.Workers = 3
	for id := int64(2); id <= 6; id++ {
		var item Item
		if err := e.Store.Load(1, &item); err != nil {
			t.Fatal(err)
		}
		item.Path = fmt.Sprintf("photo-%d.jpg", id)
		if err := e.Store.Save(id, item.State, item); err != nil {
			t.Fatal(err)
		}
	}
	meta.CacheBytes = int64(capFiles) * staging.Required(source.item.Size)
	p := &concurrentPhotos{target: target, barrier: make(chan struct{})}
	e.Photos = p
	return e, meta, p
}

func TestParallelTransfersBoundWorkersAndStagingAndSkipOnResume(t *testing.T) {
	for _, capFiles := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(capFiles), func(t *testing.T) {
			e, meta, p := parallelFixture(t, capFiles, capFiles)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := e.Run(ctx, meta); err != nil {
				t.Fatal(err)
			}
			if p.maxActive != capFiles || p.maxCreating != 1 || p.created != 6 {
				t.Fatalf("concurrency: %+v", p)
			}
			for id := int64(1); id <= 6; id++ {
				var item Item
				if err := e.Store.Load(id, &item); err != nil || item.State != "completed" {
					t.Fatalf("item %d state=%s err=%v", id, item.State, err)
				}
			}
			if err := e.Run(ctx, meta); err != nil || p.created != 6 {
				t.Fatalf("repeated completed files: %d %v", p.created, err)
			}
			files, _ := filepath.Glob(filepath.Join(e.Directory, "stage-*.bin"))
			if len(files) != 0 {
				t.Fatalf("staging retained: %v", files)
			}
		})
	}
}

func TestParallelFailureKeepsCheckpointsAndResumesWithinCap(t *testing.T) {
	e, meta, p := parallelFixture(t, 3, 3)
	p.fail = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Run(ctx, meta); err == nil {
		t.Fatal("ignored upload failure")
	}
	var used int64
	files, _ := filepath.Glob(filepath.Join(e.Directory, "stage-*.bin"))
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		used += info.Size()
	}
	if used > meta.CacheBytes || len(files) == 0 {
		t.Fatalf("invalid retained staging: %d %v", used, files)
	}
	// Existing staged files must run before waiting new downloads fill capacity.
	p.fail = false
	p.target = 1
	e.Workers = 1
	if err := e.Run(ctx, meta); err != nil {
		t.Fatal(err)
	}
	if p.created != 6 {
		t.Fatalf("not all files recovered: %d", p.created)
	}
}

func TestParallelCancelledRunAndUnknownStaging(t *testing.T) {
	e, meta, p := parallelFixture(t, 3, 4)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Run(ctx, meta); err == nil {
		t.Fatal("ignored cancellation")
	}
	if p.created != 0 {
		t.Fatal("created after cancellation")
	}
	if err := os.WriteFile(filepath.Join(e.Directory, "stage-999.bin"), []byte("unknown"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background(), meta); err == nil || !strings.Contains(err.Error(), "unrecognized staging") {
		t.Fatalf("ignored unknown staging: %v", err)
	}
}

func TestParallelActiveCancellationPreservesResumableItems(t *testing.T) {
	e, meta, p := parallelFixture(t, 3, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := e.Run(ctx, meta); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if p.created != 0 {
		t.Fatal("created after cancelled uploads")
	}
	// All workers must be joined before changing provider state or closing the DB.
	p.target = 1
	p.once.Do(func() { close(p.barrier) })
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if p.created != 6 {
		t.Fatalf("recovery incomplete: %d", p.created)
	}
}

func TestParallelUncertainCreationStopsBeforeNewUploads(t *testing.T) {
	e, meta, p := parallelFixture(t, 3, 1)
	var item Item
	if err := e.Store.Load(6, &item); err != nil {
		t.Fatal(err)
	}
	item.State = "creating"
	if err := e.Store.Save(6, item.State, item); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background(), meta); err == nil || !strings.Contains(err.Error(), "uncertain") {
		t.Fatalf("ignored uncertain outcome: %v", err)
	}
	if p.created != 0 || p.maxActive != 0 {
		t.Fatal("started work before reconciliation")
	}
}

func TestParallelProviderGateHonorsSharedPause(t *testing.T) {
	e, meta, p := parallelFixture(t, 3, 1)
	until := time.Now().Add(time.Minute)
	meta.RetryAt = until
	e.MaxRetries = 1
	if err := e.Store.Save(0, "job", meta); err != nil {
		t.Fatal(err)
	}
	waits := 0
	e.retryWait = func(ctx context.Context, delay time.Duration) error {
		waits++
		if delay < 59*time.Second {
			t.Fatalf("pause shortened: %v", delay)
		}
		var saved Meta
		if err := e.Store.Load(0, &saved); err != nil || !saved.RetryAt.Equal(until) {
			t.Fatalf("pause not durable: %v", err)
		}
		return context.Canceled
	}
	wrapped := &parallelPhotos{Photos: p, e: e, meta: meta}
	if _, err := wrapped.Start(context.Background(), "image/jpeg", 14); !errors.Is(err, context.Canceled) {
		t.Fatalf("gate ignored pause: %v", err)
	}
	if waits != 1 || !meta.RetryAt.Equal(until) {
		t.Fatal("lost pending pause")
	}
}

func TestParallelTransientFailureRetainsSharedRetryCheckpoint(t *testing.T) {
	e, meta, p := parallelFixture(t, 3, 1)
	p.startFailures = 1
	e.MaxRetries = 1
	e.retryWait = func(ctx context.Context, delay time.Duration) error {
		var saved Meta
		if err := e.Store.Load(0, &saved); err != nil || !saved.RetryAt.After(time.Now()) {
			t.Errorf("missing shared pause: %v", err)
		}
		if delay < 59*time.Second {
			t.Errorf("provider pause shortened: %v", delay)
		}
		return nil
	}
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if p.created != 6 || !meta.RetryAt.IsZero() {
		t.Fatalf("retry did not finish: created=%d pause=%v", p.created, meta.RetryAt)
	}
}
