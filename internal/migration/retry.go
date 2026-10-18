package migration

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"od2gp/internal/googlephotos"
)

func (e *Engine) transferWithRetries(ctx context.Context, meta *Meta, id int64, item *Item) error {
	for attempt := 0; ; attempt++ {
		err := e.transfer(ctx, meta, id, item)
		if err == nil {
			return nil
		}
		// Only the durable checkpoint may be used for recovery or diagnostics.
		var committed Item
		if loadErr := e.Store.Load(id, &committed); loadErr != nil {
			return fmt.Errorf("cannot read last committed failure state: %w", loadErr)
		}
		committed.Reason = err.Error()
		if saveErr := e.Store.Save(id, committed.State, committed); saveErr != nil {
			return fmt.Errorf("cannot save failure state: %w", saveErr)
		}
		var api *googlephotos.APIError
		var connection *googlephotos.ConnectionError
		retryable := errors.As(err, &connection)
		if errors.As(err, &api) {
			retryable = api.Code == 429 || (api.Code >= 500 && api.Code < 600)
		}
		if !retryable {
			return err
		}
		if retryErr := e.scheduleRetry(ctx, meta, api, attempt, committed.State == "creating", err); retryErr != nil {
			return retryErr
		}
		*item = committed
	}
}

func (e *Engine) awaitRetry(ctx context.Context, meta *Meta) error {
	delay := time.Until(meta.RetryAt)
	if delay > 15*time.Minute {
		return fmt.Errorf("provider pause is longer than the automatic wait limit; resume after %s", meta.RetryAt.Local().Format(time.RFC3339))
	}
	if delay > 0 {
		fmt.Fprintf(e.Progress, "Waiting until %s before retrying. Ctrl+C preserves this pause and progress.\n", meta.RetryAt.Local().Format(time.RFC3339))
		wait := e.retryWait
		if wait == nil {
			wait = waitContext
		}
		if err := wait(ctx, delay); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	meta.RetryAt = time.Time{}
	return e.Store.Save(0, "job", meta)
}

// All workers share the persisted provider pause. Keep the longest requested delay.
func (e *Engine) scheduleRetry(ctx context.Context, meta *Meta, api *googlephotos.APIError, attempt int, uncertain bool, original error) error {
	e.retryMu.Lock()
	defer e.retryMu.Unlock()
	delay := min(30*time.Second*time.Duration(1<<attempt), 5*time.Minute)
	delay += time.Duration(rand.Int64N(int64(time.Second)))
	until := time.Now().Add(delay).UTC()
	if api != nil && api.RetryAt.After(until) {
		until = api.RetryAt
	}
	if until.After(meta.RetryAt) {
		meta.RetryAt = until
	}
	if err := e.Store.Save(0, "job", meta); err != nil {
		return err
	}
	if uncertain || attempt >= e.MaxRetries {
		return original
	}
	fmt.Fprintf(e.Progress, "Temporary Google Photos failure; retry %d/%d for this file.\n", attempt+1, e.MaxRetries)
	return e.awaitRetry(ctx, meta)
}
