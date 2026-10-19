package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"od2gp/internal/migration"
)

type menuApp struct {
	ctx           context.Context
	input         *bufio.Reader
	out, errOut   io.Writer
	jobs          func() ([]menuJob, error)
	dispatch      func([]string, io.Reader, io.Writer, io.Writer) int
	options       transferOptions
	saveOptions   func(transferOptions) error
	startupNotice string
}

func interactiveMenu(ctx context.Context, input io.Reader, out, errOut io.Writer, getenv func(string) string, root string) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	app := menuApp{ctx: ctx, input: bufio.NewReader(newMenuInput(ctx, input)), out: out, errOut: errOut}
	options, err := loadMenuOptions(root)
	app.options = options
	if err != nil {
		app.startupNotice = err.Error() + ". Using recommended settings for now."
	}
	app.saveOptions = func(opts transferOptions) error { return saveMenuOptions(root, opts) }
	app.jobs = func() ([]menuJob, error) { return loadMenuJobs(root) }
	app.dispatch = func(args []string, input io.Reader, output, errors io.Writer) int {
		return RunContext(ctx, args, input, output, errors, getenv)
	}
	return app.run()
}

// Nested commands must not buffer and swallow subsequent menu answers.
type oneByteInput struct{ io.Reader }

func (r oneByteInput) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return r.Reader.Read(p[:1])
}

func (a *menuApp) execute(args []string, out io.Writer) int {
	code := a.dispatch(args, oneByteInput{a.input}, out, a.errOut)
	if args[0] == "run" || args[0] == "resume" {
		switch code {
		case 1:
			fmt.Fprintln(a.out, "Transfer stopped. Review the message above; choose Continue to use the same saved job.")
		case 3:
			fmt.Fprintln(a.out, "Videos are still processing. Choose Continue later to check them again.")
		case 4:
			fmt.Fprintln(a.out, "Some videos failed processing. View job progress or export a report for details.")
		}
	}
	return code
}

func (a *menuApp) ask(prompt, fallback string) (string, error) {
	if err := a.ctx.Err(); err != nil {
		return "", err
	}
	if fallback != "" {
		fmt.Fprintf(a.out, "%s [%s]: ", prompt, fallback)
	} else {
		fmt.Fprintf(a.out, "%s: ", prompt)
	}
	line, err := readSetupLine(a.input)
	if err != nil {
		return "", err
	}
	if line == "" {
		line = fallback
	}
	return line, nil
}

func (a *menuApp) run() int {
	notice := a.startupNotice
	for {
		if a.ctx.Err() != nil {
			return 1
		}
		clearPromptScreen(a.out)
		fmt.Fprintln(a.out, "OneDrive → Google Photos\nCopy photos and videos. OneDrive originals are kept.")
		fmt.Fprintln(a.out, "Settings:", a.currentOptions().summary())
		if notice != "" {
			fmt.Fprintln(a.out, notice)
			notice = ""
		}
		jobs, err := a.jobs()
		if err != nil {
			fmt.Fprintln(a.errOut, err)
		}
		defaultChoice := "1"
		for _, job := range jobs {
			if job.Problem == "" && job.Incomplete {
				fmt.Fprintf(a.out, "\nReady to continue: %q (%d/%d eligible completed, %d processing)\n", "/"+job.Folder, job.Completed, job.Total-job.Skipped, job.Processing)
				defaultChoice = "3"
				break
			}
		}
		fmt.Fprintln(a.out, "\n1) Check both account connections\n2) New migration (choose drive, then preview)\n3) Continue a saved migration\n4) View job progress\n5) Export a report\n6) Set up or reconnect accounts\n7) Choose OneDrive drive\n8) Transfer speed & options\n0) Exit")
		choice, err := a.ask("Choose", defaultChoice)
		if err != nil {
			if a.ctx.Err() != nil {
				return 1
			}
			return 0
		}
		clearPromptScreen(a.out)
		switch choice {
		case "0":
			return 0
		case "1":
			a.checkConnections()
		case "2":
			a.newPlan()
		case "3", "4":
			job, ok := a.chooseJob(jobs, choice == "3")
			if ok {
				command := "status"
				args := []string{command, "--job", job.ID}
				if choice == "3" {
					command = "run"
					if job.Started {
						command = "resume"
					}
					args = a.transferArgs(command, job.ID)
					if job.Folder == "" && !job.InventoryComplete && !job.ExcludePersonalVault && !job.Started {
						exclude, proceed := a.chooseVaultPolicy()
						if !proceed {
							break
						}
						if exclude {
							args = append(args, "--exclude-personal-vault")
						}
					}
					if !job.Started && !a.confirmOverlap(job, jobs) {
						break
					}
					if job.Uncertain > 0 {
						if !a.confirmRecovery(job) {
							break
						}
						args = append(args, "--reconcile")
					}

				}
				if choice == "3" {
					a.startTransfer(args)
				} else {
					a.execute(args, a.out)
				}
			}
		case "5":
			a.exportReport(jobs)
		case "6":
			a.accounts()
		case "7":
			a.execute([]string{"source", "select-drive"}, a.out)
		case "8":
			a.configureTransfers()
		default:
			notice = "Choose a number from 0 to 8."
			continue
		}
		if a.ctx.Err() != nil {
			return 1
		}
		if _, err := a.ask("Press Enter to return to the menu", ""); err != nil {
			if a.ctx.Err() != nil {
				return 1
			}
			return 0
		}
	}
}

