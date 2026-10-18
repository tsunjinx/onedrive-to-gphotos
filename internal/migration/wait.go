package migration

import (
	"context"
	"fmt"
	"time"
)

// WaitForVideos checks saved processing items until all reach a terminal state.
// Run must succeed first; this does not automatically retry interrupted uploads.
func (e *Engine) WaitForVideos(ctx context.Context, meta *Meta) error {
	return e.waitForVideos(ctx, meta, waitContext)
}

func (e *Engine) waitForVideos(ctx context.Context, meta *Meta, wait func(context.Context, time.Duration) error) error {
	for delay := 30 * time.Second; ; delay = min(delay*2, 5*time.Minute) {
		if err := ctx.Err(); err != nil {
			return err
		}
		ids, err := e.Store.IDs()
		if err != nil {
			return err
		}
		pending := 0
		for _, id := range ids {
			var item Item
			if err := e.Store.Load(id, &item); err != nil {
				return err
			}
			if item.State == "processing" {
				pending++
			}
		}
		if pending == 0 {
			return nil
		}
		fmt.Fprintf(e.Progress, "%d videos still processing; checking again in %s. Ctrl+C saves progress and exits.\n", pending, delay)
		if err := wait(ctx, delay); err != nil {
			return err
		}
		if err := e.Run(ctx, meta); err != nil {
			return err
		}
	}
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
