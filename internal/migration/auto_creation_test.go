package migration

import (
	"context"
	"fmt"
	"od2gp/internal/googlephotos"
	"reflect"
	"testing"
	"time"
)

func TestAutomaticBatchCreationRetryKeepsTokens(t *testing.T) {
	e, meta, p := batchFixture(t, 6)
	e.AutoRetryCreation, e.MaxRetries = true, 2
	waits := 0
	e.retryWait = func(context.Context, time.Duration) error { waits++; return nil }
	p.response = func(items []googlephotos.NewItem) ([]googlephotos.CreateResult, error) {
		if len(p.batches) == 1 {
			return nil, &googlephotos.APIError{Code: 503}
		}
		if !reflect.DeepEqual(items, p.batches[0]) {
			t.Fatal("changed tokens on retry")
		}
		result := make([]googlephotos.CreateResult, len(items))
		for i := range result {
			result[i].Media.ID = fmt.Sprint(i + 1)
		}
		return result, nil
	}
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if waits != 1 || len(p.batches) != 2 {
		t.Fatalf("waits=%d batches=%d", waits, len(p.batches))
	}
	for id := int64(1); id <= 6; id++ {
		var item Item
		if err := e.Store.Load(id, &item); err != nil || item.State != "completed" {
			t.Fatalf("not completed: %v %+v", err, item)
		}
	}
}

func TestAutomaticCreationRetryBudgetAndResume(t *testing.T) {
	e, meta, p := batchFixture(t, 6)
	e.AutoRetryCreation, e.MaxRetries = true, 1
	e.retryWait = func(context.Context, time.Duration) error { return nil }
	p.response = func([]googlephotos.NewItem) ([]googlephotos.CreateResult, error) {
		return nil, &googlephotos.APIError{Code: 503}
	}
	if err := e.Run(context.Background(), meta); err == nil {
		t.Fatal("ignored exhausted retry budget")
	}
	if len(p.batches) != 2 {
		t.Fatalf("unbounded retries: %d", len(p.batches))
	}
	original := p.batches[0]
	meta.RetryAt = time.Time{}
	p.response = func(items []googlephotos.NewItem) ([]googlephotos.CreateResult, error) {
		tokens := map[string]string{}
		for _, item := range original {
			tokens[item.Name] = item.Token
		}
		for _, item := range items {
			if tokens[item.Name] != item.Token {
				return nil, fmt.Errorf("resume changed token for %s", item.Name)
			}
		}
		r := make([]googlephotos.CreateResult, len(items))
		for i := range r {
			r[i].Media.ID = fmt.Sprint(i + 1)
		}
		return r, nil
	}
	if err := e.Run(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticSerialCreationRetryDoesNotUploadAgain(t *testing.T) {
	for _, failure := range []error{&googlephotos.APIError{Code: 503}, &googlephotos.ConnectionError{}} {
		t.Run(failure.Error(), func(t *testing.T) {
			e, meta, source, p, closeStore := fixture(t)
			defer closeStore()
			flaky := &flakyPhotos{photosFake: p, createError: failure, createFailures: 1}
			e.Photos, e.AutoRetryCreation, e.MaxRetries = flaky, true, 2
			e.retryWait = func(context.Context, time.Duration) error { return nil }
			if err := e.Run(context.Background(), meta); err != nil {
				t.Fatal(err)
			}
			if flaky.createCalls != 2 || p.starts != 1 || source.reads != 1 {
				t.Fatalf("reuploaded: calls=%d starts=%d reads=%d", flaky.createCalls, p.starts, source.reads)
			}
		})
	}
}

func TestAutomaticCreationResumeRejectsExpiredToken(t *testing.T) {
	e, meta, p := batchFixture(t, 6)
	e.AutoRetryCreation = true
	p.response = func([]googlephotos.NewItem) ([]googlephotos.CreateResult, error) {
		return nil, &googlephotos.APIError{Code: 503}
	}
	if err := e.Run(context.Background(), meta); err == nil {
		t.Fatal("ignored failed create")
	}
	meta.RetryAt = time.Time{}
	for id := int64(1); id <= 6; id++ {
		var item Item
		if err := e.Store.Load(id, &item); err != nil {
			t.Fatal(err)
		}
		item.TokenTime = time.Now().Add(-24 * time.Hour)
		if err := e.Store.Save(id, item.State, item); err != nil {
			t.Fatal(err)
		}
	}
	before := len(p.batches)
	if err := e.Run(context.Background(), meta); err == nil {
		t.Fatal("retried expired uncertain creation")
	}
	if len(p.batches) != before {
		t.Fatal("sent expired token")
	}
}