func (a *menuApp) checkConnections() {
	one := a.execute([]string{"auth", "check", "onedrive"}, a.out)
	if a.ctx.Err() != nil {
		return
	}
	google := a.execute([]string{"auth", "check", "google"}, a.out)
	if one == 0 && google == 0 {
		fmt.Fprintln(a.out, "Both connections work. You can plan or continue a migration.")
	}
}

func (a *menuApp) newPlan() {
	if a.execute([]string{"source", "select-drive"}, a.out) != 0 {
		return
	}
	clearPromptScreen(a.out)
	fmt.Fprintln(a.out, "Choose a OneDrive folder. / means the entire drive.\nA new plan has separate history; use Continue for an interrupted job.")
	folder, err := a.ask("OneDrive folder", "/Pictures")
	if err != nil {
		return
	}
	var cache int
	for {
		answer, err := a.ask("Maximum staging space in GiB", "30")
		if err != nil {
			return
		}
		cache, err = strconv.Atoi(answer)
		if err == nil && cache >= 1 && cache <= 1024 {
			break
		}
		fmt.Fprintln(a.out, "Enter a whole number from 1 to 1024. The default is 30.")
	}
	planArgs := []string{"plan", "--source", folder, "--cache-gib", strconv.Itoa(cache)}
	if normalizeMenuFolder(folder) == "" {
		exclude, proceed := a.chooseVaultPolicy()
		if !proceed {
			return
		}
		if exclude {
			planArgs = append(planArgs, "--exclude-personal-vault")
		}
	}
	var preview bytes.Buffer
	if a.execute(planArgs, io.MultiWriter(a.out, &preview)) != 0 {
		return
	}
	for _, line := range strings.Split(preview.String(), "\n") {
		if id, ok := strings.CutPrefix(line, "Job: "); ok && migration.ValidID(id) {
			jobs, err := a.jobs()
			if err != nil {
				fmt.Fprintln(a.errOut, "Cannot check existing migration history; plan is saved. Resolve job access before starting uploads.")
				return
			}
			candidate := menuJob{ID: id, Folder: normalizeMenuFolder(folder)}
			for _, job := range jobs {
				if job.ID == id {
					candidate = job
					break
				}
			}
			if !a.confirmOverlap(candidate, jobs) {
				return
			}
			// Reuse run's account/summary confirmation; nothing uploads until yes.
			clearPromptScreen(a.out)
			a.startTransfer(a.transferArgs("run", id))
			return
		}
	}
	fmt.Fprintln(a.out, "Plan saved. Choose Continue from the menu when ready.")
}

