package msgraph

import (
	"context"
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
