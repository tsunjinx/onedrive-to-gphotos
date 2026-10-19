package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"od2gp/internal/migration"
)

type transferOptions struct {
	Language        string `json:"language,omitempty"`
	ExcludedFolders string `json:"excluded_folders,omitempty"`
	Workers         int    `json:"workers"`
	BatchSize       int    `json:"batch_size"`
	Retries         int    `json:"retries"`
	WaitVideos      bool   `json:"wait_videos"`
}

func defaultTransferOptions() transferOptions {
	return transferOptions{Workers: 3, BatchSize: 50, Retries: 5, WaitVideos: true}
}
func (o transferOptions) valid() bool {
	_, err := menuExcludedFolders(o.ExcludedFolders)
	return err == nil && len(o.ExcludedFolders) <= 2048 && (o.Language == "" || o.Language == "en" || o.Language == "vi") && o.Workers >= 1 && o.Workers <= 8 && o.BatchSize >= 1 && o.BatchSize <= 50 && o.Retries >= 0 && o.Retries <= 10
}
func (o transferOptions) localizedSummary(language string) string {
	wait := "check videos once"
	if o.WaitVideos {
		wait = "wait for video processing"
	}
	return fmt.Sprintf(menuText(language, "%d files at once | batch up to %d | %d retries | %s"), o.Workers, o.BatchSize, o.Retries, menuText(language, wait))
}
func (a *menuApp) optionsSummary() string {
	opts := a.currentOptions()
	return opts.localizedSummary(opts.Language)
}
func loadMenuOptions(root string) (transferOptions, error) {
	defaults := defaultTransferOptions()
	f, err := os.Open(filepath.Join(root, "od2gp", "menu.json"))
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return defaults, errors.New("cannot read saved transfer settings")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	var opts transferOptions
	if err != nil || len(data) > 4096 || json.Unmarshal(data, &opts) != nil || !opts.valid() {
		return defaults, errors.New("saved transfer settings are invalid; choose Transfer speed & options to replace them")
	}
	return opts, nil
}
func saveMenuOptions(root string, opts transferOptions) error {
	if !opts.valid() {
		return errors.New("invalid transfer settings")
	}
	dir := filepath.Join(root, "od2gp")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("cannot create settings directory")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return errors.New("cannot secure settings directory")
	}
	data, err := json.MarshalIndent(opts, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".menu-*.tmp")
	if err != nil {
		return errors.New("cannot write transfer settings")
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(data, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.New("cannot write transfer settings")
	}
	if err = os.Rename(temp, filepath.Join(dir, "menu.json")); err != nil {
		return errors.New("cannot save transfer settings")
	}
	return nil
}
func (a *menuApp) currentOptions() transferOptions {
	if a.options.Workers == 0 {
		return defaultTransferOptions()
	}
	return a.options
}
func (a *menuApp) transferArgs(command, id string) []string {
	opts := a.currentOptions()
	args := []string{command, "--job", id}
	if opts.WaitVideos {
		args = append(args, "--wait")
	}
	return append(args, "--workers", strconv.Itoa(opts.Workers), "--batch-size", strconv.Itoa(opts.BatchSize), "--retries", strconv.Itoa(opts.Retries))
}
func (a *menuApp) startTransfer(args []string) {
	fmt.Fprintln(a.out, a.t("Transfer settings:"), a.optionsSummary())
	fmt.Fprintln(a.out, a.t("Ctrl+C saves progress and exits. Open the menu and choose Continue to pick up this job later."))
	a.execute(args, a.out)
}
func (a *menuApp) transferNumber(label string, value, minimum, maximum int) (int, bool) {
	label = a.t(label)
	for {
		answer, err := a.ask(fmt.Sprintf(a.t("%s (%d-%d, b to cancel)"), label, minimum, maximum), strconv.Itoa(value))
		if err != nil || strings.EqualFold(answer, "b") {
			return 0, false
		}
		number, err := strconv.Atoi(answer)
		if err == nil && number >= minimum && number <= maximum {
			return number, true
		}
		fmt.Fprintf(a.out, a.t("Enter a number from %d to %d.\n"), minimum, maximum)
	}
}
func (a *menuApp) configureTransfers() {
	fmt.Fprintln(a.out, a.t("Transfer speed & options\nCurrent:"), a.optionsSummary())
	fmt.Fprintln(a.out, a.t("1) Recommended: 3 files, batch 50, 5 retries, wait for videos\n2) Gentle: 1 file at once (try this if requests keep failing)\n3) Faster: 6 files at once\n4) Custom settings\n0) Back"))
	fmt.Fprintln(a.out, a.t("Settings are remembered for New migration and Continue. Your saved folder and staging limit stay the same."))
	for {
		choice, err := a.ask("Choose a preset", "0")
		if err != nil || choice == "0" {
			return
		}
		opts := a.currentOptions()
		switch choice {
		case "1":
			language := opts.Language
			filters := opts.ExcludedFolders
			opts = defaultTransferOptions()
			opts.Language = language
			opts.ExcludedFolders = filters
		case "2":
			opts.Workers = 1
		case "3":
			opts.Workers = 6
		case "4":
			fmt.Fprintln(a.out, a.t("Press Enter to keep a value, or type b to cancel without saving."))
			opts = a.currentOptions()
			var ok bool
			if opts.Workers, ok = a.transferNumber("Files to upload at once", opts.Workers, 1, 8); !ok {
				return
			}
			if opts.BatchSize, ok = a.transferNumber("Maximum files to create per batch", opts.BatchSize, 1, 50); !ok {
				return
			}
			if opts.Retries, ok = a.transferNumber("Retries after a temporary failure", opts.Retries, 0, 10); !ok {
				return
			}
			for {
				fallback := "no"
				if opts.WaitVideos {
					fallback = "yes"
				}
				answer, err := a.ask("Wait up to 30 minutes for videos? yes/no", fallback)
				if err != nil {
					return
				}
				switch strings.ToLower(answer) {
				case "b":
					return
				case "yes", "y", "có", "co":
					opts.WaitVideos = true
				case "no", "n", "không", "khong":
					opts.WaitVideos = false
				default:
					fmt.Fprintln(a.out, a.t("Enter yes or no."))
					continue
				}
				break
			}
		default:
			fmt.Fprintln(a.out, a.t("Choose a number from 0 to 4."))
			continue
		}
		if a.saveOptions != nil {
			if err := a.saveOptions(opts); err != nil {
				fmt.Fprintln(a.errOut, a.t("Could not save transfer settings. Previous settings are kept; check local folder permissions and disk space."))
				return
			}
		}
		a.options = opts
		fmt.Fprintln(a.out, a.t("Saved:"), opts.localizedSummary(opts.Language))
		return
	}
}

