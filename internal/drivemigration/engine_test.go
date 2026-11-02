package drivemigration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"od2gp/internal/googledrive"
	"od2gp/internal/journal"
	"od2gp/internal/onedrive"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fullSource struct {
	items []onedrive.Item
	data  map[string][]byte
}

func (s fullSource) WalkTree(ctx context.Context, _ string, f func(onedrive.Item) error) error {
	for _, n := range s.items {
		if e := f(n); e != nil {
			return e
		}
	}
	return nil
}
func (s fullSource) Stat(ctx context.Context, p string) (onedrive.Item, error) {
	for _, n := range s.items {
		if n.Path == p {
			return n, nil
		}
	}
	return onedrive.Item{}, fmt.Errorf("missing fixture source")
}
func (s fullSource) Read(ctx context.Context, p string, off int64, w io.Writer) error {
	_, e := w.Write(s.data[p][off:])
	return e
}

type driveFake struct {
	mu                    sync.Mutex
	files                 map[string]googledrive.File
	sessions              map[string]googledrive.File
	next                  int
	lostFolder, lostFinal bool
	failures              int
	calls                 int
}

func (d *driveFake) GenerateID(context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.next++
	return fmt.Sprint(d.next), nil
}
func (d *driveFake) Get(_ context.Context, id string) (googledrive.File, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f, ok := d.files[id]
	if !ok {
		return f, &googledrive.Error{Code: 404}
	}
	return f, nil
}
func (d *driveFake) CreateFolder(_ context.Context, f googledrive.File) (googledrive.File, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.files[f.ID] = f
	if d.lostFolder {
		d.lostFolder = false
		return googledrive.File{}, &googledrive.Error{Code: 503}
	}
	return f, nil
}
func (d *driveFake) Start(_ context.Context, f googledrive.File, size int64) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if d.failures > 0 {
		d.failures--
		return "", &googledrive.Error{Code: 503}
	}
	f.Size = size
	d.sessions[f.ID] = f
	return f.ID, nil
}
func (d *driveFake) Query(_ context.Context, s string, _ int64) (int64, *googledrive.File, error) {
	return 0, nil, nil
}
func (d *driveFake) Chunk(_ context.Context, s string, off, size int64, b []byte) (int64, *googledrive.File, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f := d.sessions[s]
	h := sha256.Sum256(b)
	f.SHA256 = hex.EncodeToString(h[:])
	d.files[f.ID] = f
	if d.lostFinal {
		d.lostFinal = false
		return 0, nil, &googledrive.Error{Code: 503}
	}
	return size, &f, nil
}
func engineFixture(t *testing.T) (*Engine, *driveFake) {
	t.Helper()
	dir := t.TempDir()
	s, e := journal.Open(filepath.Join(dir, "progress.db"), make([]byte, 32), true)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	src := fullSource{items: []onedrive.Item{{ID: "dir", Name: "Rỗng", Path: "Rỗng", IsDir: true}, {ID: "a", Name: "a.txt", Path: "a.txt", Size: 3, ModTime: "2026-01-01T00:00:00Z"}, {ID: "zero", Name: "zero", Path: "Rỗng/zero", Size: 0, ModTime: "2026-01-01T00:00:00Z"}}, data: map[string][]byte{"a.txt": []byte("abc")}}
	m := &Meta{Kind: Kind, ID: "job", RootName: "Backup", CacheBytes: 1 << 20}
	if e = Discover(context.Background(), src, s, m, nil); e != nil {
		t.Fatal(e)
	}
	d := &driveFake{files: map[string]googledrive.File{}, sessions: map[string]googledrive.File{}}
	return &Engine{Store: s, Source: src, Destination: d, Meta: m, Directory: dir, Workers: 3, retryDelay: func(int) time.Duration { return 0 }}, d
}
func TestTreeTransferAndLostResponsesDoNotDuplicate(t *testing.T) {
	e, d := engineFixture(t)
	d.lostFolder = true
	d.lostFinal = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(d.files) != 3 || d.next != 3 {
		t.Fatalf("duplicated/missing tree: files=%d ids=%d", len(d.files), d.next)
	}
	if err := e.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if d.next != 3 {
		t.Fatal("resume allocated duplicate IDs")
	}
	for _, f := range d.files {
		if f.Name == "a.txt" && f.Parents[0] == "root" {
			t.Fatal("lost source parent")
		}
	}
	files, _ := filepath.Glob(filepath.Join(e.Directory, "stage-*.bin"))
	if len(files) != 0 {
		t.Fatal("completed cache retained")
	}
}
func TestTransientErrorsBeyondOldBudgetRecover(t *testing.T) {
	e, d := engineFixture(t)
	d.failures = 8
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if d.calls != 9 {
		t.Fatalf("stopped retrying: calls=%d", d.calls)
	}
}
func TestDestinationMismatchNeverCompletes(t *testing.T) {
	e, d := engineFixture(t)
	ctx := context.Background()
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	for id, f := range d.files {
		if f.MIME != googledrive.Folder {
			f.Parents = []string{"wrong-parent"}
			d.files[id] = f
		}
	}
	if e.Verify(ctx) == nil {
		t.Fatal("verified wrong parent")
	}
}
func TestCleanupSurvivesRestart(t *testing.T) {
	e, _ := engineFixture(t)
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.stagePath(2), []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.stagePath(2)); !os.IsNotExist(err) {
		t.Fatal("completed cache not cleaned")
	}
}
func TestWorkerTunerCooldownAndRollback(t *testing.T) {
	now := time.Now()
	tuner := NewTuner(now)
	if n, _ := tuner.Observe(now.Add(time.Second), 1000, false, false); n != 3 {
		t.Fatal("scaled before window")
	}
	if n, _ := tuner.Observe(now.Add(21*time.Second), 20000, false, false); n != 4 {
		t.Fatal("did not trial increase")
	}
	if n, _ := tuner.Observe(now.Add(42*time.Second), 1000, false, false); n != 3 {
		t.Fatal("did not roll back throughput regression")
	}
	if n, _ := tuner.Observe(now.Add(43*time.Second), 0, false, true); n != 2 {
		t.Fatal("did not back off")
	}
}
