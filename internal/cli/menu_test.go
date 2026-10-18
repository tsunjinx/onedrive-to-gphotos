package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
	"od2gp/internal/journal"
	"od2gp/internal/migration"
)

func TestMenuPlansAndConfirmsWithoutLosingBufferedAnswers(t *testing.T) {
	id := strings.Repeat("a", 32)
	var out, errOut bytes.Buffer
	var calls [][]string
	app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("2\n/Pictures/Trip\n\nyes\n\n0\n")), out: &out, errOut: &errOut, jobs: func() ([]menuJob, error) { return nil, nil }}
	app.dispatch = func(args []string, input io.Reader, output, _ io.Writer) int {
		calls = append(calls, args)
		if args[0] == "source" {
			return 0
		}
		if args[0] == "plan" {
			fmt.Fprintln(output, "Job:", id)
		} else {
			// Existing commands create their own buffered readers. They must not
			// consume the subsequent menu pause/exit from piped or pasted input.
			line, _ := bufio.NewReader(input).ReadString('\n')
			if line != "yes\n" {
				t.Fatalf("confirmation lost: %q", line)
			}
		}
		return 0
	}
	if code := app.run(); code != 0 {
		t.Fatalf("menu failed: code=%d %s", code, errOut.String())
	}
	want := [][]string{{"source", "select-drive"}, {"plan", "--source", "/Pictures/Trip", "--cache-gib", "30"}, {"run", "--job", id, "--wait"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("wrong commands: %#v", calls)
	}
}

func TestMenuHasExplicitDriveSelectionAndAbortsNewPlanOnCancel(t *testing.T) {
	for _, choice := range []string{"7", "2"} {
		var out, errOut bytes.Buffer
		calls := 0
		app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader(choice + "\n\n0\n")), out: &out, errOut: &errOut,
			jobs: func() ([]menuJob, error) { return nil, nil },
			dispatch: func(args []string, _ io.Reader, _, _ io.Writer) int {
				calls++
				if !reflect.DeepEqual(args, []string{"source", "select-drive"}) {
					t.Fatalf("planned before drive selection: %v", args)
				}
				return 1
			}}
		if code := app.run(); code != 0 || calls != 1 {
			t.Fatalf("drive picker missing or cancellation ignored: code=%d calls=%d", code, calls)
		}
	}
}

func TestMenuChoosesSavedJobWithoutTypingID(t *testing.T) {
	for _, started := range []bool{false, true} {
		var out, errOut bytes.Buffer
		id := strings.Repeat("b", 32)
		var called []string
		app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("\n\n\n0\n")), out: &out, errOut: &errOut,
			jobs: func() ([]menuJob, error) {
				return []menuJob{{ID: id, Folder: "Pictures", Incomplete: true, Started: started}}, nil
			},
			dispatch: func(args []string, _ io.Reader, _, _ io.Writer) int { called = args; return 0 }}
		if code := app.run(); code != 0 {
			t.Fatal(code)
		}
		command := "run"
		if started {
			command = "resume"
		}
		if !reflect.DeepEqual(called, []string{command, "--job", id, "--wait"}) {
			t.Fatalf("wrong continuation: %#v", called)
		}
	}
}

func TestMenuExportRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.csv")
	if err := os.WriteFile(path, []byte("keep existing report"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("5\n\n\n" + path + "\n\n0\n")), out: &out, errOut: &errOut,
		jobs:     func() ([]menuJob, error) { return []menuJob{{ID: strings.Repeat("c", 32)}}, nil },
		dispatch: func([]string, io.Reader, io.Writer, io.Writer) int { t.Fatal("overwrote existing report"); return 1 }}
	if code := app.run(); code != 0 {
		t.Fatal(code)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep existing report" || !strings.Contains(errOut.String(), "exists") {
		t.Fatalf("existing report changed: %q %v %s", data, err, errOut.String())
	}
}

func TestMenuRejectsInvalidChoicesAndExitsOnEOF(t *testing.T) {
	var out, errOut bytes.Buffer
	app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("not-a-number\n")), out: &out, errOut: &errOut,
		jobs: func() ([]menuJob, error) { return nil, nil }, dispatch: func([]string, io.Reader, io.Writer, io.Writer) int { t.Fatal("unexpected action"); return 1 }}
	if code := app.run(); code != 0 || !strings.Contains(out.String(), "Choose") {
		t.Fatalf("invalid input handling: %d %s", code, out.String())
	}
}

