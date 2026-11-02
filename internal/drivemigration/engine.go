package drivemigration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"od2gp/internal/googledrive"
	"od2gp/internal/journal"
	"od2gp/internal/staging"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type Engine struct {
	sent        atomic.Int64
	latency     atomic.Int64
	requests    atomic.Int64
	retryDelay  func(int) time.Duration
	Store       *journal.Store
	Source      Source
	Destination Destination
	Meta        *Meta
	Directory   string
	Workers     int
	Auto        bool
	Progress    io.Writer
	Wait        func(context.Context, time.Duration) error
	mu          sync.Mutex
	pause       time.Time
}

func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (e *Engine) gate(ctx context.Context) error {
	e.mu.Lock()
	pause := e.pause
	e.mu.Unlock()
	w := e.Wait
	if w == nil {
		w = wait
	}
	if d := time.Until(pause); d > 0 {
		return w(ctx, d)
	}
	return ctx.Err()
}
func (e *Engine) paused(until time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if until.After(e.pause) {
		e.pause = until
		e.Meta.PauseUntil = until
		return e.Store.Save(0, "drive-job", e.Meta)
	}
	return nil
}

type task struct {
	id int64
	n  Node
}
type result struct {
	task
	err error
}

func (e *Engine) Run(ctx context.Context) error {
	if e.Meta.Kind != Kind || !e.Meta.InventoryComplete {
		return errors.New("Drive inventory is incomplete")
	}
	if e.Workers < 1 || e.Workers > 8 {
		return errors.New("workers must be 1-8")
	}
	if e.Progress == nil {
		e.Progress = io.Discard
	}
	e.sent.Store(0)
	e.latency.Store(0)
	e.requests.Store(0)
	e.pause = e.Meta.PauseUntil
	if source, ok := e.Source.(interface {
		ConfigureRetry(func(context.Context) error, func(time.Time) error)
	}); ok {
		source.ConfigureRetry(e.gate, e.paused)
	}
	ids, err := e.Store.IDs()
	if err != nil {
		return err
	}
	nodes := map[string]*task{}
	for _, id := range ids {
		var n Node
		if err = e.Store.Load(id, &n); err != nil {
			return err
		}
		if n.State == "blocked" {
			n.State = "retry"
			n.RetryAt = time.Time{}
			if err = e.save(id, &n); err != nil {
				return err
			}
		}
		if n.State == "completed" || n.State == "skipped" {
			if err = os.Remove(e.stagePath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		nodes[n.Source.Path] = &task{id, n}
	}
	known := map[string]bool{}
	for _, t := range nodes {
		known[e.stagePath(t.id)] = true
	}
	files, err := filepath.Glob(filepath.Join(e.Directory, "stage-*.bin"))
	if err != nil {
		return err
	}
	for _, f := range files {
		if !known[f] {
			return errors.New("unrecognized staging file; intervention required")
		}
	}
	var used int64
	allocated := map[int64]int64{}
	for _, t := range nodes {
		info, err := os.Lstat(e.stagePath(t.id))
		if err == nil {
			if !info.Mode().IsRegular() {
				return errors.New("unsafe staging file")
			}
			allocated[t.id] = info.Size()
			used += info.Size()
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if used > e.Meta.CacheBytes {
		return errors.New("saved staging exceeds cap; intervention required")
	}
	free, err := staging.Free(e.Directory)
	if err != nil {
		return err
	}
	if free < 128<<20 {
		return errors.New("insufficient local disk reserve")
	}
	capacity := min(e.Meta.CacheBytes, used+int64(free-(128<<20)))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := make(chan task)
	done := make(chan result, 8)
	var pool sync.WaitGroup
	for range 8 {
		pool.Go(func() {
			for t := range work {
				err := e.transfer(ctx, t.id, &t.n)
				done <- result{t, err}
			}
		})
	}
	active := map[int64]bool{}
	count := 0
	defer func() { cancel(); close(work); pool.Wait() }()
	tuner := NewTuner(time.Now())
	limit := e.Workers
	if e.Auto {
		limit = tuner.Limit
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	started := time.Now()
	lastPrint := time.Time{}
	lastMetrics := started
	for {
		if err := ctx.Err(); err != nil {
			for count > 0 {
				<-done
				count--
			}
			return err
		}
		var pending []*task
		unresolved := 0
		blocked := 0
		completed := 0
		var bytes int64
		for _, t := range nodes {
			if t.n.State == "completed" || t.n.State == "skipped" {
				completed++
				if !t.n.Source.IsDir {
					bytes += t.n.Source.Size
				}
				continue
			}
			unresolved++
			if t.n.State == "blocked" {
				blocked++
				continue
			}
			if active[t.id] || time.Now().Before(t.n.RetryAt) {
				continue
			}
			if t.n.Source.Path != "" {
				p, ok := nodes[t.n.Parent]
				if !ok {
					return errors.New("journal parent missing")
				}
				if p.n.State != "completed" {
					continue
				}
				t.n.ParentTarget = p.n.TargetID
			} else {
				if t.n.ParentTarget == "" {
					t.n.ParentTarget = e.Meta.RootParentID
				}
			}
			pending = append(pending, t)
		}
		if unresolved == 0 {
			return nil
		}
		sort.Slice(pending, func(i, j int) bool {
			if pending[i].n.Source.IsDir != pending[j].n.Source.IsDir {
				return pending[i].n.Source.IsDir
			}
			return pending[i].n.Source.Size < pending[j].n.Source.Size
		})
		dispatched := false
		for _, t := range pending {
			if count >= limit {
				break
			}
			e.mu.Lock()
			paused := time.Now().Before(e.pause)
			e.mu.Unlock()
			if paused {
				break
			}
			required := int64(0)
			if !t.n.Source.IsDir {
				required = staging.Required(t.n.Source.Size)
			}
			extra := max(int64(0), required-allocated[t.id])
			if extra > capacity-used {
				continue
			}
			used += extra
			allocated[t.id] += extra
			active[t.id] = true
			count++
			work <- *t
			dispatched = true
		}
		if blocked > 0 && count == 0 && len(pending) == 0 && blocked == unresolved {
			return fmt.Errorf("%d items require intervention; progress retained", blocked)
		}
		if !dispatched && count == 0 && len(pending) > 0 {
			e.mu.Lock()
			paused := time.Now().Before(e.pause)
			e.mu.Unlock()
			retrying := false
			for _, t := range nodes {
				if t.n.State == "retry" {
					retrying = true
					break
				}
			}
			if !paused && !retrying {
				return errors.New("retained staging prevents progress; intervention required")
			}
		}
		if count == 0 && blocked > 0 { // Mark descendants of blocked parents explicitly, without stopping independent delayed tasks.
			changed := false
			for _, t := range nodes {
				if t.n.State == "completed" || t.n.State == "skipped" || t.n.State == "blocked" {
					continue
				}
				p := nodes[t.n.Parent]
				if t.n.Source.Path != "" && p != nil && p.n.State == "blocked" {
					t.n.State = "blocked"
					t.n.Reason = "parent requires intervention"
					if err = e.save(t.id, &t.n); err != nil {
						return err
					}
					changed = true
				}
			}
			if changed {
				continue
			}
		}
		select {
		case r := <-done:
			count--
			delete(active, r.id)
			t := nodes[r.n.Source.Path]
			t.n = r.n
			if r.err != nil {
				var api *googledrive.Error
				var temporary interface{ SourceTemporary() bool }
				var sourcePause interface{ RetryTime() time.Time }
				retry := errors.As(r.err, &api) && api.Temporary() || errors.As(r.err, &temporary) && temporary.SourceTemporary() || errors.As(r.err, &sourcePause)
				if errors.Is(r.err, context.Canceled) {
					return r.err
				}
				t.n.Reason = r.err.Error()
				if retry {
					t.n.State = "retry"
					t.n.Attempts++
					delay := time.Second * time.Duration(1<<min(t.n.Attempts, 8))
					if api != nil && api.Code != 0 {
						delay = max(delay, 30*time.Second)
					}
					delay = min(delay, 5*time.Minute)
					if e.retryDelay != nil {
						delay = e.retryDelay(t.n.Attempts)
					}
					t.n.RetryAt = time.Now().Add(delay)
					if sourcePause != nil && sourcePause.RetryTime().After(t.n.RetryAt) {
						t.n.RetryAt = sourcePause.RetryTime()
					}
					if api != nil && api.RetryAt.After(t.n.RetryAt) {
						t.n.RetryAt = api.RetryAt
					}
					if api != nil && (api.Code == 429 || api.Reason == "rateLimitExceeded" || api.Reason == "userRateLimitExceeded" || api.Reason == "dailyLimitExceeded") {
						if err = e.paused(t.n.RetryAt); err != nil {
							return err
						}
					}
				} else {
					t.n.State = "blocked"
				}
				if e.Auto {
					limit, _ = tuner.Observe(time.Now(), 0, false, true)
				}
				fmt.Fprintf(e.Progress, "%q: %s; state=%s, retry=%s\n", t.n.Source.Path, t.n.Reason, t.n.State, t.n.RetryAt.Format(time.RFC3339))
				if err = e.save(t.id, &t.n); err != nil {
					return err
				}
				if api != nil && (api.Code == 401 || api.Code == 403 && (api.Reason == "storageQuotaExceeded" || api.Reason == "insufficientPermissions")) {
					return r.err
				}
				var reconnect interface{ RequireReconnect() bool }
				if errors.As(r.err, &reconnect) && reconnect.RequireReconnect() {
					return r.err
				}
			} else {
				used -= allocated[t.id]
				delete(allocated, t.id)

			}
		case <-ticker.C:
			now := time.Now()
			seconds := max(0.001, now.Sub(lastMetrics).Seconds())
			lastMetrics = now
			transferred := e.sent.Swap(0)
			duration := time.Duration(e.latency.Swap(0))
			requests := int(e.requests.Swap(0))
			if e.Auto {
				pressure := used >= capacity && count < limit
				var reason string
				limit, reason = tuner.ObserveNetwork(time.Now(), transferred, duration, requests, pressure, false)
				if reason != "" {
					fmt.Fprintf(e.Progress, "Auto-workers: %d (%s)\n", limit, reason)
				}
			}
			e.mu.Lock()
			next := e.pause
			e.mu.Unlock()
			if !next.After(now) {
				next = time.Time{}
				for _, t := range nodes {
					if t.n.RetryAt.After(now) && (next.IsZero() || t.n.RetryAt.Before(next)) {
						next = t.n.RetryAt
					}
				}
			}
			waiting := time.Duration(0)
			if next.After(now) {
				waiting = next.Sub(now).Round(time.Second)
			}
			fmt.Fprintf(e.Progress, "Drive metrics: w:%d/%d %.1fMiB/s cache:%.1f/%.1fGiB wait:%s\n", count, limit, float64(transferred)/seconds/(1<<20), float64(used)/(1<<30), float64(capacity)/(1<<30), waiting)
			if time.Since(lastPrint) > 10*time.Second {
				fmt.Fprintf(e.Progress, "Drive progress: %d/%d entries, remaining %d, %.2f GiB confirmed, workers %d/%d, elapsed %s\n", completed, len(nodes), unresolved, float64(bytes)/(1<<30), count, limit, time.Since(started).Round(time.Second))
				lastPrint = time.Now()
			}
		case <-ctx.Done():
		}
	}
}
