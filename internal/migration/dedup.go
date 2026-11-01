package migration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"od2gp/internal/journal"
	"od2gp/internal/onedrive"
)

// CompletedRef represents a file confirmed completed in another migration job.
type CompletedRef struct {
	JobID   string
	Path    string
	Size    int64
	ModTime string
	Hashes  map[string]string
	MediaID string
}

// CompletedIndex indexes completed files across sibling jobs by OneDrive item ID and normalized path.
type CompletedIndex struct {
	byID   map[string]CompletedRef
	byPath map[string]CompletedRef
}

// NewCompletedIndex initializes an empty index.
func NewCompletedIndex() *CompletedIndex {
	return &CompletedIndex{
		byID:   make(map[string]CompletedRef),
		byPath: make(map[string]CompletedRef),
	}
}

// Size returns the count of unique completed items indexed.
func (idx *CompletedIndex) Size() int {
	if idx == nil {
		return 0
	}
	if len(idx.byID) > len(idx.byPath) {
		return len(idx.byID)
	}
	return len(idx.byPath)
}

// Match checks whether a source item matches a previously completed file.
func (idx *CompletedIndex) Match(source onedrive.Item, fullPath string) (CompletedRef, bool) {
	if idx == nil {
		return CompletedRef{}, false
	}
	// Try matching by unique OneDrive Item ID first
	if source.ID != "" {
		if ref, ok := idx.byID[source.ID]; ok {
			if matchesRef(ref, source) {
				return ref, true
			}
		}
	}
	// Fallback to normalized full path
	normPath := normalizeDeduplicationPath(fullPath)
	if normPath != "" {
		if ref, ok := idx.byPath[normPath]; ok {
			if matchesRef(ref, source) {
				return ref, true
			}
		}
	}
	return CompletedRef{}, false
}

func normalizeDeduplicationPath(p string) string {
	return strings.ToLower(strings.Trim(filepath.ToSlash(p), "/"))
}

func matchesRef(ref CompletedRef, source onedrive.Item) bool {
	// If both have positive sizes and they differ, content has changed.
	if source.Size > 0 && ref.Size > 0 && source.Size != ref.Size {
		return false
	}
	// If both have timestamps and they differ, item was modified.
	if source.ModTime != "" && ref.ModTime != "" && source.ModTime != ref.ModTime {
		return false
	}
	// If both have hashes, verify they do not conflict.
	for k, v := range source.Hashes {
		if refVal, ok := ref.Hashes[k]; ok && refVal != "" && v != "" && !strings.EqualFold(refVal, v) {
			return false
		}
	}
	return true
}

// KeyResolver retrieves the encryption key for a saved job.
type KeyResolver func(id string) ([]byte, error)

// LoadCompletedIndex scans sibling migration jobs under jobsDir matching the given driveID and googleSubject,
// populating an index of all completed files.
func LoadCompletedIndex(jobsDir, currentJobID, driveID, googleSubject string, resolveKey KeyResolver) (*CompletedIndex, error) {
	idx := NewCompletedIndex()
	if jobsDir == "" {
		return idx, nil
	}
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		return idx, nil
	}
	if resolveKey == nil {
		resolveKey = func(id string) ([]byte, error) { return journal.Key(id, false) }
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		otherID := entry.Name()
		if otherID == currentJobID || !ValidID(otherID) {
			continue
		}
		dbPath := filepath.Join(jobsDir, otherID, "progress.db")
		if _, err := os.Stat(dbPath); err != nil {
			continue
		}
		key, err := resolveKey(otherID)
		if err != nil {
			continue
		}
		otherStore, err := journal.Open(dbPath, key, false)
		if err != nil {
			continue
		}
		var otherMeta Meta
		if err := otherStore.Load(0, &otherMeta); err != nil {
			otherStore.Close()
			continue
		}
		if driveID != "" && otherMeta.DriveID != "" && driveID != otherMeta.DriveID {
			otherStore.Close()
			continue
		}
		if googleSubject != "" && otherMeta.GoogleSubject != "" && googleSubject != otherMeta.GoogleSubject {
			otherStore.Close()
			continue
		}
		_ = otherStore.LoadRecords("completed", func(id int64, state string, plain []byte) error {
			var item Item
			if err := json.Unmarshal(plain, &item); err != nil {
				return nil
			}
			ref := CompletedRef{
				JobID:   otherID,
				Path:    item.Path,
				Size:    item.Source.Size,
				ModTime: item.Source.ModTime,
				Hashes:  item.Source.Hashes,
				MediaID: item.MediaID,
			}
			if item.Source.ID != "" {
				idx.byID[item.Source.ID] = ref
			}
			if item.Path != "" {
				idx.byPath[normalizeDeduplicationPath(item.Path)] = ref
			}
			return nil
		})
		otherStore.Close()
	}
	return idx, nil
}
