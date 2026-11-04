package drivemigration

import (
	"testing"
	"time"
)

func TestAutoWorkersDoesNotRecoverAfterOneSuccessfulWindow(t *testing.T) {
	now := time.Now()
	tuner := NewTuner(now)
	tuner.Observe(now, 0, false, true)
	if n, _ := tuner.ObserveNetwork(now.Add(21*time.Second), 1<<20, time.Second, 1, false, false); n != 2 {
		t.Fatalf("increased too soon after interrupted transfer: %d", n)
	}
}

func TestAutoWorkersHoldsDuringLongRetryAndResumesAfterRecovery(t *testing.T) {
	now := time.Now()
	tuner := NewTuner(now)
	tuner.Observe(now, 0, false, true)
	// Other files making progress must not override a five-minute retry deadline.
	for i := 1; i <= 15; i++ {
		if n, _ := tuner.ObserveTransfer(now.Add(time.Duration(i)*20*time.Second), 1<<20, time.Second, 1, false, true); n != 2 {
			t.Fatalf("scaled with retries pending: %d", n)
		}
	}
	if n, _ := tuner.ObserveTransfer(now.Add(321*time.Second), 1<<20, time.Second, 1, false, false); n != 3 {
		t.Fatalf("did not recover after pause: %d", n)
	}
}
func TestAutoWorkersFailuresAtMinimumRefreshRecovery(t *testing.T) {
	now := time.Now()
	tuner := NewTuner(now)
	tuner.Observe(now, 0, false, true)
	tuner.Observe(now, 0, false, true)
	tuner.Observe(now.Add(110*time.Second), 0, false, true)
	if n, _ := tuner.ObserveNetwork(now.Add(140*time.Second), 1<<20, time.Second, 1, false, false); n != 1 {
		t.Fatalf("failure at minimum ignored: %d", n)
	}
	if n, _ := tuner.ObserveNetwork(now.Add(231*time.Second), 1<<20, time.Second, 1, false, false); n != 2 {
		t.Fatalf("stuck at minimum after recovery: %d", n)
	}
}
