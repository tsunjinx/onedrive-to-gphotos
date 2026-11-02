package drivemigration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"od2gp/internal/googledrive"
	"od2gp/internal/journal"
	"od2gp/internal/onedrive"
	"od2gp/internal/staging"
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
	if len(f.Parents) == 0 {
		f.Parents = []string{"actual-my-drive-root"}
	}
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
	m := &Meta{Kind: Kind, RootParentID: "root", ID: "job", RootName: "Backup", CacheBytes: 1 << 20}
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

func (d *driveFake) Children(_ context.Context, parent string) ([]googledrive.File, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []googledrive.File
	for _, f := range d.files {
		if len(f.Parents) == 1 && f.Parents[0] == parent {
			out = append(out, f)
		}
	}
	return out, nil
}

func TestUnknownStagingAndSourceTreeChangesAreVisible(t *testing.T) {
	e, d := engineFixture(t)
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	src := e.Source.(fullSource)
	src.items = append(src.items, onedrive.Item{ID: "new", Name: "new-folder", Path: "new-folder", IsDir: true})
	e.Source = src
	if e.Verify(context.Background()) == nil {
		t.Fatal("ignored new source folder")
	}
	e.Source = fullSource{items: src.items[:len(src.items)-1], data: src.data}
	d.files["extra"] = googledrive.File{ID: "extra", Name: "extra", Parents: []string{"1"}}
	if e.Verify(context.Background()) == nil {
		t.Fatal("ignored unexpected destination item")
	}
}

type deniedFolder struct{ *driveFake }

func (d deniedFolder) CreateFolder(ctx context.Context, f googledrive.File) (googledrive.File, error) {
	if f.Name == "Rỗng" {
		return googledrive.File{}, &googledrive.Error{Code: 403}
	}
	return d.driveFake.CreateFolder(ctx, f)
}
func TestPermanentFolderFailureDoesNotCancelIndependentFile(t *testing.T) {
	e, d := engineFixture(t)
	e.Destination = deniedFolder{d}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := e.Run(ctx); err == nil {
		t.Fatal("ignored blocked folder")
	}
	nodes, err := Load(e.Store)
	if err != nil {
		t.Fatal(err)
	}
	if nodes["a.txt"].State != "completed" || nodes["Rỗng"].State != "blocked" {
		t.Fatalf("independent work lost: %+v", nodes)
	}
}
func TestStagingCapBoundsTransfersWithEightWorkers(t *testing.T) {
	e, _ := engineFixture(t)
	src := e.Source.(fullSource)
	for i := 0; i < 12; i++ {
		p := fmt.Sprintf("item-%d", i)
		src.items = append(src.items, onedrive.Item{ID: p, Name: p, Path: p, Size: 3, ModTime: "2026-01-01T00:00:00Z"})
		src.data[p] = []byte("abc")
	}
	e.Source = src
	e.Meta.InventoryComplete = false
	e.Meta.CacheBytes = 31
	if err := Discover(context.Background(), src, e.Store, e.Meta, nil); err != nil {
		t.Fatal(err)
	}
	e.Workers = 8
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Verify(ctx); err != nil {
		t.Fatal(err)
	}
}

type expiredSession struct{ *driveFake }

