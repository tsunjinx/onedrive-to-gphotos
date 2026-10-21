package migration

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"od2gp/internal/staging"
)

type interruptedSource struct {
	*sourceFake
	offsets     []int64
	fail        bool
	ignoreRange bool
}
type ignoredRange struct{}

func (ignoredRange) Error() string         { return "range ignored" }
func (ignoredRange) RestartDownload() bool { return true }
func (s *interruptedSource) Read(ctx context.Context, name string, offset int64, w io.Writer) error {
	s.offsets = append(s.offsets, offset)
	if s.fail {
		s.fail = false
		_, err := w.Write(s.data[:staging.Chunk+100])
		if err != nil {
			return err
		}
		return errors.New("source interrupted")
	}
	if offset > 0 && s.ignoreRange {
		return ignoredRange{}
	}
	return s.sourceFake.Read(ctx, name, offset, w)
}
func TestDownloadResumeAfterReopenUsesVerifiedOffset(t *testing.T) {
	for _, ignoreRange := range []bool{false, true} {
		e, meta, source, photos, close := fixture(t)
		source.data = bytes.Repeat([]byte{1}, staging.Chunk+255)
		source.item.Size = int64(len(source.data))
		var item Item
		e.Store.Load(1, &item)
		item.Source = source.item
		e.Store.Save(1, item.State, item)
		interrupted := &interruptedSource{sourceFake: source, fail: true, ignoreRange: ignoreRange}
		e.Source = interrupted
		if err := e.Run(context.Background(), meta); err == nil {
			t.Fatal("interruption ignored")
		}
		fresh, saved := reopenEngine(t, e)
		if err := fresh.Run(context.Background(), saved); err != nil {
			t.Fatal(err)
		}
		if len(interrupted.offsets) < 2 || interrupted.offsets[1] != staging.Chunk {
			t.Fatalf("did not resume retained prefix: %v", interrupted.offsets)
		}
		if ignoreRange && (len(interrupted.offsets) != 3 || interrupted.offsets[2] != 0) {
			t.Fatalf("range fallback appended unsafe data: %v", interrupted.offsets)
		}
		fresh.Store.Load(1, &item)
		if item.State != "completed" || photos.creates != 1 {
			t.Fatalf("not completed exactly once: %+v", item)
		}
		close()
	}
}