func (a *menuApp) chooseJob(jobs []menuJob, unfinishedOnly bool) (menuJob, bool) {
	var choices []menuJob
	for _, job := range jobs {
		if unfinishedOnly && job.Problem == "" && !job.Incomplete {
			continue
		}
		choices = append(choices, job)
		label := fmt.Sprintf("%q — %d/%d eligible completed, %d processing, %d skipped — %s (%s)", "/"+job.Folder, job.Completed, job.Total-job.Skipped, job.Processing, job.Skipped, job.Created.Local().Format("2006-01-02 15:04"), job.ID[:8])
		if !job.InventoryComplete && !job.Started {
			label += " — inventory incomplete"
		}
		if job.Uncertain > 0 {
			label += fmt.Sprintf(" — needs recovery (%d)", job.Uncertain)
		}
		if job.ExcludePersonalVault {
			label += " — Vault excluded"
		}
		if job.Problem != "" {
			label = job.ID[:8] + " — " + job.Problem
		}
		fmt.Fprintf(a.out, "%d) %s\n", len(choices), label)
	}
	if len(choices) == 0 {
		fmt.Fprintln(a.out, "No matching saved jobs. Choose Plan a new migration to begin.")
		return menuJob{}, false
	}
	fmt.Fprintln(a.out, "0) Back")
	for {
		answer, err := a.ask("Choose a job", "1")
		if err != nil || answer == "0" {
			return menuJob{}, false
		}
		number, err := strconv.Atoi(answer)
		if err == nil && number >= 1 && number <= len(choices) {
			job := choices[number-1]
			if job.Problem != "" {
				fmt.Fprintln(a.out, job.Problem)
				continue
			}
			return job, true
		}
		fmt.Fprintln(a.out, "Choose one of the displayed job numbers.")
	}
}

func (a *menuApp) exportReport(jobs []menuJob) {
	job, ok := a.chooseJob(jobs, false)
	if !ok {
		return
	}
	var format string
	for {
		var err error
		format, err = a.ask("Report format: csv or json", "csv")
		if err != nil {
			return
		}
		if format == "csv" || format == "json" {
			break
		}
		fmt.Fprintln(a.out, "Enter csv or json.")
	}
	extension := format
	if format == "json" {
		extension = "jsonl"
	}
	name, err := a.ask("Save report to", "migration-"+job.ID[:8]+"."+extension)
	if err != nil {
		return
	}
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			fmt.Fprintln(a.errOut, "That file already exists. Choose a different report name.")
		} else {
			fmt.Fprintln(a.errOut, "Cannot create that report; check the folder and permissions.")
		}
		return
	}
	code := a.execute([]string{"report", "--job", job.ID, "--format", format}, file)
	closeErr := file.Close()
	if code != 0 || closeErr != nil {
		os.Remove(name)
		fmt.Fprintln(a.errOut, "Report export failed; incomplete output was removed.")
		return
	}
	absolute, err := filepath.Abs(name)
	if err != nil {
		absolute = name
	}
	fmt.Fprintf(a.out, "Report saved: %q\n", absolute)
}

func (a *menuApp) accounts() {
	fmt.Fprintln(a.out, "1) Configure Google client JSON\n2) Sign in to OneDrive\n3) Sign in to Google\n4) Check both connections\n5) Choose OneDrive drive\n0) Back")
	choice, err := a.ask("Choose", "4")
	if err != nil {
		return
	}
	switch choice {
	case "1":
		a.execute([]string{"setup"}, a.out)
	case "2":
		a.execute([]string{"auth", "login", "onedrive"}, a.out)
	case "3":
		a.execute([]string{"auth", "login", "google"}, a.out)
	case "4":
		a.checkConnections()
	case "5":
		a.execute([]string{"source", "select-drive"}, a.out)
	case "0":
	default:
		fmt.Fprintln(a.out, "Choose a number from 0 to 5 next time.")
	}
}
