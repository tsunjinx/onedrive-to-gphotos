// Package drivemigration copies a versioned source tree to a dedicated Drive root.
package drivemigration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"od2gp/internal/googledrive"
	"od2gp/internal/journal"
	"od2gp/internal/onedrive"
	"od2gp/internal/staging"
	"path"
	"strings"
	"time"
)

const Kind = "onedrive-google-drive-v1"

type Meta struct {
	RootParentID                                                                                            string
	VerifiedAt                                                                                              time.Time
	FinalError                                                                                              string
	Kind, ID, Folder, RootName, DriveID, SourceSubject, SourceClient, DestinationSubject, DestinationClient string
	CacheBytes                                                                                              int64
	InventoryComplete, ExcludeVault                                                                         bool
	Created, PauseUntil                                                                                     time.Time
}
type Node struct {
	Source                                                                     onedrive.Item
	Parent, TargetID, ParentTarget, State, Reason, Hash, Session, Verification string
	Attempts                                                                   int
	Offset                                                                     int64
	RetryAt                                                                    time.Time
}
type Source interface {
	WalkTree(context.Context, string, func(onedrive.Item) error) error
	Stat(context.Context, string) (onedrive.Item, error)
	Read(context.Context, string, int64, io.Writer) error
}
type Destination interface {
	GenerateID(context.Context) (string, error)
	Get(context.Context, string) (googledrive.File, error)
	CreateFolder(context.Context, googledrive.File) (googledrive.File, error)
	Start(context.Context, googledrive.File, int64) (string, error)
	Query(context.Context, string, int64) (int64, *googledrive.File, error)
	Chunk(context.Context, string, int64, int64, []byte) (int64, *googledrive.File, error)
}

func Same(a, b onedrive.Item) bool {
	return a.ID == b.ID && a.Size == b.Size && a.ModTime == b.ModTime && maps.Equal(a.Hashes, b.Hashes)
}
func clean(p string) bool {
	return p != "" && p != "." && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../") && !strings.ContainsAny(p, "\\\x00")
}
func Discover(ctx context.Context, s Source, store *journal.Store, m *Meta, progress io.Writer) error {
	if m.Kind != Kind || m.ID == "" || m.RootName == "" || strings.ContainsAny(m.RootName, "/\\\x00") {
		return errors.New("invalid Drive job metadata")
	}
	if m.InventoryComplete {
		return nil
	}
	if err := store.ResetInventory(); err != nil {
		return err
	}
	root := Node{Source: onedrive.Item{ID: "root", Name: m.RootName, IsDir: true}, State: "pending"}
	if err := store.Save(1, root.State, root); err != nil {
		return err
	}
	seen := map[string]bool{}
	paths := map[string]bool{}
	var id int64 = 1
	visit := func(item onedrive.Item) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !clean(item.Path) || item.ID == "" || seen[item.ID] || paths[item.Path] {
			return errors.New("unsafe, repeated or unidentified source tree item")
		}
		seen[item.ID] = true
		paths[item.Path] = true
		if strings.Count(item.Path, "/")+2 > 100 {
			return fmt.Errorf("folder depth cannot be represented in Drive: %q", item.Path)
		}
		n := Node{Source: item, Parent: path.Dir(item.Path), State: "pending"}
		if n.Parent == "." {
			n.Parent = ""
		}
		if !item.IsDir {
			if item.Size < 0 {
				return fmt.Errorf("unknown source size: %q", item.Path)
			}
			if item.Size == 0 {
				n.State = "skipped"
				n.Reason = "skipped_empty_file"
			} else if item.Size > 5_000_000_000_000 || staging.Required(item.Size) > m.CacheBytes {
				return fmt.Errorf("file exceeds Drive/staging limit: %q", item.Path)
			}
			if item.ModTime == "" && len(item.Hashes) == 0 {
				return fmt.Errorf("source version unavailable: %q", item.Path)
			}
		}
		id++
		if err := store.Save(id, n.State, n); err != nil {
			return err
		}
		if progress != nil && id%100 == 0 {
			fmt.Fprintf(progress, "Scanned %d source files and folders.\n", id-1)
		}
		return nil
	}
	var err error
	if checkpoint, ok := s.(interface {
		WalkTreeCheckpoint(context.Context, string, *journal.Store, string, io.Writer, func(onedrive.Item) error) error
	}); ok {
		binding := fmt.Sprintf("drive-tree-v1:%s:%s:%s:%s:%t", m.ID, m.DriveID, m.SourceSubject, m.Folder, m.ExcludeVault)
		err = checkpoint.WalkTreeCheckpoint(ctx, m.Folder, store, binding, progress, visit)
	} else {
		err = s.WalkTree(ctx, m.Folder, visit)
	}
	if err != nil {
		return err
	}
	nodes, err := Load(store)
	if err != nil {
		return err
	}
	children := map[string]int{}
	for _, n := range nodes {
		if n.Source.Path == "" {
			continue
		}
		parent, ok := nodes[n.Parent]
		if !ok || !parent.Source.IsDir {
			return fmt.Errorf("missing parent for %q", n.Source.Path)
		}
		if n.State != "skipped" {
			children[n.Parent]++
		}
		if children[n.Parent] > 500000 {
			return fmt.Errorf("folder contains too many items: %q", n.Parent)
		}
	}
	m.InventoryComplete = true
	return store.Save(0, "drive-job", m)
}
func Load(s *journal.Store) (map[string]Node, error) {
	ids, e := s.IDs()
	if e != nil {
		return nil, e
	}
	nodes := map[string]Node{}
	for _, id := range ids {
		var n Node
		if e := s.Load(id, &n); e != nil {
			return nil, e
		}
		if _, ok := nodes[n.Source.Path]; ok {
			return nil, errors.New("repeated journal path")
		}
		nodes[n.Source.Path] = n
	}
	return nodes, nil
}
