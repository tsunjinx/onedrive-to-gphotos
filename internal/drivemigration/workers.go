package drivemigration

import "time"

// Tuner evaluates aggregate acknowledged bytes over windows, with cooldown and trial rollback.
type Tuner struct {
	Limit          int
	start, changed time.Time
	bytes          int64
	previous       float64
	trial          bool
}

func NewTuner(now time.Time) *Tuner { return &Tuner{Limit: 3, start: now, changed: now} }
func (t *Tuner) Observe(now time.Time, bytes int64, pressure, failed bool) (int, string) {
	t.bytes += bytes
	if failed || pressure {
		if t.Limit > 1 {
			t.Limit--
			t.changed = now
			t.start = now
			t.bytes = 0
			t.trial = false
			return t.Limit, "backoff or staging pressure"
		}
		return t.Limit, ""
	}
	elapsed := now.Sub(t.start)
	if elapsed < 20*time.Second || now.Sub(t.changed) < 20*time.Second {
		return t.Limit, ""
	}
	rate := float64(t.bytes) / elapsed.Seconds()
	t.bytes = 0
	t.start = now
	if rate <= 0 {
		return t.Limit, ""
	}
	if t.trial {
		t.trial = false
		if rate < t.previous*1.05 {
			t.Limit = max(1, t.Limit-1)
			t.changed = now
			return t.Limit, "higher concurrency did not improve throughput"
		}
	}
	if t.Limit < 8 {
		t.previous = rate
		t.trial = true
		t.Limit++
		t.changed = now
		return t.Limit, "stable measured throughput; testing one additional worker"
	}
	return t.Limit, ""
}