func (a *menuApp) confirmRecovery(job menuJob) bool {
	fmt.Fprintf(a.out, a.t("%d file(s) need creation recovery for this job. Google may already have accepted them before the response was lost.\n"), job.Uncertain)
	fmt.Fprintln(a.out, a.t("Recovery verifies the retained original files, retries those uncertain items, then continues this same job. Confirmed completed files remain skipped."))
	for {
		fmt.Fprintln(a.out, a.t("1) Review job progress first\n2) Retry uncertain items using the verified original files\n0) Back without retrying"))
		choice, err := a.ask("Choose recovery action", "0")
		if err != nil {
			return false
		}
		switch choice {
		case "0":
			return false
		case "1":
			a.execute([]string{"status", "--job", job.ID}, a.out)
		case "2":
			return true
		default:
			fmt.Fprintln(a.out, a.t("Choose 1, 2 or 0."))
		}
	}
}

func menuExcludedFolders(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parts := strings.Split(value, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return migration.NormalizeExcludedFolders(parts)
}
func (a *menuApp) configureSourceFilters() {
	fmt.Fprintln(a.out, a.t("Source folder filters (new migrations only). Continue keeps its saved filter."))
	fmt.Fprintln(a.out, a.t("Enter drive-root folders separated by |, e.g. /Documents/Projects | /Apps. Type - to clear, or b to cancel."))
	fmt.Fprintln(a.out, a.t("Files are still scanned and shown as skipped in reports. No folders are excluded automatically."))
	for {
		opts := a.currentOptions()
		answer, err := a.ask("Folders to exclude", opts.ExcludedFolders)
		if err != nil || strings.EqualFold(answer, "b") {
			return
		}
		if answer == "-" {
			answer = ""
		}
		folders, err := menuExcludedFolders(answer)
		if err != nil {
			fmt.Fprintln(a.errOut, a.t("Use absolute folder paths such as /Documents/Projects; do not use . or .. segments."))
			continue
		}
		opts.ExcludedFolders = strings.Join(folders, " | ")
		if a.saveOptions != nil {
			if err := a.saveOptions(opts); err != nil {
				fmt.Fprintln(a.errOut, a.t("Could not save transfer settings. Previous settings are kept; check local folder permissions and disk space."))
				return
			}
		}
		a.options = opts
		fmt.Fprintln(a.out, a.t("Saved filters for new migrations:"), opts.ExcludedFolders)
		return
	}
}
