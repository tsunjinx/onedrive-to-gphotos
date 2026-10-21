package migration

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type transientSourceError struct{}

func (transientSourceError) Error() string         { return "temporary source interruption" }
func (transientSourceError) SourceTemporary() bool { return true }

type retrySource struct {
	*sourceFake
	calls     int
	failures  int
	permanent bool
	gate      func(context.Context) error
	pause     func(time.Time) error
}

func (s *retrySource) ConfigureRetry(gate func(context.Context) error, pause func(time.Time) error) {
	s.gate = gate
	s.pause = pause
}
func (s *retrySource) Read(ctx context.Context, p string, offset int64, w io.Writer) error {
	s.calls++
	if s.calls <= s.failures {
		if s.permanent {
			return errors.New("staging permission failure")
		}
		return transientSourceError{}
	}
	return s.sourceFake.Read(ctx, p, offset, w)
}
func TestSourceDownloadRetriesAreBoundedAndPermanentErrorsStop(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		e, meta, source, _, closeStore := fixture(t)
		flaky := &retrySource{sourceFake: source, failures: 3, permanent: permanent}
		e.Source = flaky
		e.MaxRetries = 2
		waits := 0
		e.retryWait = func(context.Context, time.Duration) error {
			waits++
			var m Meta
			if err := e.Store.Load(0, &m); err != nil || !m.RetryAt.After(time.Now()) {
				t.Fatal("retry not durable", err)
			}
			return nil
		}
		if err := e.Run(context.Background(), meta); err == nil {
			t.Fatal("exhaustion accepted")
		}
		want := 3
		if permanent {
			want = 1
		}
		if flaky.calls != want || waits != want-1 {
			t.Fatalf("calls=%d waits=%d", flaky.calls, waits)
		}
		closeStore()
	}
}
func TestSourceWorkersShareDurablePause(t *testing.T) {
	e, meta, source, _, closeStore := fixture(t)
	defer closeStore()
	s := &retrySource{sourceFake: source}
	e.Source = s
	e.MaxRetries = 2
	e.bindSourceRetry(meta)
	until := time.Now().Add(time.Minute)
	if err := s.pause(until); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	waits := 0
	e.retryWait = func(context.Context, time.Duration) error { waits++; close(entered); <-release; return nil }
	done := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); done <- s.gate(context.Background()) }()
	<-entered
	wg.Add(1)
	go func() { defer wg.Done(); done <- s.gate(context.Background()) }()
	select {
	case <-done:
		t.Fatal("request passed active pause")
	default:
	}
	var saved Meta
	if err := e.Store.Load(0, &saved); err != nil || !saved.RetryAt.Equal(until) {
		t.Fatal("pause not saved", err)
	}
	close(release)
	wg.Wait()
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if waits != 1 {
		t.Fatalf("workers did not share wait: %d", waits)
	}
}
