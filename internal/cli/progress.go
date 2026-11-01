package cli

import (
	"fmt"
	"io"
	"od2gp/internal/journal"
	"os"
	"strings"
	"sync"
	"time"
)

// A file-based ETA estimates the remaining transfer pass, not Google's video
// processing time. Existing completed records do not inflate this run's rate.
type transferDisplay struct {
	columns  func() int
	mu       sync.Mutex
	out      io.Writer
	store    *journal.Store
	started  time.Time
	baseline int
	language string
	last     string
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
}

func startTransferDisplay(store *journal.Store, out io.Writer, verbose bool, language string) (io.Writer, func()) {
	file, ok := out.(*os.File)
	if verbose || !ok || !terminalInput(file) || os.Getenv("TERM") == "dumb" {
		return out, func() {}
	}
	restore, ok := enableProgressOutput(file)
	if !ok {
		return out, func() {}
	}
	counts, err := store.StateCounts()
	if err != nil {
		restore()
		return out, func() {}
	}
	d := &transferDisplay{columns: func() int { return progressColumns(file) }, language: language, out: out, store: store, started: time.Now(), baseline: sentCount(counts), stop: make(chan struct{}), done: make(chan struct{})}
	if language == "vi" {
		fmt.Fprintln(out, "Tiến độ gửi tệp; ETA chưa gồm thời gian Google xử lý video.")
	} else {
		fmt.Fprintln(out, "File transfer progress; ETA excludes Google video processing.")
	}
	d.render(counts, time.Now())
	go func() {
		defer close(d.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-d.stop:
				return
			case now := <-ticker.C:
				counts, err := store.StateCounts()
				if err == nil {
					d.render(counts, now)
				}
			}
		}
	}()
	closeDisplay := func() {
		d.once.Do(func() {
			close(d.stop)
			<-d.done
			d.mu.Lock()
			fmt.Fprint(d.out, "\r\x1b[2K")
			d.mu.Unlock()
			restore()
		})
	}
	return d, closeDisplay
}
func sentCount(c map[string]int) int {
	return c["completed"] + c["processing"] + c["processing_failed"]
}
func progressLine(c map[string]int, baseline int, elapsed time.Duration) string {
	total := 0
	for state, n := range c {
		if state != "skipped" {
			total += n
		}
	}
	sent := sentCount(c)
	remaining := max(0, total-sent)
	transferable := max(0, remaining-c["needs_review"])
	percent := 0
	if total > 0 {
		percent = 100 * sent / total
	}
	filled := min(10, percent/10)
	eta := "estimating"
	gained := sent - baseline
	if transferable == 0 {
		eta = "0s"
	} else if elapsed >= 10*time.Second && gained >= 3 {
		eta = shortDuration(time.Duration(float64(elapsed) * float64(transferable) / float64(gained)))

	}
	return fmt.Sprintf("[%s%s] %3d%% left:%d review:%d ETA:%s elapsed:%s", strings.Repeat("#", filled), strings.Repeat("-", 10-filled), percent, remaining, c["needs_review"], eta, shortDuration(elapsed))
}
func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}
func (d *transferDisplay) render(c map[string]int, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.last = progressLine(c, d.baseline, now.Sub(d.started))
	if d.language == "vi" {
		d.last = strings.NewReplacer("left:", "còn:", "review:", "xem lại:", "elapsed:", "đã chạy:", "estimating", "đang tính").Replace(d.last)
	}
	if d.columns != nil {
		width := max(1, d.columns()-1)
		runes := []rune(d.last)
		if len(runes) > width {
			d.last = string(runes[:width])
		}
	}
	fmt.Fprintf(d.out, "\r\x1b[2K%s", d.last)
}
func (d *transferDisplay) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, line := range strings.Split(strings.TrimSpace(string(p)), "\n") {
		line = strings.TrimSpace(line)
		routine := strings.HasPrefix(line, "Queue batch:") || strings.HasPrefix(line, "Create batch:") || strings.HasPrefix(line, "Transfers:") || strings.HasPrefix(line, "Batch creation:")
		if strings.HasPrefix(line, "[") {
			routine = routine || strings.Contains(line, "Uploaded ") || strings.Contains(line, "Upload session and bytes:") || strings.Contains(line, "Download and version checks:") || strings.HasSuffix(line, ": discovered") || strings.HasSuffix(line, ": uploading") || strings.HasSuffix(line, ": staged") || strings.HasSuffix(line, ": uploaded") || strings.HasSuffix(line, ": downloading") || strings.HasSuffix(line, ": processing") || strings.HasSuffix(line, ": created")
		}
		if line != "" && !routine {
			if _, err := fmt.Fprintf(d.out, "\r\x1b[2K%s\n", line); err != nil {
				return 0, err
			}
			fmt.Fprintf(d.out, "\r\x1b[2K%s", d.last)
		}
	}
	return len(p), nil
}
