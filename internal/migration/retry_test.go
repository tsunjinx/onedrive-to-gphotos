package migration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"od2gp/internal/googlephotos"
)

type flakyPhotos struct {
	*photosFake
	startError, createError error
	startCalls, createCalls int
	startFailures           int
	createFailures          int
	lostChunk               bool
}

func (p *flakyPhotos) Start(ctx context.Context, mime string, size int64) (googlephotos.Session, error) {
	p.startCalls++
	if p.startCalls <= p.startFailures {
		return googlephotos.Session{}, p.startError
	}
	return p.photosFake.Start(ctx, mime, size)
}
func (p *flakyPhotos) Chunk(ctx context.Context, session googlephotos.Session, offset int64, data []byte, final bool) (string, error) {
	token, err := p.photosFake.Chunk(ctx, session, offset, data, final)
	if p.lostChunk {
		p.lostChunk = false
		return "", &googlephotos.ConnectionError{}
	}
	return token, err
}
func (p *flakyPhotos) Create(ctx context.Context, token, name string) (googlephotos.Media, error) {
	p.createCalls++
	if p.createError != nil && (p.createFailures == 0 || p.createCalls <= p.createFailures) {
		return googlephotos.Media{}, p.createError
	}
	return p.photosFake.Create(ctx, token, name)
}

func TestRetriesTransientFailureFromCommittedStage(t *testing.T) {
	e, meta, source, photos, closeStore := fixture(t)
	defer closeStore()
	flaky := &flakyPhotos{photosFake: photos, startFailures: 1, startError: &googlephotos.APIError{Code: 503}}
	e.Photos = flaky
	e.MaxRetries = 2
	waits := 0
	e.retryWait = func(_ context.Context, delay time.Duration) error {
		waits++
		if delay < 30*time.Second || delay > 32*time.Second {
			t.Fatalf("unexpected retry delay: %s", delay)
		}
		var saved Meta
		if err := e.Store.Load(0, &saved); err != nil || !saved.RetryAt.After(time.Now()) {
			t.Fatalf("pause not saved before waiting: %v", err)
		}
		return nil
	}
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if waits != 1 || flaky.startCalls != 2 || photos.creates != 1 || source.reads != 1 {
		t.Fatalf("unsafe retry: waits=%d starts=%d creates=%d downloads=%d", waits, flaky.startCalls, photos.creates, source.reads)
	}
	if !meta.RetryAt.IsZero() {
		t.Fatal("successful retry left an active pause")
	}
}

func TestLostChunkRetryQueriesServerBeforeContinuing(t *testing.T) {
	e, meta, source, photos, closeStore := fixture(t)
	defer closeStore()
	e.Photos = &flakyPhotos{photosFake: photos, lostChunk: true}
	e.MaxRetries = 1
	e.retryWait = func(context.Context, time.Duration) error { return nil }
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if photos.queries != 1 || photos.starts != 1 || photos.creates != 1 || source.reads != 1 {
		t.Fatalf("unsafe lost-response retry: %+v reads=%d", photos, source.reads)
	}
}

func TestUncertainCreationAndPermanentErrorsAreNeverAutoRetried(t *testing.T) {
	for _, failure := range []error{&googlephotos.APIError{Code: 503}, &googlephotos.ConnectionError{}, &googlephotos.APIError{Code: 403}, errors.New("invalid creation response")} {
		t.Run(failure.Error(), func(t *testing.T) {
			e, meta, _, photos, closeStore := fixture(t)
			defer closeStore()
			flaky := &flakyPhotos{photosFake: photos, createError: failure}
			e.Photos, e.MaxRetries, e.Reconcile = flaky, 3, true
			e.retryWait = func(context.Context, time.Duration) error {
				t.Fatal("automatically retried uncertain or permanent creation failure")
				return nil
			}
			if err := e.Run(context.Background(), meta); err == nil || flaky.createCalls != 1 {
				t.Fatalf("creation outcome ignored: %v calls=%d", err, flaky.createCalls)
			}
		})
	}
}

