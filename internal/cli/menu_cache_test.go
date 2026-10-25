package cli

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
	"od2gp/internal/joblock"
	"od2gp/internal/journal"
	"od2gp/internal/migration"
)

func TestMenuContinueResizesWithoutStartingTransfer(t *testing.T) {
	id := strings.Repeat("a", 32)
	var output bytes.Buffer
	var calls [][]string
	app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("3\nc\n1\n60\n\n0\n")), out: &output, errOut: &output, options: defaultTransferOptions(), jobs: func() ([]menuJob, error) {
		return []menuJob{{ID: id, CacheBytes: 30 << 30, Started: true, Incomplete: true}}, nil
	}, dispatch: func(args []string, _ io.Reader, _, _ io.Writer) int { calls = append(calls, args); return 0 }}
	if code := app.run(); code != 0 {
		t.Fatal(code)
	}
	want := [][]string{{"resize-cache", "--job", id, "--cache-gib", "60"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected upload or new plan: %v", calls)
	}
	app.input = bufio.NewReader(strings.NewReader("b\n"))
	app.resizeCache(menuJob{ID: id, CacheBytes: 30 << 30})
	if len(calls) != 1 {
		t.Fatal("cancel resized job")
	}
}
func TestResizeCacheIsOfflineLockedAndKeepsCompletion(t *testing.T) {
	keyring.MockInit()
	root := t.TempDir()
	id := strings.Repeat("b", 32)
	dir := filepath.Join(root, "od2gp", "jobs", id)
	key, err := journal.Key(id, true)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, "progress.db")
	store, err := journal.Open(name, key, true)
	if err != nil {
		t.Fatal(err)
	}
	meta := migration.Meta{ID: id, CacheBytes: 30 << 30, DriveID: "drive", GoogleSubject: "account", InventoryComplete: true, ExcludePersonalVault: true}
	store.Save(0, "job", meta)
	store.Save(1, "completed", migration.Item{State: "completed", MediaID: "confirmed"})
	store.Close()
	run := func() int {
		var output bytes.Buffer
		return migrationCommand(context.Background(), "resize-cache", []string{"--job", id, "--cache-gib", "60"}, strings.NewReader(""), &output, &output, func(string) string { return "" }, root)
	}
	unlock, err := joblock.Acquire(filepath.Join(dir, "job.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if run() != 1 {
		t.Fatal("resized active job")
	}
	unlock()
	if code := run(); code != 0 {
		t.Fatalf("offline resize failed: %d", code)
	}
	store, err = journal.Open(name, key, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var saved migration.Meta
	store.Load(0, &saved)
	meta.CacheBytes = 60 << 30
	if !reflect.DeepEqual(saved, meta) {
		t.Fatal("changed job identity or scope")
	}
	var item migration.Item
	store.Load(1, &item)
	if item.State != "completed" || item.MediaID != "confirmed" {
		t.Fatal("lost completion")
	}
}
func TestResizeRequiresExplicitValidLimit(t *testing.T) {
	for _, args := range [][]string{{"--job", strings.Repeat("a", 32)}, {"--cache-gib", "0"}, {"--cache-gib", "2048"}, {"--cache-gib", "60", "--yes"}} {
		var out bytes.Buffer
		if code := migrationCommand(context.Background(), "resize-cache", args, strings.NewReader(""), &out, &out, func(string) string { return "" }, t.TempDir()); code != 2 {
			t.Fatalf("invalid flags reached job access: %v %d", args, code)
		}
	}
}
