package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestMenuOptionsPersistSeparatelyFromCredentials(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "od2gp")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "config.json")
	if err := os.WriteFile(config, []byte("keep OAuth settings"), 0600); err != nil {
		t.Fatal(err)
	}
	opts := transferOptions{Workers: 6, BatchSize: 25, Retries: 2, WaitVideos: false}
	if err := saveMenuOptions(root, opts); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadMenuOptions(root)
	if err != nil || loaded != opts {
		t.Fatalf("lost preferences: %+v %v", loaded, err)
	}
	data, _ := os.ReadFile(config)
	if string(data) != "keep OAuth settings" {
		t.Fatal("changed OAuth settings")
	}
	info, err := os.Stat(filepath.Join(dir, "menu.json"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatalf("permissions: %v %v", info, err)
	}
	opts.Workers = 9
	if err := saveMenuOptions(root, opts); err == nil {
		t.Fatal("accepted invalid options")
	}
	again, err := loadMenuOptions(root)
	if err != nil || again != loaded {
		t.Fatal("invalid edit overwrote saved options")
	}
}
func TestMenuOptionsDefaultsAndCorruption(t *testing.T) {
	root := t.TempDir()
	got, err := loadMenuOptions(root)
	if err != nil || got != defaultTransferOptions() {
		t.Fatalf("bad defaults: %+v %v", got, err)
	}
	dir := filepath.Join(root, "od2gp")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"broken", `{"workers":99,"batch_size":50,"retries":5}`, strings.Repeat("x", 4097)} {
		if err := os.WriteFile(filepath.Join(dir, "menu.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadMenuOptions(root); err == nil {
			t.Fatal("accepted corrupt options")
		}
	}
}
func TestMenuPresetPersistsAndContinueUsesIt(t *testing.T) {
	id := strings.Repeat("c", 32)
	var out bytes.Buffer
	var saved transferOptions
	var called []string
	app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("8\n3\n\n3\n1\n\n0\n")), out: &out, errOut: &out,
		jobs: func() ([]menuJob, error) {
			return []menuJob{{ID: id, Folder: "Pictures", Started: true, Incomplete: true}}, nil
		},
		saveOptions: func(opts transferOptions) error { saved = opts; return nil },
		dispatch:    func(args []string, _ io.Reader, _, _ io.Writer) int { called = args; return 0 }}
	if code := app.run(); code != 0 {
		t.Fatal(code)
	}
	if saved.Workers != 6 || saved.BatchSize != 50 {
		t.Fatalf("preset not saved: %+v", saved)
	}
	want := []string{"resume", "--job", id, "--wait", "--workers", "6", "--batch-size", "50", "--retries", "5"}
	if !reflect.DeepEqual(called, want) {
		t.Fatalf("settings not used: %v", called)
	}
}
func TestMenuCustomOptionsValidateAndKeepVideoChoice(t *testing.T) {
	var out bytes.Buffer
	var saved transferOptions
	app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("4\n9\n2\n51\n10\n11\n0\nmaybe\nno\n")), out: &out, errOut: &out, saveOptions: func(opts transferOptions) error { saved = opts; return nil }}
	app.configureTransfers()
	want := transferOptions{Workers: 2, BatchSize: 10, Retries: 0, WaitVideos: false}
	if saved != want {
		t.Fatalf("wrong custom options: %+v", saved)
	}
	args := app.transferArgs("resume", strings.Repeat("a", 32))
	if strings.Contains(strings.Join(args, " "), "--wait") {
		t.Fatal("ignored video wait choice")
	}
}
func TestMenuSettingsCancelAndSaveFailureKeepPrevious(t *testing.T) {
	for _, input := range []string{"0\n", "", "3\n", "4\nb\n"} {
		var out bytes.Buffer
		original := defaultTransferOptions()
		original.Workers = 2
		app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader(input)), out: &out, errOut: &out, options: original, saveOptions: func(transferOptions) error { return errors.New("disk unavailable") }}
		app.configureTransfers()
		if app.currentOptions() != original {
			t.Fatal("changed preferences after cancellation or failed save")
		}
	}
}
func TestMenuGuidesRecoveryAndNeverAssumesConsent(t *testing.T) {
	for _, choice := range []string{"0", "", "2"} {
		var out bytes.Buffer
		var called []string
		id := strings.Repeat("b", 32)
		app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("3\n1\n" + choice + "\n\n0\n")), out: &out, errOut: &out,
			jobs: func() ([]menuJob, error) {
				return []menuJob{{ID: id, Folder: "Pictures", Started: true, Incomplete: true, Uncertain: 2}}, nil
			},
			dispatch: func(args []string, _ io.Reader, _, _ io.Writer) int { called = args; return 0 }}
		app.run()
		if choice == "2" {
			if !strings.Contains(strings.Join(called, " "), "--reconcile") {
				t.Fatalf("recovery not dispatched: %v", called)
			}
		} else if called != nil {
			t.Fatalf("recovery without consent: %v", called)
		}
	}
}

func TestMenuRecoveryCanBeReviewedAndDoesNotPersistConsent(t *testing.T) {
	id := strings.Repeat("b", 32)
	job := menuJob{ID: id, Folder: "Pictures", Started: true, Incomplete: true, Uncertain: 1}
	var out bytes.Buffer
	var calls [][]string
	app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("3\n1\n1\n2\n\n3\n1\n\n0\n")), out: &out, errOut: &out,
		jobs: func() ([]menuJob, error) { return []menuJob{job}, nil },
		dispatch: func(args []string, _ io.Reader, _, _ io.Writer) int {
			calls = append(calls, args)
			if args[0] == "resume" {
				job.Uncertain = 0
			}
			return 0
		}}
	app.run()
	if len(calls) != 3 || calls[0][0] != "status" {
		t.Fatalf("review missing: %v", calls)
	}
	if !strings.Contains(strings.Join(calls[1], " "), "--reconcile") || strings.Contains(strings.Join(calls[2], " "), "--reconcile") {
		t.Fatalf("consent reused: %v", calls)
	}
}
func TestFasterPresetPreservesOtherChoices(t *testing.T) {
	var out bytes.Buffer
	opts := transferOptions{Workers: 2, BatchSize: 10, Retries: 0, WaitVideos: false}
	app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("3\n")), out: &out, errOut: &out, options: opts}
	app.configureTransfers()
	opts.Workers = 6
	if app.currentOptions() != opts {
		t.Fatalf("speed changed other choices: %+v", app.currentOptions())
	}
}

func TestRealMenuReloadsSavedPresetOnNextLaunch(t *testing.T) {
	root := t.TempDir()
	getenv := func(key string) string {
		if key == "OD2GP_CONFIG_DIR" {
			return root
		}
		return ""
	}
	var out, errOut bytes.Buffer
	if code := RunContext(context.Background(), []string{"menu"}, strings.NewReader("8\n3\n\n0\n"), &out, &errOut, getenv); code != 0 {
		t.Fatalf("save failed: %d %s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := RunContext(context.Background(), []string{"menu"}, strings.NewReader("0\n"), &out, &errOut, getenv); code != 0 || !strings.Contains(out.String(), "6 files at once") {
		t.Fatalf("next launch lost preset: %d %s %s", code, out.String(), errOut.String())
	}
}
