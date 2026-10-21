package msgraph

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func graphTemporary(code int) bool { return code == 429 || code == 502 || code == 503 || code == 504 }
func graphRetryDelay(header string, attempt int, now time.Time) time.Duration {
	fallback := time.Second*time.Duration(1<<min(attempt, 8)) + time.Duration(rand.Int64N(int64(time.Second)))
	if seconds, err := strconv.ParseInt(strings.TrimSpace(header), 10, 64); err == nil && seconds >= 0 {
		// Saturate only at Duration limits to avoid overflow. Never shorten ordinary server pauses.
		if seconds > int64((1<<63-1)/time.Second) {
			return time.Duration(1<<63 - 1)
		}
		return max(time.Duration(seconds)*time.Second, fallback)
	}
	if until, err := http.ParseTime(header); err == nil && until.After(now) {
		return max(until.Sub(now), fallback)
	}
	return fallback
}
func waitGraph(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// PauseError exposes only a safe deadline, so jobs can persist source throttling.
type PauseError struct{ Until time.Time }

func (e *PauseError) Error() string {
	return fmt.Sprintf("Microsoft Graph requested a pause; resume after %s", e.Until.Local().Format(time.RFC3339))
}
func (e *PauseError) RetryTime() time.Time { return e.Until }

// A full response to a range request must never be appended to retained chunks.
type RangeUnsupportedError struct{}

func (*RangeUnsupportedError) Error() string         { return "source ignored the download range" }
func (*RangeUnsupportedError) RestartDownload() bool { return true }

// ConfigureRetry is called before starting workers. Both callbacks coordinate
// with the owning job; they must be concurrency-safe and omit credentials.
func (c *Client) ConfigureRetry(gate func(context.Context) error, persist func(time.Time) error) {
	c.retryGate = gate
	c.retryPause = persist
}
func (c Client) pauseRequestRetry(ctx context.Context, delay time.Duration, attempt int) error {
	until := time.Now().Add(delay)
	if c.retryPause != nil {
		if err := c.retryPause(until); err != nil {
			return err
		}
	}
	if attempt >= 3 || delay > 15*time.Minute {
		return &PauseError{Until: until}
	}
	if c.retryGate != nil {
		return nil
	} // Next request waits on the shared durable deadline.
	wait := c.retryWait
	if wait == nil {
		wait = waitGraph
	}
	if err := wait(ctx, delay); err != nil {
		return errors.Join(err, &PauseError{Until: until})
	}
	return nil
}

// Download retries must reopen and authenticate staging before writing again.
// A staging write error never uses this type.
type TemporaryDownloadError struct{ Until time.Time }

func (e *TemporaryDownloadError) Error() string {
	return "OneDrive download temporarily interrupted; retained encrypted chunks can be resumed"
}
func (e *TemporaryDownloadError) RetryTime() time.Time { return e.Until }
func (*TemporaryDownloadError) SourceTemporary() bool  { return true }
func (c Client) downloadFailure(header string) error {
	until := time.Now().Add(graphRetryDelay(header, 0, time.Now()))
	if c.retryPause != nil {
		if err := c.retryPause(until); err != nil {
			return err
		}
	}
	return &TemporaryDownloadError{Until: until}
}