func (d expiredSession) Query(context.Context, string, int64) (int64, *googledrive.File, error) {
	return 0, nil, &googledrive.Error{Code: 410}
}
func TestExpiredSessionKeepsDestinationIdentity(t *testing.T) {
	e, d := engineFixture(t)
	nodes, err := Load(e.Store)
	if err != nil {
		t.Fatal(err)
	}
	n := nodes["a.txt"]
	n.State = "uploading"
	n.TargetID = "reserved"
	n.ParentTarget = "root"
	n.Session = "expired"
	w, _, err := staging.Resume(context.Background(), e.stagePath(3), e.stageKey(3), 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	n.Hash, err = w.Finish()
	if err != nil {
		t.Fatal(err)
	}
	e.Destination = expiredSession{d}
	if err = e.transfer(context.Background(), 3, &n); err != nil {
		t.Fatal(err)
	}
	if n.TargetID != "reserved" || d.next != 0 || n.State != "completed" {
		t.Fatal("expired session changed identity")
	}
}

type cancelAfterFolder struct {
	*driveFake
	cancel context.CancelFunc
}

func (d cancelAfterFolder) CreateFolder(ctx context.Context, f googledrive.File) (googledrive.File, error) {
	_, err := d.driveFake.CreateFolder(ctx, f)
	d.cancel()
	return googledrive.File{}, errors.Join(err, &googledrive.Error{Code: 503})
}
func TestReopenedJournalRecoversLostFolderCreation(t *testing.T) {
	e, d := engineFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	e.Destination = cancelAfterFolder{d, cancel}
	if err := e.Run(ctx); err == nil {
		t.Fatal("expected interruption")
	}
	key := append([]byte(nil), e.Store.Key...)
	if err := e.Store.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := journal.Open(filepath.Join(e.Directory, "progress.db"), key, false)
	if err != nil {
		t.Fatal(err)
	}
	e.Store = s
	defer s.Close()
	e.Destination = d
	e.Meta.PauseUntil = time.Time{}
	if err = e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.next != 3 || len(d.files) != 3 {
		t.Fatal("reopen duplicated destination IDs")
	}
}

func TestUnrecognizedStagingStopsBeforeCloudMutation(t *testing.T) {
	e, d := engineFixture(t)
	if err := os.WriteFile(e.stagePath(999), []byte("unknown"), 0600); err != nil {
		t.Fatal(err)
	}
	if e.Run(context.Background()) == nil || d.next != 0 {
		t.Fatal("unrecognized staging was ignored")
	}
}

type conflictingFolder struct {
	*driveFake
	once bool
}

func (d *conflictingFolder) CreateFolder(ctx context.Context, f googledrive.File) (googledrive.File, error) {
	out, err := d.driveFake.CreateFolder(ctx, f)
	if err != nil {
		return out, err
	}
	if d.once {
		d.once = false
		return googledrive.File{}, &googledrive.Error{Code: 409}
	}
	return out, nil
}
func TestCreateConflictReconcilesSameIDAutomatically(t *testing.T) {
	e, d := engineFixture(t)
	e.Destination = &conflictingFolder{d, true}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if d.next != 3 || len(d.files) != 3 {
		t.Fatal("creation conflict duplicated destination")
	}
}

func TestTemporaryFailureHoldingFullCacheWaitsInsteadOfStopping(t *testing.T) {
	e, d := engineFixture(t)
	src := e.Source.(fullSource)
	src.items = append(src.items, onedrive.Item{ID: "b", Name: "b.txt", Path: "b.txt", Size: 3, ModTime: "2026-01-01T00:00:00Z"})
	src.data["b.txt"] = []byte("abc")
	e.Source = src
	e.Meta.CacheBytes = 31
	e.Meta.InventoryComplete = false
	if err := Discover(context.Background(), src, e.Store, e.Meta, nil); err != nil {
		t.Fatal(err)
	}
	d.failures = 1
	e.retryDelay = func(int) time.Duration { return 200 * time.Millisecond }
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Verify(ctx); err != nil {
		t.Fatal(err)
	}
}

type limitedFolder struct {
	*driveFake
	once  bool
	until time.Time
}

func (d *limitedFolder) CreateFolder(ctx context.Context, f googledrive.File) (googledrive.File, error) {
	if d.once {
		d.once = false
		return googledrive.File{}, &googledrive.Error{Code: 429, RetryAt: d.until}
	}
	if time.Now().Before(d.until) {
		return googledrive.File{}, fmt.Errorf("retried before server deadline")
	}
	return d.driveFake.CreateFolder(ctx, f)
}
func TestServerRetryAfterIsPersistedAndHonored(t *testing.T) {
	e, d := engineFixture(t)
	until := time.Now().Add(200 * time.Millisecond)
	e.Destination = &limitedFolder{d, true, until}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	var saved Meta
	if err := e.Store.Load(0, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.PauseUntil.Before(until) {
		t.Fatal("server retry deadline was shortened or not saved")
	}
}

func TestAppRootLearnsParentWithoutUnrelatedRootAccess(t *testing.T) {
	e, d := engineFixture(t)
	e.Meta.RootParentID = ""
	d.lostFolder = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	nodes, err := Load(e.Store)
	if err != nil {
		t.Fatal(err)
	}
	if nodes[""].ParentTarget != "actual-my-drive-root" {
		t.Fatal("root parent was not durably learned")
	}
	if err = e.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	if err = e.Run(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPackageSkipSurvivesTransferVerificationAndResume(t *testing.T) {
	e, d := engineFixture(t)
	src := e.Source.(fullSource)
	src.items = append(src.items, onedrive.Item{ID: "notebook", Name: "Notebook", Path: "Rỗng/Notebook", Size: -1, OneDrivePackage: true})
	e.Source = src
	e.Meta.InventoryComplete = false
	e.Meta.SkipOneDrivePackages = true
	if err := Discover(context.Background(), src, e.Store, e.Meta, nil); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := e.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := e.Verify(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(d.files) != 3 || d.next != 3 {
		t.Fatalf("created destination object for package: %+v", d.files)
	}
	nodes, err := Load(e.Store)
	if err != nil {
		t.Fatal(err)
	}
	if nodes["Rỗng/Notebook"].Reason != SkippedPackage {
		t.Fatal("skip lost on resume")
	}
}