func TestRetryBudgetAndBackoffAreBounded(t *testing.T) {
	e, meta, _, photos, closeStore := fixture(t)
	defer closeStore()
	flaky := &flakyPhotos{photosFake: photos, startFailures: 100, startError: &googlephotos.APIError{Code: 429}}
	e.Photos, e.MaxRetries = flaky, 2
	waits := 0
	e.retryWait = func(_ context.Context, delay time.Duration) error {
		minimum := 30 * time.Second * time.Duration(1<<waits)
		if delay < minimum || delay > minimum+2*time.Second {
			t.Fatalf("wrong backoff: %s", delay)
		}
		waits++
		return nil
	}
	if err := e.Run(context.Background(), meta); err == nil || waits != 2 || flaky.startCalls != 3 {
		t.Fatalf("unbounded retry: %v waits=%d calls=%d", err, waits, flaky.startCalls)
	}
	var saved Meta
	if err := e.Store.Load(0, &saved); err != nil || !saved.RetryAt.After(time.Now()) {
		t.Fatalf("final pause not persisted: %v", err)
	}
}

func TestCancellationDuringRetryPreservesPauseAndStage(t *testing.T) {
	e, meta, source, photos, closeStore := fixture(t)
	defer closeStore()
	flaky := &flakyPhotos{photosFake: photos, startFailures: 1, startError: &googlephotos.APIError{Code: 503}}
	e.Photos, e.MaxRetries = flaky, 2
	e.retryWait = func(context.Context, time.Duration) error { return context.Canceled }
	if err := e.Run(context.Background(), meta); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	var item Item
	if err := e.Store.Load(1, &item); err != nil || item.State != "staged" || source.reads != 1 || !meta.RetryAt.After(time.Now()) {
		t.Fatalf("recovery state lost: %+v err=%v", item, err)
	}
}

func TestRetryAfterIsHonoredAndLongPausesStop(t *testing.T) {
	for _, pause := range []time.Duration{2 * time.Minute, time.Hour} {
		t.Run(pause.String(), func(t *testing.T) {
			e, meta, _, photos, closeStore := fixture(t)
			defer closeStore()
			until := time.Now().Add(pause)
			e.Photos = &flakyPhotos{photosFake: photos, startFailures: 1, startError: &googlephotos.APIError{Code: 429, RetryAt: until}}
			e.MaxRetries = 2
			waits := 0
			e.retryWait = func(_ context.Context, delay time.Duration) error {
				waits++
				if pause > 15*time.Minute || delay < pause-time.Second {
					t.Fatalf("provider pause shortened or wait unbounded: %s", delay)
				}
				return nil
			}
			err := e.Run(context.Background(), meta)
			if pause > 15*time.Minute {
				if err == nil || !strings.Contains(err.Error(), "longer than") || waits != 0 || !meta.RetryAt.Equal(until) {
					t.Fatalf("long pause not saved: %v waits=%d retry=%s", err, waits, meta.RetryAt)
				}
			} else if err != nil || waits != 1 {
				t.Fatalf("retry-after failed: %v waits=%d", err, waits)
			}
		})
	}
}

func TestResumeWaitsForSavedPauseBeforeAnyTransfer(t *testing.T) {
	e, meta, source, photos, closeStore := fixture(t)
	defer closeStore()
	e.MaxRetries = 1
	meta.RetryAt = time.Now().Add(time.Minute)
	if err := e.Store.Save(0, "job", meta); err != nil {
		t.Fatal(err)
	}
	waits := 0
	e.retryWait = func(_ context.Context, delay time.Duration) error {
		waits++
		if source.reads != 0 || photos.starts != 0 || delay < 59*time.Second {
			t.Fatal("transfer started before the saved pause")
		}
		return nil
	}
	if err := e.Run(context.Background(), meta); err != nil || waits != 1 || photos.creates != 1 {
		t.Fatalf("saved-pause resume failed: %v waits=%d", err, waits)
	}
}

func TestKnownCreateRateLimitRetriesSavedTokenWithoutReupload(t *testing.T) {
	e, meta, source, photos, closeStore := fixture(t)
	defer closeStore()
	flaky := &flakyPhotos{photosFake: photos, createError: &googlephotos.APIError{Code: 429}, createFailures: 1}
	e.Photos, e.MaxRetries = flaky, 1
	waits := 0
	e.retryWait = func(context.Context, time.Duration) error {
		waits++
		var item Item
		if err := e.Store.Load(1, &item); err != nil || item.State != "uploaded" || item.Token == "" {
			t.Fatalf("known rejection lost the upload token: state=%s err=%v", item.State, err)
		}
		return nil
	}
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if waits != 1 || flaky.createCalls != 2 || photos.starts != 1 || photos.creates != 1 || source.reads != 1 {
		t.Fatalf("rate limit caused repeated upload or creation: waits=%d calls=%d starts=%d creates=%d reads=%d", waits, flaky.createCalls, photos.starts, photos.creates, source.reads)
	}
}
