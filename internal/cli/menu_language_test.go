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
	"regexp"
	"strings"
	"testing"
)

func TestMenuLanguagePersistsAndReloadsVietnamese(t *testing.T) {
	root := t.TempDir()
	getenv := func(key string) string {
		if key == "OD2GP_CONFIG_DIR" {
			return root
		}
		return ""
	}
	var out, errOut bytes.Buffer
	if code := RunContext(context.Background(), []string{"menu"}, strings.NewReader("9\n2\n\n0\n"), &out, &errOut, getenv); code != 0 {
		t.Fatalf("language selection failed: %d %s", code, errOut.String())
	}
	opts, err := loadMenuOptions(root)
	if err != nil || opts.Language != "vi" {
		t.Fatalf("language not saved: %+v %v", opts, err)
	}
	out.Reset()
	errOut.Reset()
	RunContext(context.Background(), []string{"menu"}, strings.NewReader("0\n"), &out, &errOut, getenv)
	for _, want := range []string{"Tiếp tục", "Tốc độ", "Ngôn ngữ", "tệp cùng lúc"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing Vietnamese %q: %s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "Continue a saved migration") {
		t.Fatal("main menu remained English")
	}
}
func TestLanguageSelectionPreservesTransferChoicesAndCanSwitchBack(t *testing.T) {
	var out bytes.Buffer
	opts := transferOptions{Workers: 6, BatchSize: 25, Retries: 0, WaitVideos: false}
	app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("2\n")), out: &out, errOut: &out, options: opts}
	app.configureLanguage()
	opts.Language = "vi"
	if app.currentOptions() != opts {
		t.Fatalf("changed transfer settings: %+v", app.currentOptions())
	}
	app.input = bufio.NewReader(strings.NewReader("1\n"))
	app.configureLanguage()
	opts.Language = "en"
	if app.currentOptions() != opts {
		t.Fatal("could not switch back to English")
	}
	app.options.Language = "vi"
	app.input = bufio.NewReader(strings.NewReader("1\n"))
	app.configureTransfers()
	if app.currentOptions().Language != "vi" {
		t.Fatal("recommended preset reset language")
	}
}
func TestVietnameseMenuPreservesCommandFlagsAndInputPaths(t *testing.T) {
	id := strings.Repeat("a", 32)
	var out bytes.Buffer
	var calls [][]string
	opts := defaultTransferOptions()
	opts.Language = "vi"
	app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("3\n1\n\n0\n")), out: &out, errOut: &out, options: opts,
		jobs: func() ([]menuJob, error) {
			return []menuJob{{ID: id, Folder: "Pictures/Tiếp tục", Started: true, Incomplete: true}}, nil
		},
		dispatch: func(args []string, _ io.Reader, _, _ io.Writer) int { calls = append(calls, args); return 0 }}
	app.run()
	want := []string{"resume", "--job", id, "--wait", "--workers", "3", "--batch-size", "50", "--retries", "5"}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("translated command protocol: %v", calls)
	}
	if !strings.Contains(out.String(), "Pictures/Tiếp tục") {
		t.Fatal("changed source path")
	}
}
func TestLanguageCancellationAndLegacyPreferences(t *testing.T) {
	var out bytes.Buffer
	opts := defaultTransferOptions()
	for _, answer := range []string{"0\n", ""} {
		app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader(answer)), out: &out, errOut: &out, options: opts}
		app.configureLanguage()
		if app.currentOptions() != opts {
			t.Fatal("cancel changed language")
		}
	}
	root := t.TempDir()
	if err := saveMenuOptions(root, opts); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadMenuOptions(root)
	if err != nil || loaded.Language != "" {
		t.Fatalf("legacy default changed: %+v %v", loaded, err)
	}
	loaded.Language = "unsupported"
	if err := saveMenuOptions(root, loaded); err == nil {
		t.Fatal("accepted unsupported language")
	}
}

func TestMenuCatalogPreservesFormatParameters(t *testing.T) {
	verbs := regexp.MustCompile(`%[a-zA-Z]`)
	for english, vietnamese := range vietnameseMenu {
		if !reflect.DeepEqual(verbs.FindAllString(english, -1), verbs.FindAllString(vietnamese, -1)) {
			t.Errorf("changed format parameters: %q", english)
		}
	}
}
func TestVietnameseReportKeepsMachineReadableData(t *testing.T) {
	id := strings.Repeat("b", 32)
	file := filepath.Join(t.TempDir(), "báo cáo.csv")
	var out bytes.Buffer
	opts := defaultTransferOptions()
	opts.Language = "vi"
	payload := "name,status\nSettings:,completed\n"
	app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("5\n1\ncsv\n" + file + "\n\n0\n")), out: &out, errOut: &out, options: opts,
		jobs: func() ([]menuJob, error) { return []menuJob{{ID: id}}, nil }, dispatch: func(args []string, _ io.Reader, w, _ io.Writer) int {
			if !reflect.DeepEqual(args, []string{"report", "--job", id, "--format", "csv"}) {
				t.Fatalf("translated report arguments: %v", args)
			}
			io.WriteString(w, payload)
			return 0
		}}
	app.run()
	data, err := os.ReadFile(file)
	if err != nil || string(data) != payload || !strings.Contains(out.String(), "Đã lưu báo cáo") {
		t.Fatalf("changed export: %q %v %s", data, err, out.String())
	}
}
func TestLanguageSaveFailureKeepsPreviousLanguage(t *testing.T) {
	var out bytes.Buffer
	opts := defaultTransferOptions()
	opts.Language = "vi"
	app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("1\n")), out: &out, errOut: &out, options: opts, saveOptions: func(transferOptions) error { return errors.New("disk full") }}
	app.configureLanguage()
	if app.currentOptions() != opts || !strings.Contains(out.String(), "Không lưu được ngôn ngữ") {
		t.Fatal("failed save changed language")
	}
}
