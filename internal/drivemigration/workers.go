package drivemigration

import "time"

// Tuner evaluates aggregate acknowledged bytes over windows, with cooldown and trial rollback.
type Tuner struct {
	latency         time.Duration
	samples         int
	previousLatency time.Duration
	Limit           int
	start, changed  time.Time
	bytes           int64
	previous        float64
	trial           bool
	recoverUntil    time.Time
}

func NewTuner(now time.Time) *Tuner { return &Tuner{Limit: 3, start: now, changed: now} }
func (t *Tuner) Observe(now time.Time, bytes int64, pressure, failed bool) (int, string) {
	t.bytes += bytes
	if failed || pressure {
		// Every failure refreshes recovery, including failures at the minimum.
		t.recoverUntil = now.Add(2 * time.Minute)
		t.hold(now)
		if t.Limit > 1 {
			t.Limit--
			return t.Limit, "backoff or staging pressure"
		}
		return t.Limit, ""
	}
	elapsed := now.Sub(t.start)
	if elapsed < 20*time.Second || now.Sub(t.changed) < 20*time.Second {
		return t.Limit, ""
	}
	average := time.Duration(0)
	if t.samples > 0 {
		average = t.latency / time.Duration(t.samples)
	}
	t.latency = 0
	t.samples = 0
	rate := float64(t.bytes) / elapsed.Seconds()
	t.bytes = 0
	t.start = now
	if rate <= 0 {
		return t.Limit, ""
	}
	if t.trial {
		t.trial = false
		if rate < t.previous*1.05 || t.previousLatency > 0 && average > 2*t.previousLatency && rate < t.previous*1.2 {
			t.Limit = max(1, t.Limit-1)
			t.changed = now
			return t.Limit, "higher concurrency did not improve throughput"
		}
	}
	if now.Before(t.recoverUntil) {
		return t.Limit, ""
	}
	if t.Limit < 8 {
		t.previous = rate
		t.previousLatency = average
		t.trial = true
		t.Limit++
		t.changed = now
		return t.Limit, "stable measured throughput; testing one additional worker"
	}
	return t.Limit, ""
}

func (t *Tuner) ObserveNetwork(now time.Time, bytes int64, latency time.Duration, samples int, pressure, failed bool) (int, string) {
	t.latency += latency
	t.samples += samples
	return t.Observe(now, bytes, pressure, failed)
}

// hold discards samples gathered during provider pauses or retry backoff. They
// must not be interpreted as a healthy throughput trial when a pause expires.
func (t *Tuner) hold(now time.Time) {
	t.start = now
	t.changed = now
	t.bytes = 0
	t.latency = 0
	t.samples = 0
	t.trial = false
}
func (t *Tuner) ObserveTransfer(now time.Time, bytes int64, latency time.Duration, samples int, pressure, recovering bool) (int, string) {
	if recovering {
		t.hold(now)
		return t.Limit, ""
	}
	return t.ObserveNetwork(now, bytes, latency, samples, pressure, false)
}
