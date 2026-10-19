package msgraph

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestGraphRetriesReadOnlyRequestsAndHonorsPause(t *testing.T) {
	calls, waits := 0, 0
	c := Client{HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" {
			t.Fatal("retry must be read only")
		}
		resp := response(`{"id":"drive"}`)
		if calls == 1 {
			resp.StatusCode = 429
			resp.Header.Set("Retry-After", "90")
		}
		if calls == 2 {
			resp.StatusCode = 503
		}
		return resp, nil
	})}, retryWait: func(ctx context.Context, d time.Duration) error {
		waits++
		if waits == 1 && d < 90*time.Second {
			t.Fatalf("shortened pause: %v", d)
		}
		return nil
	}}
	if _, err := c.DefaultDrive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || waits != 2 {
		t.Fatalf("calls=%d waits=%d", calls, waits)
	}
}

func TestGraphRetryBoundCancellationAndPermanentErrors(t *testing.T) {
	for _, code := range []int{401, 403, 404, 429, 503} {
		calls := 0
		c := Client{HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
			calls++
			r := response("")
			r.StatusCode = code
			return r, nil
		})}, retryWait: func(context.Context, time.Duration) error { return nil }}
		if _, err := c.DefaultDrive(context.Background()); err == nil {
			t.Fatal("accepted failed request")
		}
		want := 1
		if code == 429 || code == 503 {
			want = 4
		}
		if calls != want {
			t.Fatalf("HTTP %d: calls=%d", code, calls)
		}
	}
	c := Client{HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { r := response(""); r.StatusCode = 429; return r, nil })}, retryWait: func(context.Context, time.Duration) error { return context.Canceled }}
	if _, err := c.DefaultDrive(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestGraphLongPauseAndRetryDate(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	if d := graphRetryDelay("172800", 0, now); d != 48*time.Hour {
		t.Fatalf("long pause shortened: %v", d)
	}
	if d := graphRetryDelay(now.Add(2*time.Minute).Format(http.TimeFormat), 0, now); d != 2*time.Minute {
		t.Fatalf("date delay=%v", d)
	}
	calls := 0
	c := Client{HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		calls++
		r := response("")
		r.StatusCode = 429
		r.Header.Set("Retry-After", "3600")
		return r, nil
	})}, retryWait: func(context.Context, time.Duration) error { t.Fatal("long pause must stop"); return nil }}
	if _, err := c.DefaultDrive(context.Background()); err == nil || calls != 1 {
		t.Fatalf("long pause ignored: %v", err)
	}
}
