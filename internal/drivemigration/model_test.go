package drivemigration

import (
	"context"
	"io"
	"od2gp/internal/journal"
	"od2gp/internal/onedrive"
	"path/filepath"
	"testing"
)

type sourceFake struct{ items []onedrive.Item }

func (s sourceFake) WalkTree(ctx context.Context, _ string, f func(onedrive.Item) error) error {
	for _, v := range s.items {
		if e := f(v); e != nil {
			return e
		}
	}
	return nil
}
func (s sourceFake) Stat(context.Context, string) (onedrive.Item, error)  { return onedrive.Item{}, nil }
func (s sourceFake) Read(context.Context, string, int64, io.Writer) error { return nil }
func TestDiscoverKeepsEmptyFoldersAndSkipsOnlyConfirmedZero(t *testing.T) {
	for _, size := range []int64{0, -1} {
		s, e := journal.Open(filepath.Join(t.TempDir(), "progress.db"), make([]byte, 32), true)
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		m := Meta{Kind: Kind, ID: "test", RootName: "Backup", CacheBytes: 1 << 30}
		src := sourceFake{items: []onedrive.Item{{ID: "d", Name: "rỗng", Path: "rỗng", IsDir: true}, {ID: "f", Name: "empty.txt", Path: "rỗng/empty.txt", Size: size, ModTime: "2026-01-01T00:00:00Z"}}}
		e = Discover(context.Background(), src, s, &m, nil)
		if size < 0 {
			if e == nil || m.InventoryComplete {
				t.Fatal("unknown size silently skipped")
			}
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		n, e := Load(s)
		if e != nil {
			t.Fatal(e)
		}
		if len(n) != 3 || n["rỗng"].State != "pending" || n["rỗng/empty.txt"].Reason != "skipped_empty_file" {
			t.Fatalf("lost tree: %+v", n)
		}
	}
}
