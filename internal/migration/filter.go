package migration

import (
	"errors"
	"strings"
)

// Exclusions are literal drive-root folder paths, never globs or filename heuristics.
// Inventory still lists every file; exclusions become reportable skipped records.
func NormalizeExcludedFolders(folders []string) ([]string, error) {
	var normalized []string
	for _, folder := range folders {
		if !strings.HasPrefix(folder, "/") || strings.ContainsAny(folder, "\\\x00\r\n") {
			return nil, errors.New("excluded folders must be absolute OneDrive paths with forward slashes")
		}
		folder = strings.TrimRight(folder, "/")
		if folder == "" {
			return nil, errors.New("cannot exclude the entire drive")
		}
		for _, part := range strings.Split(folder[1:], "/") {
			if part == "" || part == "." || part == ".." {
				return nil, errors.New("excluded folders must not contain empty, . or .. segments")
			}
		}
		duplicate := false
		for _, existing := range normalized {
			if strings.EqualFold(existing, folder) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			normalized = append(normalized, folder)
		}
	}
	return normalized, nil
}
func ExcludedFolder(name string, folders []string) string {
	parts := strings.Split(strings.TrimPrefix(name, "/"), "/")
	for _, folder := range folders {
		prefix := strings.Split(strings.Trim(folder, "/"), "/")
		if len(parts) < len(prefix) {
			continue
		}
		matches := true
		for i := range prefix {
			if !strings.EqualFold(parts[i], prefix[i]) {
				matches = false
				break
			}
		}
		if matches {
			return folder
		}
	}
	return ""
}
