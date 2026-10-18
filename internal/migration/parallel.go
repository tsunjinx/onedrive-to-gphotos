package migration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"od2gp/internal/googlephotos"
	"od2gp/internal/staging"
)

type lockedProgress struct {
	mu  sync.Mutex
	out io.Writer
}

func (w *lockedProgress) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.out.Write(p)
}

type transferTask struct {
	id       int64
	item     Item
	reserved int64
}
type transferResult struct {
	task transferTask
	err  error
}

// The coordinator alone owns staging reservations, including files left by a
// previous process. Workers retain their reservation until they finish or stop.
func (e *Engine) runParallel(ctx context.Context, meta *Meta, ids []int64) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	originalPhotos, originalProgress := e.Photos, e.Progress
	e.Progress = &lockedProgress{out: originalProgress}
	e.Photos = &parallelPhotos{Photos: originalPhotos, e: e, meta: meta}
	defer func() { e.Photos = originalPhotos; e.Progress = originalProgress }()
	files, err := filepath.Glob(filepath.Join(e.Directory, "stage-*.bin"))
	if err != nil {
		return err
	}
	allocated := make(map[int64]int64)
	known := make(map[string]int64, len(ids))
	for _, id := range ids {
		known[filepath.Join(e.Directory, fmt.Sprintf("stage-%d.bin", id))] = id
	}
	var used int64
	for _, file := range files {
		info, err := os.Lstat(file)
		if err != nil {
			return err
		}
		id, ok := known[file]
		if !ok || !info.Mode().IsRegular() {
			return errors.New("unrecognized staging file; review saved job before continuing")
		}
		allocated[id] = info.Size()
		used += info.Size()
	}
	if used > meta.CacheBytes {
		return errors.New("saved staging exceeds cache capacity; no transfers started")
	}
	free, err := staging.Free(e.Directory)
	if err != nil {
		return errors.New("cannot check available cache disk space")
	}
	if free < 128<<20 {
		return errors.New("insufficient free disk space for journal reserve")
	}
	// Reserve space for downloads not yet physically written by other workers.
	capacity := min(meta.CacheBytes, used+int64(free-(128<<20)))
	var pending []transferTask
	for _, id := range ids {
		var item Item
		if err := e.Store.Load(id, &item); err != nil {
			return err
		}
		if item.State == "creating" && !e.Reconcile {
			return errors.New("destination creation outcome is uncertain; use resume --reconcile after reviewing status to retry the identical staged content")
		}
		if item.State == "completed" || item.State == "skipped" || item.State == "processing_failed" {
			continue
		}
		required := staging.Required(item.Source.Size)
		if item.State == "created" || item.State == "processing" {
			required = allocated[id]
		}
		required = max(required, allocated[id])
		if required > meta.CacheBytes {
			return errors.New("file exceeds cache capacity; create a new plan with a larger --cache-gib")
		}
		pending = append(pending, transferTask{id: id, item: item, reserved: required})
	}
	tasks := make(chan transferTask)
	results := make(chan transferResult, e.Workers)
	var workers sync.WaitGroup
	for range e.Workers {
		workers.Go(func() {
			for task := range tasks {
				fmt.Fprintf(e.Progress, "[%d/%d] %q: %s\n", task.id, len(ids), task.item.Path, task.item.State)
				err := e.transferWithRetries(ctx, meta, task.id, &task.item)
				if err != nil {
					cancel(fmt.Errorf("file %d: %w", task.id, err))
				}
				results <- transferResult{task: task, err: err}
			}
		})
	}
	defer func() { close(tasks); workers.Wait() }()
	fmt.Fprintf(e.Progress, "Transfers: up to %d parallel files; shared staging cap %.1f GiB. Library creation stays sequential.\n", e.Workers, float64(meta.CacheBytes)/(1<<30))
	active := 0
	var firstErr error
	for len(pending) > 0 || active > 0 {
		if ctx.Err() != nil && firstErr == nil {
			firstErr = context.Cause(ctx)
			cancel(firstErr)
		}
		// Look for any fitting file, so a large waiting file cannot prevent existing
		// staged files from finishing and releasing their reservations.
		index := -1
		if firstErr == nil && active < e.Workers {
			for i, task := range pending {
				if task.reserved-allocated[task.id] <= capacity-used {
					index = i
					break
				}
			}
		}
		if index >= 0 {
			task := pending[index]
			select {
			case tasks <- task:
				used += task.reserved - allocated[task.id]
				allocated[task.id] = task.reserved
				if index == 0 {
					pending = pending[1:]
				} else {
					pending = append(pending[:index], pending[index+1:]...)
				}
				active++
			case <-ctx.Done():
				firstErr = context.Cause(ctx)
				cancel(firstErr)
			}
			continue
		}
		if active == 0 {
			if firstErr != nil {
				return firstErr
			}
			return errors.New("staging capacity is occupied by saved files; no pending transfer fits")
		}
		result := <-results
		active--
		if result.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("file %d: %w", result.task.id, result.err)
			cancel(firstErr)
		}
		file := filepath.Join(e.Directory, fmt.Sprintf("stage-%d.bin", result.task.id))
		var remaining int64
		info, statErr := os.Lstat(file)
		if statErr == nil {
			remaining = info.Size()
		} else if !errors.Is(statErr, os.ErrNotExist) && firstErr == nil {
			firstErr = statErr
			cancel(firstErr)
		}
		used += remaining - allocated[result.task.id]
		allocated[result.task.id] = remaining
	}
	return firstErr
}

// Raw upload requests may overlap. Google recommends serial media creation for
// each account. Every new Google request observes the shared Retry-After pause.
type parallelPhotos struct {
	Photos
	e        *Engine
	meta     *Meta
	createMu sync.Mutex
}

func (p *parallelPhotos) gate(ctx context.Context) error {
	p.e.retryMu.Lock()
	defer p.e.retryMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.meta.RetryAt.IsZero() {
		return nil
	}
	if p.e.MaxRetries == 0 {
		return errors.New("provider requested a pause; resume later")
	}
	return p.e.awaitRetry(ctx, p.meta)
}
func (p *parallelPhotos) Start(ctx context.Context, mime string, size int64) (googlephotos.Session, error) {
	if err := p.gate(ctx); err != nil {
		return googlephotos.Session{}, err
	}
	return p.Photos.Start(ctx, mime, size)
}
func (p *parallelPhotos) Query(ctx context.Context, s googlephotos.Session) (int64, string, error) {
	if err := p.gate(ctx); err != nil {
		return 0, "", err
	}
	return p.Photos.Query(ctx, s)
}
func (p *parallelPhotos) Chunk(ctx context.Context, s googlephotos.Session, offset int64, data []byte, final bool) (string, error) {
	if err := p.gate(ctx); err != nil {
		return "", err
	}
	return p.Photos.Chunk(ctx, s, offset, data, final)
}
func (p *parallelPhotos) Create(ctx context.Context, token, name string) (googlephotos.Media, error) {
	p.createMu.Lock()
	defer p.createMu.Unlock()
	if err := p.gate(ctx); err != nil {
		return googlephotos.Media{}, err
	}
	return p.Photos.Create(ctx, token, name)
}
func (p *parallelPhotos) Get(ctx context.Context, id string) (googlephotos.Media, error) {
	if err := p.gate(ctx); err != nil {
		return googlephotos.Media{}, err
	}
	return p.Photos.Get(ctx, id)
}
