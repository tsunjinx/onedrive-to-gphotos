package cli

import (
	"bufio"
	"bytes"
	"context"
	"od2gp/internal/drivemigration"
	"od2gp/internal/journal"
	"od2gp/internal/onedrive"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDriveUsageFailsBeforeAuthentication(t *testing.T) {
	for _, args := range [][]string{{"run", "--job", "bad"}, {"plan", "--workers", "9"}, {"plan", "--source", "/../escape"}, {"plan", "--cache-gib", "0"}} {
		var out, errOut bytes.Buffer
		if c := driveTask(context.Background(), args, strings.NewReader(""), &out, &errOut, func(string) string { return "" }, t.TempDir()); c != 2 {
			t.Fatalf("invalid args contacted provider: %v code %d", args, c)
		}
	}
}
func TestDriveReportRedactsSessionsAndEscapesCSV(t *testing.T) {
	s, e := journal.Open(filepath.Join(t.TempDir(), "job.db"), make([]byte, 32), true)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	n := drivemigration.Node{Source: onedrive.Item{ID: "source", Path: "=formula.txt", Size: 1}, State: "retry", Session: "PRIVATE_SESSION_URL", TargetID: "target"}
	if e = s.Save(1, n.State, n); e != nil {
		t.Fatal(e)
	}
	for _, format := range []string{"json", "csv"} {
		var out bytes.Buffer
		if e = driveReport(s, drivemigration.Meta{ID: "job", Kind: drivemigration.Kind}, format, &out); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(out.String(), n.Session) {
			t.Fatal("session leaked in report")
		}
		if format == "csv" && !strings.Contains(out.String(), "'=formula.txt") {
			t.Fatal("CSV formula unescaped")
		}
	}
}
func TestDriveMenuDoesNotAuthenticateOnOpen(t *testing.T) {
	var out, errOut bytes.Buffer
	c := driveCommand(context.Background(), []string{"menu"}, strings.NewReader("0\n"), &out, &errOut, func(string) string { return "" }, t.TempDir())
	if c != 0 || !strings.Contains(out.String(), "Google Drive") || errOut.Len() != 0 {
		t.Fatalf("menu failed: %d %s", c, errOut.String())
	}
}

func TestDriveWorkersAreSeparateFromPhotos(t *testing.T) {
	a := menuApp{options: defaultTransferOptions()}
	args := a.driveRunArgs(strings.Repeat("1", 32))
	if args[len(args)-1] != "auto" {
		t.Fatal("Drive auto mode not default")
	}
	a.options.DriveWorkers = "2"
	args = a.driveRunArgs(strings.Repeat("1", 32))
	if args[len(args)-1] != "2" || a.options.Workers != 3 {
		t.Fatal("Drive setting changed Photos workers")
	}
}

func TestDriveReportDisclosesPathExclusions(t *testing.T) {
	s, e := journal.Open(filepath.Join(t.TempDir(), "job.db"), make([]byte, 32), true)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	m := drivemigration.Meta{ExcludedPaths: []string{"Documents/Notebook"}, VerifiedAt: time.Now()}
	for _, format := range []string{"json", "csv"} {
		var out bytes.Buffer
		if e := driveReport(s, m, format, &out); e != nil {
			t.Fatal(e)
		}
		for _, text := range []string{"/Documents/Notebook", "accepted_exclusion", "count and size unknown"} {
			if !strings.Contains(out.String(), text) {
				t.Fatalf("exclusion missing from %s: %s", format, out.String())
			}
		}
		if format == "csv" && !strings.Contains(out.String(), "job_verified_with_exclusions") {
			t.Fatal("claimed full coverage")
		}
	}
}
func TestDriveResumeMenuExcludesOnlyOnExplicitSelection(t *testing.T) {
	m := drivemigration.Meta{ID: strings.Repeat("1", 32)}
	for _, tc := range []struct {
		input    string
		excludes bool
	}{{"1\n", false}, {"2\n/Documents/Notebook\n", true}} {
		var out bytes.Buffer
		a := menuApp{ctx: context.Background(), out: &out, input: bufio.NewReader(strings.NewReader(tc.input)), options: defaultTransferOptions()}
		args, ok := a.driveResumeScopeArgs(m)
		if !ok {
			t.Fatal("menu cancelled unexpectedly")
		}
		got := strings.Contains(strings.Join(args, "|"), "--exclude-path|/Documents/Notebook")
		if got != tc.excludes {
			t.Fatalf("wrong exclusion args %v", args)
		}
	}
}

func TestDriveReportShowsAutoSkippedPackages(t *testing.T) {
	s, e := journal.Open(filepath.Join(t.TempDir(), "job.db"), make([]byte, 32), true)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	n := drivemigration.Node{Source: onedrive.Item{ID: "n", Path: "Documents/Notebook", Size: -1, OneDrivePackage: true}, State: "skipped", Reason: drivemigration.SkippedPackage}
	if e := s.Save(1, n.State, n); e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e := driveReport(s, drivemigration.Meta{VerifiedAt: time.Now()}, "csv", &out); e != nil {
		t.Fatal(e)
	}
	for _, value := range []string{"skipped_onedrive_package", "count and size unknown", "job_verified_with_exclusions"} {
		if !strings.Contains(out.String(), value) {
			t.Fatalf("missing %s", value)
		}
	}
}
