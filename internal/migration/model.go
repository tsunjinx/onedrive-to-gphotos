// Package migration coordinates bounded, checkpointed OneDrive to Google Photos transfers.
package migration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"maps"
	"path"
	"strings"
	"time"

	"od2gp/internal/googlephotos"
	"od2gp/internal/onedrive"
)

type Source interface {
	DriveID(context.Context) (string, error)
	Walk(context.Context, string, func(onedrive.Item) error) error
	Stat(context.Context, string) (onedrive.Item, error)
	Read(context.Context, string, int64, io.Writer) error
}
type Meta struct {
	SourceBackend, SourceSubject, SourceClient       string
	ID, Folder, DriveID, GoogleSubject, GoogleClient string
	CacheBytes                                       int64
	InventoryComplete                                bool
	ExcludePersonalVault                             bool
	Created                                          time.Time
	RetryAt                                          time.Time
}
type Item struct {
	Source                                   onedrive.Item
	Path, MIME, State, Reason, Hash, MediaID string
	Video                                    bool
	Upload                                   googlephotos.Session
	Offset                                   int64
	Token                                    string
	TokenTime                                time.Time
}

func ID() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func ValidID(id string) bool {
	value, err := hex.DecodeString(id)
	return err == nil && len(value) == 16 && strings.ToLower(id) == id
}
func fullPath(folder, name string) (string, error) {
	if path.IsAbs(name) || strings.Contains(name, "\\") || strings.ContainsRune(name, 0) {
		return "", errors.New("unsupported source path")
	}
	cleaned := path.Clean(name)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("source path escapes selected folder")
	}
	return path.Join(strings.TrimPrefix(folder, "/"), cleaned), nil
}
func sameVersion(a, b onedrive.Item) bool {
	return a.ID == b.ID && a.Size == b.Size && a.ModTime == b.ModTime && maps.Equal(a.Hashes, b.Hashes)
}
func classify(item onedrive.Item) (string, bool, string) {
	extension := strings.ToLower(path.Ext(item.Path))
	photos := map[string]string{".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".heic": "image/heic", ".heif": "image/heif", ".gif": "image/gif", ".webp": "image/webp", ".avif": "image/avif", ".bmp": "image/bmp", ".tif": "image/tiff", ".tiff": "image/tiff"}
	videos := map[string]string{".mp4": "video/mp4", ".mov": "video/quicktime", ".m4v": "video/x-m4v", ".avi": "video/x-msvideo", ".mkv": "video/x-matroska", ".wmv": "video/x-ms-wmv", ".mpg": "video/mpeg", ".mpeg": "video/mpeg", ".3gp": "video/3gpp", ".mts": "video/mp2t", ".m2ts": "video/mp2t"}
	mime, ok := photos[extension]
	video := false
	if !ok {
		mime, ok = videos[extension]
		video = ok
	}
	if !ok {
		return "", false, "unsupported file type"
	}
	if item.ID == "" {
		return mime, video, "source stable ID missing"
	}
	if item.Size <= 0 {
		return mime, video, "empty or unknown-size file"
	}
	if (!video && item.Size > 200_000_000) || (video && item.Size > 20_000_000_000) {
		return mime, video, "file exceeds Google Photos upload limit"
	}
	return mime, video, ""
}
