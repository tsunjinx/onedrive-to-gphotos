package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestProgressResumeETAAndReview(t *testing.T) {
	c := map[string]int{"completed": 100, "discovered": 100, "skipped": 5}
	if s := progressLine(c, 100, time.Minute); !strings.Contains(s, "ETA:estimating") {
		t.Fatal(s)
	}
	c["completed"] = 110
	c["discovered"] = 90
	if s := progressLine(c, 100, time.Minute); !strings.Contains(s, "left:90") || !strings.Contains(s, "ETA:9m00s") {
		t.Fatal(s)
	}
	c["needs_review"] = 1
	c["discovered"] = 89
	if s := progressLine(c, 100, time.Minute); !strings.Contains(s, "review:1") || strings.Contains(s, "100%") {
		t.Fatal(s)
	}
}
func TestProgressKeepsErrorsAndHidesRoutineLines(t *testing.T) {
	var out bytes.Buffer
	d := &transferDisplay{out: &out}
	d.Write([]byte("[1] Uploaded 30 / 30 bytes\nQueue batch: 3 items\n"))
	if out.Len() != 0 {
		t.Fatal(out.String())
	}
	d.Write([]byte("[2] file: needs review; staging retained\nWaiting until tomorrow\n"))
	if !strings.Contains(out.String(), "needs review") || !strings.Contains(out.String(), "Waiting until") {
		t.Fatal(out.String())
	}
}

func TestProgressFitsNarrowWindow(t *testing.T) {
	var out bytes.Buffer
	d := &transferDisplay{out: &out, columns: func() int { return 35 }, started: time.Now()}
	d.render(map[string]int{"discovered": 22350}, time.Now())
	if len([]rune(d.last)) >= 35 || strings.Contains(out.String(), "\n") {
		t.Fatal(out.String())
	}
}
