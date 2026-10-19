package migration

import (
	"context"
	"errors"
	"fmt"
	"path"
	"time"

	"od2gp/internal/googlephotos"
	"od2gp/internal/journal"
)

type batchPhotos interface {
	CreateBatch(context.Context, []googlephotos.NewItem) ([]googlephotos.CreateResult, error)
}

func (e *Engine) saveTasks(tasks []transferTask) error {
	records := make([]journal.Record, len(tasks))
	for i, task := range tasks {
		records[i] = journal.Record{ID: task.id, State: task.item.State, Value: task.item}
	}
	return e.Store.SaveUpdates(records)
}

// The coordinator is the sole committer. Each prepared batch and its outcomes
// are atomically checkpointed; encrypted staged bytes survive ambiguous results.
func (e *Engine) commitBatch(ctx context.Context, meta *Meta, tasks []transferTask) error {
	photos := e.Photos.(batchPhotos)
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		requests := make([]googlephotos.NewItem, len(tasks))
		for i := range tasks {
			item := &tasks[i].item
			if item.Token == "" || time.Since(item.TokenTime) > 23*time.Hour {
				// Only this token must be refreshed. Other uploaded items remain queued.
				item.State = "staged"
				item.Upload = googlephotos.Session{}
				item.Token = ""
				if err := e.Store.Save(tasks[i].id, item.State, item); err != nil {
					return err
				}
				return errors.New("queued upload token missing or expired; resume to upload this unfinished file again")
			}
			requests[i] = googlephotos.NewItem{Token: item.Token, Name: path.Base(item.Path)}
		}
		// A queued request cancelled before dispatch must not become uncertain.
		if gate, ok := e.Photos.(*parallelPhotos); ok {
			if err := gate.gate(ctx); err != nil {
				return err
			}
		}
		for i := range tasks {
			tasks[i].item.State = "creating"
			tasks[i].item.Reason = ""
		}
		if err := e.saveTasks(tasks); err != nil {
			return err
		}
		started := time.Now()
		results, requestErr := photos.CreateBatch(ctx, requests)
		fmt.Fprintf(e.Progress, "Create batch: %d items, %.2fs.\n", len(tasks), time.Since(started).Seconds())
		var retry []transferTask
		var permanent, uncertain error
		var retryAPI *googlephotos.APIError
		var retryError error
		if requestErr != nil {
			var api *googlephotos.APIError
			safe := errors.As(requestErr, &api) && (api.Code == 401 || api.Code == 403 || api.Code == 429)
			for i := range tasks {
				tasks[i].item.Reason = requestErr.Error()
				if safe {
					tasks[i].item.State = "uploaded"
				}
			}
			if err := e.saveTasks(tasks); err != nil {
				return err
			}
			if !safe || api.Code != 429 {
				// Save provider pauses, but never auto-retry an ambiguous creation.
				if api != nil && (api.Code == 429 || api.Code >= 500) {
					return e.scheduleRetry(ctx, meta, api, attempt, true, requestErr)
				}
				return requestErr
			}
			retry = tasks
			retryAPI = api
			retryError = requestErr
		} else {
			if len(results) != len(tasks) {
				results = make([]googlephotos.CreateResult, len(tasks))
				for i := range results {
					results[i].Err = &googlephotos.OutcomeUnknownError{}
				}
			}
			for i := range tasks {
				item := &tasks[i].item
				result := results[i]
				if result.Err == nil && result.Media.ID != "" {
					item.MediaID = result.Media.ID
					item.State = "created"
					item.Token = ""
					item.Upload = googlephotos.Session{}
					item.Reason = ""
					continue
				}
				var rejected *googlephotos.ItemError
				if errors.As(result.Err, &rejected) {
					item.State = "uploaded"
					item.Reason = rejected.Error()
					switch rejected.Code {
					case 8, 13, 14:
						retry = append(retry, tasks[i])
						retryError = rejected
					default:
						if permanent == nil {
							permanent = rejected
						}
					}
				} else {
					item.Reason = (&googlephotos.OutcomeUnknownError{}).Error()
					uncertain = &googlephotos.OutcomeUnknownError{}
				}
			}
			// Commit every known success before cleanup, retries or returning any failure.
			if err := e.saveTasks(tasks); err != nil {
				return err
			}
			for i := range tasks {
				if tasks[i].item.State == "created" {
					if err := e.transferWithRetries(ctx, meta, tasks[i].id, &tasks[i].item); err != nil && permanent == nil {
						permanent = err
					}
				}
			}
			if uncertain != nil {
				return uncertain
			}
			if permanent != nil {
				return permanent
			}
			if len(retry) == 0 {
				return nil
			}
		}
		if err := e.scheduleRetry(ctx, meta, retryAPI, attempt, false, retryError); err != nil {
			return err
		}
		tasks = retry
	}
}
