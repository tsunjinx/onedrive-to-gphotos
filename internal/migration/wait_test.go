package migration

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitForVideosBacksOffWithoutUploadingAgain(t *testing.T) {
	e, meta, source, photos, closeStore := fixture(t)
	defer closeStore()
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
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute}
	calls := 0
	err := e.waitForVideos(context.Background(), meta, func(_ context.Context, delay time.Duration) error {
		if calls >= len(want) || delay != want[calls] {
			t.Fatalf("unexpected delay %s at check %d", delay, calls)
		}
		calls++
		photos.ready = calls == len(want)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != len(want) || photos.creates != 1 || source.reads != 1 {
		t.Fatalf("unexpected polling/transfer: waits=%d creates=%d downloads=%d", calls, photos.creates, source.reads)
	}
	if err := e.Store.Load(1, &item); err != nil || item.State != "completed" {
		t.Fatalf("readiness not committed: state=%s err=%v", item.State, err)
	}
}

func TestCancelledVideoWaitKeepsPendingState(t *testing.T) {
	e, meta, _, _, closeStore := fixture(t)
	defer closeStore()
	item := Item{State: "processing", Video: true, MediaID: "created-id"}
	if err := e.Store.Save(1, item.State, item); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := e.waitForVideos(ctx, meta, func(ctx context.Context, _ time.Duration) error {
		cancel()
		return waitContext(ctx, time.Hour)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if err := e.Store.Load(1, &item); err != nil || item.State != "processing" {
		t.Fatalf("pending state lost: %s %v", item.State, err)
	}
}

func TestVideoWaitReturnsImmediatelyForFinishedJobs(t *testing.T) {
	for _, state := range []string{"completed", "skipped", "processing_failed"} {
		t.Run(state, func(t *testing.T) {
			e, meta, _, photos, closeStore := fixture(t)
			defer closeStore()
			if err := e.Store.Save(1, state, Item{State: state}); err != nil {
				t.Fatal(err)
			}
			err := e.waitForVideos(context.Background(), meta, func(context.Context, time.Duration) error {
				t.Fatal("finished job waited for video processing")
				return nil
			})
			if err != nil || photos.creates != 0 || photos.starts != 0 {
				t.Fatalf("finished job changed: err=%v creates=%d starts=%d", err, photos.creates, photos.starts)
			}
		})
	}
}

func TestVideoWaitHonorsDeadlineWithoutChangingProgress(t *testing.T) {
	e, meta, _, _, closeStore := fixture(t)
	defer closeStore()
	if err := e.Store.Save(1, "processing", Item{State: "processing", MediaID: "created-id", Video: true}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := e.WaitForVideos(ctx, meta); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait ignored deadline: %v", err)
	}
	var item Item
	if err := e.Store.Load(1, &item); err != nil || item.State != "processing" {
		t.Fatalf("deadline lost pending state: %s %v", item.State, err)
	}
}
