package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"od2gp/internal/onedrive"
)

type driveBrowser interface {
	List(context.Context, string, bool, func(onedrive.Item) error) error
}

type drivePreview struct {
	Folders                            []string
	Media                              []onedrive.Item
	FilesChecked                       int
	Limited, ListingFailed, ScanFailed bool
}

var errPreviewLimit = errors.New("preview limit reached")

func collectDrivePreview(ctx context.Context, source driveBrowser, folder string) (drivePreview, error) {
	var preview drivePreview
	if err := ctx.Err(); err != nil {
		return preview, err
	}
	budget, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	entries := 0
	err := source.List(budget, folder, false, func(item onedrive.Item) error {
		entries++
		if item.IsDir {
			if len(preview.Folders) < 20 {
				preview.Folders = append(preview.Folders, path.Join("/", folder, item.Path))
			} else {
				preview.Limited = true
			}
		}
		if entries >= 200 {
			return errPreviewLimit
		}
		return budget.Err()
	})
	preview.ListingFailed = err != nil && !errors.Is(err, errPreviewLimit) && budget.Err() == nil
	preview.Limited = preview.Limited || errors.Is(err, errPreviewLimit)
	if budget.Err() == nil {
		err = source.List(budget, folder, true, func(item onedrive.Item) error {
			if item.IsDir {
				return budget.Err()
			}
			preview.FilesChecked++
			if previewMediaKind(item) != "" {
				item.Path = path.Join("/", folder, item.Path)
				preview.Media = append(preview.Media, item)
			}
			if len(preview.Media) >= 10 || preview.FilesChecked >= 2000 {
				return errPreviewLimit
			}
			return budget.Err()
		})
		preview.ScanFailed = err != nil && !errors.Is(err, errPreviewLimit) && budget.Err() == nil
		preview.Limited = preview.Limited || errors.Is(err, errPreviewLimit)
	}
	preview.Limited = preview.Limited || budget.Err() != nil
	sort.Strings(preview.Folders)
	return preview, ctx.Err()
}

// These are filename/MIME hints for recognition, not migration eligibility.
func previewMediaKind(item onedrive.Item) string {
	if strings.HasPrefix(strings.ToLower(item.MimeType), "image/") {
		return "photo"
	}
	if strings.HasPrefix(strings.ToLower(item.MimeType), "video/") {
		return "video"
	}
	switch strings.ToLower(path.Ext(item.Path)) {
	case ".jpg", ".jpeg", ".png", ".heic", ".heif", ".gif", ".webp", ".avif", ".bmp", ".tif", ".tiff":
		return "photo"
	case ".mp4", ".mov", ".m4v", ".avi", ".mkv", ".wmv", ".mpg", ".mpeg", ".3gp", ".mts", ".m2ts":
		return "video"
	}
	return ""
}

func reviewDriveContents(ctx context.Context, reader *bufio.Reader, out io.Writer, source driveBrowser) (bool, error) {
	folder := ""
inspect:
	for {
		clearPromptScreen(out)
		fmt.Fprintf(out, "Inspecting %q: folder names and a media sample only (up to 15 seconds).\n", "/"+folder)
		preview, err := collectDrivePreview(ctx, source, folder)
		if err != nil {
			return false, err
		}
		fmt.Fprintln(out, "\nFolders found (up to 20):")
		for _, name := range preview.Folders {
			fmt.Fprintf(out, "  %q\n", name)
		}
		if len(preview.Folders) == 0 {
			fmt.Fprintln(out, "  No folders were returned in this sample.")
		}
		fmt.Fprintf(out, "\nPhoto/video examples from %d file names checked (up to 10 examples):\n", preview.FilesChecked)
		for _, item := range preview.Media {
			fmt.Fprintf(out, "  %s: %q (%.1f MiB)\n", previewMediaKind(item), item.Path, float64(max(item.Size, 0))/(1<<20))
		}
		if len(preview.Media) == 0 {
			fmt.Fprintln(out, "  No photo/video examples found. This does not prove the drive is empty.")
		}
		if preview.Limited {
			fmt.Fprintln(out, "Sample limit reached. These are not complete drive totals.")
		}
		if preview.ListingFailed || preview.ScanFailed {
			fmt.Fprintln(out, "Some metadata could not be listed. Try a known folder or another drive.")
		}
		fmt.Fprintln(out, "No media was downloaded or uploaded. Planning checks eligibility separately.\nThis chooses the drive only; choose the migration folder when creating a plan.\n\n1) Use this drive (proceed to folder selection)\n2) Choose another drive\n3) Inspect a folder on this drive\n0) Cancel and keep the previous drive")
		for {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			fmt.Fprint(out, "Choose after reviewing the names [1]: ")
			choice, err := readSetupLine(reader)
			if err != nil {
				return false, err
			}
			if choice == "" {
				choice = "1"
			}
			switch choice {
			case "1":
				return true, nil
			case "2":
				return false, nil
			case "0":
				return false, context.Canceled
			case "3":
				fmt.Fprint(out, "OneDrive folder to inspect (e.g. /Pictures/Camera Roll; / for root): ")
				name, err := readSetupLine(reader)
				if err != nil {
					return false, err
				}
				name = strings.Trim(strings.TrimPrefix(name, "onedrive:"), "/")
				valid := !strings.ContainsAny(name, "\\\x00")
				for _, part := range strings.Split(name, "/") {
					if part == ".." {
						valid = false
					}
				}
				if !valid {
					fmt.Fprintln(out, "Use a OneDrive folder path with forward slashes and without ..")
					continue
				}
				folder = name
				continue inspect
			default:
				fmt.Fprintln(out, "Choose 1, 2, 3 or 0.")
			}
		}
	}
}
