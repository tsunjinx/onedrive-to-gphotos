package cli

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"od2gp/internal/journal"
	"od2gp/internal/migration"
	"od2gp/internal/onedrive"
	"path/filepath"
	"strings"
	"testing"
)

func TestMenuSourceFiltersPersistAndCancel(t *testing.T) {
	root := t.TempDir()
	opts := defaultTransferOptions()
	opts.Language = "vi"
	opts.Workers = 6
	a := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("/Documents/Projects | /Apps\n")), out: io.Discard, errOut: io.Discard, options: opts, saveOptions: func(o transferOptions) error { return saveMenuOptions(root, o) }}
	a.configureSourceFilters()
	saved, err := loadMenuOptions(root)
	if err != nil || saved.ExcludedFolders != "/Documents/Projects | /Apps" || saved.Workers != 6 || saved.Language != "vi" {
		t.Fatalf("lost preferences: %+v %v", saved, err)
	}
	a.input = bufio.NewReader(strings.NewReader("/Bad/../Path\nb\n"))
	a.configureSourceFilters()
	if a.options != saved {
		t.Fatal("cancel changed filters")
	}
	a.input = bufio.NewReader(strings.NewReader("-\n"))
	a.configureSourceFilters()
	if a.options.ExcludedFolders != "" {
		t.Fatal("clear failed")
	}
}

func TestNewPlanUsesFiltersButContinueArgumentsDoNot(t *testing.T) {
	var calls [][]string
	var output bytes.Buffer
	opts := defaultTransferOptions()
	opts.ExcludedFolders = "/Documents/Projects | /Apps"
	a := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("/Pictures\n10\n")), out: &output, errOut: &output, options: opts, dispatch: func(args []string, input io.Reader, out, errOut io.Writer) int {
		calls = append(calls, append([]string(nil), args...))
		return 0
	}}
	a.newPlan()
	if len(calls) != 2 || strings.Join(calls[1], " ") != "plan --source /Pictures --cache-gib 10 --exclude-folder /Documents/Projects --exclude-folder /Apps" {
		t.Fatalf("wrong scope: %v", calls)
	}
	if strings.Contains(strings.Join(a.transferArgs("resume", "job"), " "), "exclude-folder") {
		t.Fatal("Continue changed saved scope")
	}
}

func TestFilterFlagsRejectExistingJobsBeforeConnecting(t *testing.T) {
	for _, args := range [][]string{{"--exclude-folder", "/"}, {"--exclude-folder", "/Pictures/../Other"}} {
		var output bytes.Buffer
		if code := migrationCommand(context.Background(), "plan", args, strings.NewReader(""), &output, &output, func(string) string { return "" }, t.TempDir()); code != 2 {
			t.Fatalf("invalid filter reached setup: %d %s", code, output.String())
		}
	}
	var output bytes.Buffer
	if code := migrationCommand(context.Background(), "resume", []string{"--exclude-folder", "/Private"}, strings.NewReader(""), &output, &output, func(string) string { return "" }, t.TempDir()); code != 2 {
		t.Fatalf("changed existing scope: %d %s", code, output.String())
	}
}

func TestFilteredReportsDisclosePolicyAndSkippedFiles(t *testing.T) {
	root := t.TempDir()
	store, err := journal.Open(filepath.Join(root, "job.db"), bytes.Repeat([]byte{7}, 32), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	meta := migration.Meta{ExcludedFolders: []string{"/Documents/Projects"}}
	item := migration.Item{Path: "Documents/Projects/icon.png", Source: onedrive.Item{Size: 12}, State: "skipped", Reason: "excluded by saved folder filter: /Documents/Projects"}
	if err := store.Save(1, item.State, item); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "csv"} {
		var out bytes.Buffer
		if err := writeMigrationReport(store, meta, format, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "folder_filter") || !strings.Contains(out.String(), "skipped") || !strings.Contains(out.String(), item.Path) {
			t.Fatalf("missing accounting: %s", out.String())
		}
	}
}
