package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"od2gp/internal/onedrive"
)

type previewSource struct {
	folders []onedrive.Item
	files   []onedrive.Item
	visits  int
	probed  []string
	failure error
}

func (s *previewSource) List(_ context.Context, folder string, recursive bool, visit func(onedrive.Item) error) error {
	if recursive {
		for _, item := range s.files {
			s.visits++
			if err := visit(item); err != nil {
				return err
			}
		}
	} else {
		s.probed = append(s.probed, folder)
		for _, item := range s.folders {
			if err := visit(item); err != nil {
				return err
			}
		}
	}
	return s.failure
}

func TestDrivePreviewShowsFoldersAndActualMediaPaths(t *testing.T) {
	source := &previewSource{folders: []onedrive.Item{{Path: "Pictures", IsDir: true}, {Path: "Documents", IsDir: true}}, files: []onedrive.Item{{Path: "notes.txt"}, {Path: "Pictures/Camera Roll/IMG_1234.HEIC", Size: 4 << 20}, {Path: "Movies/trip.MP4", Size: 20 << 20}}}
	var out bytes.Buffer
	for _, input := range []string{"1\n", "\n"} {
		out.Reset()
		accept, err := reviewDriveContents(context.Background(), bufio.NewReader(strings.NewReader(input)), &out, source)
		if err != nil || !accept {
			t.Fatalf("review failed for input %q: %v", input, err)
		}
	}
	for _, text := range []string{"Pictures", "Documents", "IMG_1234.HEIC", "trip.MP4", "sample"} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("missing %s: %s", text, out.String())
		}
	}
	if strings.Contains(out.String(), "notes.txt") {
		t.Fatal("non-media shown as a media example")
	}
}

func TestDrivePreviewStopsAtSampleAndFileLimits(t *testing.T) {
	for _, media := range []bool{false, true} {
		source := &previewSource{}
		for i := 0; i < 3000; i++ {
			name := fmt.Sprintf("file-%d.txt", i)
			if media {
				name = fmt.Sprintf("photo-%d.jpg", i)
			}
			source.files = append(source.files, onedrive.Item{Path: name})
		}
		preview, err := collectDrivePreview(context.Background(), source, "")
		if err != nil || !preview.Limited || source.visits > 2000 || len(preview.Media) > 10 {
			t.Fatalf("unbounded preview: %+v %v visits=%d", preview, err, source.visits)
		}
		if media && source.visits != 10 {
			t.Fatalf("did not stop after ten samples: %d", source.visits)
		}
	}
}

func TestDrivePreviewCanInspectFolderTryAnotherOrCancel(t *testing.T) {
	for _, answer := range []string{"2\n", "0\n", "3\n/Pictures/Camera Roll\n1\n"} {
		source := &previewSource{files: []onedrive.Item{{Path: "IMG.jpg"}}}
		var out bytes.Buffer
		accepted, err := reviewDriveContents(context.Background(), bufio.NewReader(strings.NewReader(answer)), &out, source)
		switch answer[0] {
		case '0':
			if !errors.Is(err, context.Canceled) || accepted {
				t.Fatalf("cancel accepted: %v", err)
			}
		case '2':
			if err != nil || accepted {
				t.Fatalf("did not request another drive: %v", err)
			}
		case '3':
			if err != nil || !accepted || len(source.probed) != 2 || source.probed[1] != "Pictures/Camera Roll" || !strings.Contains(out.String(), "Pictures/Camera Roll/IMG.jpg") {
				t.Fatalf("folder probe failed: %v %v %s", err, source.probed, out.String())
			}
		}
	}
}

func TestEmptyOrFailedPreviewDoesNotClaimAnEmptyDrive(t *testing.T) {
	source := &previewSource{failure: errors.New("private provider details")}
	var out bytes.Buffer
	_, err := reviewDriveContents(context.Background(), bufio.NewReader(strings.NewReader("0\n")), &out, source)
	if !errors.Is(err, context.Canceled) || !strings.Contains(out.String(), "does not prove") || strings.Contains(out.String(), "private provider") {
		t.Fatalf("misleading or unsafe preview: %v %s", err, out.String())
	}
}

func TestDrivePreviewBoundsFolderListAndRejectsParentPaths(t *testing.T) {
	source := &previewSource{}
	for i := 0; i < 1000; i++ {
		source.folders = append(source.folders, onedrive.Item{Path: fmt.Sprintf("folder-%d", i), IsDir: true})
	}
	preview, err := collectDrivePreview(context.Background(), source, "")
	if err != nil || len(preview.Folders) != 20 || !preview.Limited {
		t.Fatalf("folder list unbounded: %+v %v", preview, err)
	}
	source.probed = nil
	var out bytes.Buffer
	accepted, err := reviewDriveContents(context.Background(), bufio.NewReader(strings.NewReader("3\n../outside\n2\n")), &out, source)
	if err != nil || accepted || len(source.probed) != 1 {
		t.Fatalf("invalid probe reached remote: %v %v", source.probed, err)
	}
}