func TestMenuLoadsSavedProgressAndPrioritizesUnfinishedJobs(t *testing.T) {
	keyring.MockInit()
	root := t.TempDir()
	for i, state := range []string{"completed", "processing", "discovered"} {
		id := strings.Repeat(fmt.Sprint(i+1), 32)
		key, err := journal.Key(id, true)
		if err != nil {
			t.Fatal(err)
		}
		store, err := journal.Open(filepath.Join(root, "od2gp", "jobs", id, "progress.db"), key, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Save(0, "job", migration.Meta{ID: id, Folder: "Pictures", Created: time.Now().Add(-time.Duration(i) * time.Hour), InventoryComplete: true, ExcludePersonalVault: true, DriveID: "drive", GoogleSubject: "destination"}); err != nil {
			t.Fatal(err)
		}
		if err := store.Save(1, state, migration.Item{State: state}); err != nil {
			t.Fatal(err)
		}
		store.Close()
	}
	jobs, err := loadMenuJobs(root)
	if err != nil || len(jobs) != 3 {
		t.Fatalf("jobs unavailable: %v %v", jobs, err)
	}
	for _, job := range jobs {
		if !job.InventoryComplete || !job.ExcludePersonalVault || job.DriveID != "drive" || job.GoogleSubject != "destination" {
			t.Fatalf("saved scope or bindings lost: %+v", job)
		}
	}
	if !jobs[0].Incomplete || !jobs[0].Started || jobs[0].Processing != 1 || jobs[1].Started || jobs[2].Incomplete {
		t.Fatalf("wrong job ordering/progress: %+v", jobs)
	}
}

func TestExplicitMenuWorksWithInvalidSettingsAndPipedExit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "od2gp"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "od2gp", "config.json"), []byte("broken settings"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := RunContext(context.Background(), []string{"menu"}, strings.NewReader("0\n"), &out, &errOut, func(key string) string {
		if key == "OD2GP_CONFIG_DIR" {
			return root
		}
		return ""
	})
	if code != 0 || !strings.Contains(out.String(), "Set up or reconnect accounts") {
		t.Fatalf("menu inaccessible: %d %s", code, errOut.String())
	}
}

func TestNoArgumentsWithPipedInputDoesNotStartMenu(t *testing.T) {
	var out, errOut bytes.Buffer
	code := RunContext(context.Background(), nil, strings.NewReader("2\n"), &out, &errOut, func(key string) string {
		if key == "OD2GP_CONFIG_DIR" {
			return t.TempDir()
		}
		return ""
	})
	if code != 0 || !strings.Contains(out.String(), "Usage:") || strings.Contains(out.String(), "Choose [") {
		t.Fatalf("redirected command became interactive: %d %s", code, out.String())
	}
}

func TestMenuCancellationUnblocksInput(t *testing.T) {
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	root := t.TempDir()
	var out, errOut bytes.Buffer
	code := RunContext(ctx, []string{"menu"}, input, &out, &errOut, func(key string) string {
		if key == "OD2GP_CONFIG_DIR" {
			return root
		}
		return ""
	})
	if code != 1 || ctx.Err() == nil {
		t.Fatalf("menu ignored cancellation: %d", code)
	}
}

func TestMenuExportsReportToChosenFile(t *testing.T) {
	for _, format := range []string{"csv", "json"} {
		t.Run(format, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "report with spaces.txt")
			id := strings.Repeat("d", 32)
			var out, errOut bytes.Buffer
			app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("5\n\n" + format + "\n" + file + "\n\n0\n")), out: &out, errOut: &errOut,
				jobs: func() ([]menuJob, error) { return []menuJob{{ID: id}}, nil },
				dispatch: func(args []string, _ io.Reader, output, _ io.Writer) int {
					if !reflect.DeepEqual(args, []string{"report", "--job", id, "--format", format}) {
						t.Fatalf("wrong report command: %v", args)
					}
					fmt.Fprint(output, "report contents")
					return 0
				}}
			if code := app.run(); code != 0 {
				t.Fatal(code)
			}
			data, err := os.ReadFile(file)
			if err != nil || string(data) != "report contents" || !strings.Contains(out.String(), "Report saved:") {
				t.Fatalf("report not saved: %q %v", data, err)
			}
		})
	}
}

func TestMenuSetupDoesNotConsumeFollowingMenuInput(t *testing.T) {
	root := t.TempDir()
	client := filepath.Join(root, "client.json")
	if err := os.WriteFile(client, []byte(`{"installed":{"client_id":"id","client_secret":"secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	calls := 0
	app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("6\n1\n" + client + "\n\n0\n")), out: &out, errOut: &errOut,
		jobs: func() ([]menuJob, error) { return nil, nil },
		dispatch: func(args []string, input io.Reader, output, errors io.Writer) int {
			calls++
			if !reflect.DeepEqual(args, []string{"setup"}) {
				t.Fatalf("wrong setup command: %v", args)
			}
			return RunSetup(input, output, errors, func(key string) string {
				if key == "OD2GP_CONFIG_DIR" {
					return root
				}
				return ""
			}, func(string) error { return nil })
		}}
	if code := app.run(); code != 0 || calls != 1 {
		t.Fatalf("setup swallowed menu answers: %d calls=%d %s", code, calls, errOut.String())
	}
}
