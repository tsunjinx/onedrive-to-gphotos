// Package onedrive accesses OneDrive through rclone's supported OAuth client.
package onedrive

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const RcloneVersion = "v1.75.2"
const maxArchive = 200 << 20

func ExecutableName() string {
	if runtime.GOOS == "windows" {
		return "rclone.exe"
	}
	return "rclone"
}
func checkBinary(ctx context.Context, path string) error {
	output, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil || !strings.HasPrefix(string(output), "rclone v") {
		return errors.New("rclone executable failed validation")
	}
	return nil
}

// Ensure installs only the single verified executable, without extracting archive paths.
func Ensure(ctx context.Context, dir string) (string, error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return "", errors.New("unsupported OS for rclone setup")
	}
	if runtime.GOARCH != "amd64" && !(runtime.GOOS == "darwin" && runtime.GOARCH == "arm64") {
		return "", errors.New("unsupported architecture for rclone setup")
	}
	if path, err := exec.LookPath(ExecutableName()); err == nil && checkBinary(ctx, path) == nil {
		return path, nil
	}
	dir = filepath.Join(dir, "rclone", RcloneVersion+"-"+runtime.GOOS+"-"+runtime.GOARCH)
	path := filepath.Join(dir, ExecutableName())
	if checkBinary(ctx, path) == nil {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", errors.New("cannot create rclone tool directory")
	}
	lock := filepath.Join(dir, ".install-lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		return "", errors.New("rclone installation is already running; inspect .install-lock if a previous setup was interrupted")
	}
	defer os.Remove(lock)
	if _, err := os.Lstat(path); err == nil {
		return "", errors.New("managed rclone is invalid; move it aside before retrying")
	}
	stage, err := os.MkdirTemp(dir, ".download-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	archive := archiveName(runtime.GOOS, runtime.GOARCH)
	base := "https://downloads.rclone.org/" + RcloneVersion + "/"
	client := &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.Host != "downloads.rclone.org" {
			return errors.New("untrusted download redirect")
		}
		return nil
	}}
	sums, err := download(ctx, client, base+"SHA256SUMS", 2<<20)
	if err != nil {
		return "", err
	}
	checksum := ""
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == archive {
			checksum = fields[0]
			break
		}
	}
	decoded, err := hex.DecodeString(checksum)
	if err != nil || len(decoded) != 32 {
		return "", errors.New("official checksum manifest is missing the rclone archive")
	}
	zipPath := filepath.Join(stage, "rclone.zip")
	if err := downloadArchive(ctx, client, base+archive, zipPath, checksum); err != nil {
		return "", err
	}
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", errors.New("invalid rclone zip archive")
	}
	defer reader.Close()
	expected := strings.TrimSuffix(archive, ".zip") + "/" + ExecutableName()
	found := false
	candidate := filepath.Join(stage, ExecutableName())
	for _, entry := range reader.File {
		if entry.Name != expected {
			continue
		}
		if found || entry.Mode()&os.ModeSymlink != 0 || entry.UncompressedSize64 > maxArchive {
			return "", errors.New("unexpected rclone executable in archive")
		}
		found = true
		source, err := entry.Open()
		if err != nil {
			return "", err
		}
		dest, err := os.OpenFile(candidate, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			source.Close()
			return "", err
		}
		_, copyErr := io.Copy(dest, io.LimitReader(source, maxArchive+1))
		source.Close()
		syncErr := dest.Sync()
		closeErr := dest.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil {
			return "", errors.New("cannot extract rclone; check disk space")
		}
	}
	if !found {
		return "", errors.New("archive does not contain the expected rclone executable")
	}
	if err := checkBinary(ctx, candidate); err != nil {
		return "", err
	}
	if err := os.Rename(candidate, path); err != nil {
		return "", errors.New("cannot install verified rclone")
	}
	return path, nil
}
func download(ctx context.Context, client *http.Client, address string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("rclone download failed; internet access is required for initial setup")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("rclone download failed (HTTP %d)", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("invalid or oversized rclone download")
	}
	return data, nil
}
func downloadArchive(ctx context.Context, client *http.Client, address, path, expected string) error {
	request, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("rclone archive download failed; retry with internet access")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("rclone archive download failed (HTTP %d)", response.StatusCode)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxArchive+1))
	if err != nil || n > maxArchive {
		return errors.New("incomplete or oversized rclone archive")
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return errors.New("rclone checksum mismatch; refusing to execute")
	}
	return file.Sync()
}

// Go names macOS darwin; rclone publishes macOS archives under osx.
func archiveName(goos, arch string) string {
	platform := goos
	if platform == "darwin" {
		platform = "osx"
	}
	return fmt.Sprintf("rclone-%s-%s-%s.zip", RcloneVersion, platform, arch)
}
