package onedrive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestArchiveDownloadVerifiesBytes(t *testing.T) {
	data := "official test archive"
	hash := sha256.Sum256([]byte(data))
	for _, checksum := range []string{hex.EncodeToString(hash[:]), strings.Repeat("0", 64)} {
		path := filepath.Join(t.TempDir(), "download.zip")
		client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data))}, nil
		})}
		err := downloadArchive(context.Background(), client, "https://example.test/archive", path, checksum)
		if (err == nil) != (checksum == hex.EncodeToString(hash[:])) {
			t.Fatalf("checksum accepted incorrectly: %v", err)
		}
		if err == nil {
			got, _ := os.ReadFile(path)
			if string(got) != data {
				t.Fatal("archive truncated")
			}
		}
	}
}

func TestOfficialArchiveNames(t *testing.T) {
	for _, tc := range []struct{ os, arch, want string }{
		{"darwin", "arm64", "rclone-v1.75.2-osx-arm64.zip"},
		{"darwin", "amd64", "rclone-v1.75.2-osx-amd64.zip"},
		{"windows", "amd64", "rclone-v1.75.2-windows-amd64.zip"},
	} {
		if got := archiveName(tc.os, tc.arch); got != tc.want {
			t.Fatalf("%s/%s archive=%q want=%q", tc.os, tc.arch, got, tc.want)
		}
	}
}
