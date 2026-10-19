package cli

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"od2gp/internal/googlephotos"
	"od2gp/internal/identity"
	"od2gp/internal/joblock"
	"od2gp/internal/journal"
	"od2gp/internal/migration"
	"od2gp/internal/staging"
)

func migrationCommand(ctx context.Context, command string, args []string, input io.Reader, out, errOut io.Writer, getenv func(string) string, root string) int {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(errOut)
	jobID := flags.String("job", "", "saved job ID")
	folder := flags.String("source", "/", "OneDrive folder")
	cacheGiB := flags.Int64("cache-gib", 30, "maximum encrypted staging size in GiB")
	yes := flags.Bool("yes", false, "start uploads without an interactive confirmation")
	format := flags.String("format", "json", "report format: json or csv")
	excludeVault := flags.Bool("exclude-personal-vault", false, "explicitly omit inaccessible /Personal Vault from a root scan; saved with the job")
	reconcile := flags.Bool("reconcile", false, "allow identical-byte retry of an uncertain creation")
	wait := flags.Bool("wait", false, "check pending videos for up to 30 minutes")
	batchSize := flags.Int("batch-size", 50, "maximum items per sequential Google creation batch (1-50)")
	workers := flags.Int("workers", 3, "parallel file transfers (1-8); library creation remains sequential")
	retries := flags.Int("retries", 5, "automatic retries per file for temporary Google failures (0-10)")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return 2
	}
	if *excludeVault && command != "plan" && command != "run" && command != "resume" {
		fmt.Fprintln(errOut, "--exclude-personal-vault is supported by plan, run and resume")
		return 2
	}
	if *retries < 0 || *retries > 10 {
		fmt.Fprintln(errOut, "--retries must be between 0 and 10")
		return 2
	}
	if *workers < 1 || *workers > 8 {
		fmt.Fprintln(errOut, "--workers must be between 1 and 8")
		return 2
	}
	if *batchSize < 1 || *batchSize > 50 {
		fmt.Fprintln(errOut, "--batch-size must be between 1 and 50")
		return 2
	}
	batchSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "batch-size" {
			batchSet = true
		}
	})
	if batchSet && command != "run" && command != "resume" {
		fmt.Fprintln(errOut, "--batch-size is supported by run and resume")
		return 2
	}
	workersSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "workers" {
			workersSet = true
		}
	})
	if workersSet && command != "run" && command != "resume" {
		fmt.Fprintln(errOut, "--workers is supported by run and resume")
		return 2
	}
	retriesSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "retries" {
			retriesSet = true
		}
	})
	if retriesSet && command != "run" && command != "resume" {
		fmt.Fprintln(errOut, "--retries is supported by run and resume")
		return 2
	}
	if command == "plan" && *jobID != "" {
		fmt.Fprintln(errOut, "plan creates a new job; use resume for an existing job")
		return 2
	}
	if *wait && command != "run" && command != "resume" {
		fmt.Fprintln(errOut, "--wait is supported by run and resume")
		return 2
	}
	if command == "status" && *jobID == "" {
		return listMigrationJobs(root, out, errOut)
	}
	if command != "plan" && !migration.ValidID(*jobID) {
		fmt.Fprintln(errOut, "Provide --job JOB_ID from the plan output.")
		return 2
	}
	if command == "plan" && (*cacheGiB < 1 || *cacheGiB > 1024) {
		fmt.Fprintln(errOut, "--cache-gib must be between 1 and 1024")
		return 2
	}
	if command == "report" && *format != "json" && *format != "csv" {
		fmt.Fprintln(errOut, "--format must be json or csv")
		return 2
	}
	jobReady := false
	fail := func(err error) int {
		fmt.Fprintln(errOut, err)
		if jobReady {
			fmt.Fprintf(errOut, "Saved job: %s. Resume with: od2gp resume --job %s\n", *jobID, *jobID)
		}
		return 1
	}
	selected := strings.Trim(strings.TrimPrefix(*folder, "onedrive:"), "/")
	if command == "plan" {
		if strings.Contains(selected, "\\") || strings.ContainsRune(selected, 0) {
			return fail(errors.New("use forward slashes in the source folder"))
		}
		for _, part := range strings.Split(selected, "/") {
			if part == ".." {
				return fail(errors.New("source folder must not contain .."))
			}
		}
	}
	if command == "plan" && *excludeVault && selected != "" {
		return fail(errors.New("--exclude-personal-vault is only needed for a drive-root plan"))
	}
	var source migration.Source
	var microsoftSession identity.Session
	backend := sourceBackend(getenv)
	if command != "plan" {
		backend = "rclone"
	}
	excludePolicy := *excludeVault
	var photos googlephotos.Client
	var session identity.Session
	var driveID string
	var sourceUnlock func()
	defer func() {
		if sourceUnlock != nil {
			sourceUnlock()
		}
	}()
	connect := func() error {
		provider, err := identity.Configure("google", getenv)
		if err != nil {
			return err
		}
		sourceUnlock, err = lockSource(root)
		if err != nil {
			return err
		}
		if err := validateBackend(backend); err != nil {
			return err
		}
		if backend == "graph" {
			fmt.Fprintln(errOut, "Connecting directly to Microsoft Graph...")
			native, signedIn, sourceErr := graphSourceClient(ctx, getenv)
			if sourceErr != nil {
				return sourceErr
			}
			native.ExcludePersonalVault = excludePolicy
			source = native
			microsoftSession = signedIn
		} else {
			legacy, sourceErr := sourceClient(ctx, getenv, root, errOut)
			if sourceErr != nil {
				return sourceErr
			}
			legacy.ExcludePersonalVault = excludePolicy
			source = legacy
		}
		httpClient, signedIn, err := identity.GoogleClient(ctx, provider)
		if err != nil {
			return err
		}
		session = signedIn
		photos = googlephotos.Client{HTTP: httpClient}
		driveID, err = source.DriveID(ctx)
		return err
	}
	if command == "plan" {
		// A resumable job is created only after all provider preflight checks pass.
		if err := connect(); err != nil {
			return fail(err)
		}
		id, err := migration.ID()
		if err != nil {
			return fail(err)
		}
		*jobID = id
	}
	directory := filepath.Join(root, "od2gp", "jobs", *jobID)
	if command == "plan" {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return fail(err)
		}
	} else if _, err := os.Stat(filepath.Join(directory, "progress.db")); err != nil {
		return fail(errors.New("job journal not found"))
	}
	if command != "status" && command != "report" {
		unlock, err := joblock.Acquire(filepath.Join(directory, "job.lock"))
		if err != nil {
			return fail(err)
		}
		defer unlock()
	}
	key, err := journal.Key(*jobID, command == "plan")
	if err != nil {
		return fail(err)
	}
	store, err := journal.Open(filepath.Join(directory, "progress.db"), key, command == "plan")
	if err != nil {
		return fail(err)
	}
	defer store.Close()
	var meta migration.Meta
	if command != "plan" {
		if err := store.Load(0, &meta); err != nil {
			return fail(err)
		}
		jobReady = true
		backend = migrationBackend(meta)
		excludePolicy = meta.ExcludePersonalVault || *excludeVault
	}
	if command == "status" || command == "report" {
		if command == "report" {
			if err := writeMigrationReport(store, meta, *format, out); err != nil {
				return fail(err)
			}
			return 0
		}
		if err := migrationSummary(store, meta, out); err != nil {
			return fail(err)
		}
		return 0
	}
	if command != "plan" {
		if err := connect(); err != nil {
			return fail(err)
		}
	}
	engine := migration.Engine{Store: store, Source: source, Photos: photos, Directory: directory, Progress: errOut, Reconcile: *reconcile, MaxRetries: *retries, Workers: *workers, BatchSize: *batchSize}
	if command == "plan" {
		meta = migration.Meta{SourceBackend: backend, SourceSubject: microsoftSession.Account.Subject, SourceClient: microsoftSession.ClientID, ID: *jobID, Folder: selected, DriveID: driveID, GoogleSubject: session.Account.Subject, GoogleClient: session.ClientID, CacheBytes: *cacheGiB << 30, ExcludePersonalVault: *excludeVault, Created: time.Now().UTC()}
		if err := store.Save(0, "job", meta); err != nil {
			return fail(err)
		}
		jobReady = true
		fmt.Fprintf(errOut, "Building read-only plan. Saved job: %s\n", meta.ID)
		if err := engine.Discover(ctx, &meta); err != nil {
			return fail(err)
		}
		if err := migrationSummary(store, meta, out); err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "Start this job: od2gp run --job %s\n", meta.ID)
		return 0
	}
	if meta.DriveID != driveID {
		return fail(fmt.Errorf("this job belongs to a different OneDrive drive; use source select-drive with OD2GP_SOURCE_BACKEND=%s to restore its original drive", backend))
	}
	if backend == "graph" && (meta.SourceSubject != microsoftSession.Account.Subject || meta.SourceClient != microsoftSession.ClientID) {
		return fail(errors.New("saved Microsoft account/client identity does not match; refusing transfer"))
	}
	if meta.GoogleSubject != session.Account.Subject || meta.GoogleClient != session.ClientID {
		return fail(errors.New("saved Google account/client identity does not match; refusing transfer"))
	}
	if *excludeVault && !meta.ExcludePersonalVault {
		if err := enableVaultExclusion(&meta); err != nil {
			return fail(err)
		}
		if err := store.Save(0, "job", meta); err != nil {
			return fail(err)
		}
	}
	if meta.ExcludePersonalVault {
		fmt.Fprintln(errOut, vaultExclusionNotice)
	}
	// A failed plan is still a read-only inventory. Finish it before presenting
	// counts and asking for first-upload approval, including on direct resume.
	resumedInventory := !meta.InventoryComplete
	if resumedInventory {
		fmt.Fprintln(errOut, "Restarting incomplete inventory for the saved job; no uploads until the scan completes.")
		if err := engine.Discover(ctx, &meta); err != nil {
			return fail(err)
		}
	}
	if (command == "run" || resumedInventory) && !*yes {
		if err := migrationSummary(store, meta, out); err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "Upload this job to Google account %q? Type yes: ", session.Account.Email)
		line, err := readSetupLine(bufio.NewReader(input))
		if err != nil || line != "yes" {
			fmt.Fprintln(errOut, "Transfer cancelled.")
			return 1
		}
	}
	if err := engine.Run(ctx, &meta); err != nil {
		return fail(err)
	}
	if *wait {
		waitCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		err := engine.WaitForVideos(waitCtx, &meta)
		timedOut := waitCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil
		cancel()
		if err != nil {
			if !timedOut || !errors.Is(err, context.DeadlineExceeded) {
				return fail(err)
			}
			fmt.Fprintln(out, "Video wait limit reached. Saved progress is available for resume --wait.")
		}
	}
	if err := migrationSummary(store, meta, out); err != nil {
		return fail(err)
	}
	ids, err := store.IDs()
	if err != nil {
		return fail(err)
	}
	pending := false
	failed := false
	for _, id := range ids {
		var item migration.Item
		if err := store.Load(id, &item); err != nil {
			return fail(err)
		}
		if item.State == "processing" {
			pending = true
		}
		if item.State == "processing_failed" {
			failed = true
		}
	}
	if pending {
		fmt.Fprintln(out, "Google is still processing videos. Resume this job later to check readiness.")
		return 3
	}
	if failed {
		return 4
	}
	fmt.Fprintln(out, "Job finished. Review the report for any skipped files.")
	return 0
}

const vaultExclusionReason = "explicitly excluded folder; contents not scanned or copied; file count and size unknown"
const vaultExclusionNotice = "Excluded: /Personal Vault. Its contents were not scanned or copied; file count and size are unknown. This job does not cover that folder."

func enableVaultExclusion(meta *migration.Meta) error {
	if meta.InventoryComplete {
		return errors.New("cannot change source exclusions after inventory is complete")
	}
	if meta.Folder != "" {
		return errors.New("Personal Vault exclusion is supported only for a drive-root job")
	}
	meta.ExcludePersonalVault = true
	return nil
}

func migrationBackend(meta migration.Meta) string {
	if meta.SourceBackend == "" {
		return "rclone"
	}
	return meta.SourceBackend
}

func migrationSummary(store *journal.Store, meta migration.Meta, out io.Writer) error {
	ids, err := store.IDs()
	if err != nil {
		return err
	}
	counts := map[string]int{}
	var bytes, largest int64
	for _, id := range ids {
		var item migration.Item
		if err := store.Load(id, &item); err != nil {
			return err
		}
		counts[item.State]++
		if item.State != "skipped" {
			bytes += item.Source.Size
			if staging.Required(item.Source.Size) > largest {
				largest = staging.Required(item.Source.Size)
			}
		}
	}
	fmt.Fprintf(out, "Job: %s\nSource: OneDrive /%s\nFiles: %d; eligible bytes: %d\nCompleted: %d; processing: %d; skipped: %d; processing failures: %d\nCache cap: %.1f GiB; largest staged file: %.2f GiB\nInventory complete: %t\n", meta.ID, meta.Folder, len(ids), bytes, counts["completed"], counts["processing"], counts["skipped"], counts["processing_failed"], float64(meta.CacheBytes)/(1<<30), float64(largest)/(1<<30), meta.InventoryComplete)
	fmt.Fprintf(out, "Source backend: %s\n", migrationBackend(meta))
	if meta.ExcludePersonalVault {
		fmt.Fprintln(out, vaultExclusionNotice)
	}
	if counts["creating"] > 0 {
		fmt.Fprintf(out, "Uncertain destination creations: %d. Review the report before resume --reconcile.\n", counts["creating"])
	}
	fmt.Fprintln(out, "Uploads use Google original quality and count toward storage. Available destination capacity has not been checked.")
	if largest > meta.CacheBytes {
		fmt.Fprintln(out, "Cache capacity is too small for the largest file; increase --cache-gib in a new plan.")
	}
	if !meta.RetryAt.IsZero() && time.Now().Before(meta.RetryAt) {
		fmt.Fprintf(out, "Provider pause until: %s\n", meta.RetryAt.Local().Format(time.RFC3339))
	}
	return nil
}
func writeMigrationReport(store *journal.Store, meta migration.Meta, format string, out io.Writer) error {
	ids, err := store.IDs()
	if err != nil {
		return err
	}
	if format == "csv" {
		writer := csv.NewWriter(out)
		if err := writer.Write([]string{"source", "bytes", "state", "destination_id", "reason"}); err != nil {
			return err
		}
		if meta.ExcludePersonalVault {
			if err := writer.Write([]string{"/Personal Vault", "", "excluded_folder", "", vaultExclusionReason}); err != nil {
				return err
			}
		}
		for _, id := range ids {
			var item migration.Item
			if err := store.Load(id, &item); err != nil {
				return err
			}
			if err := writer.Write([]string{csvSafe(item.Path), fmt.Sprint(item.Source.Size), item.State, csvSafe(item.MediaID), csvSafe(item.Reason)}); err != nil {
				return err
			}
		}
		writer.Flush()
		return writer.Error()
	}
	encoder := json.NewEncoder(out)
	if meta.ExcludePersonalVault {
		if err := encoder.Encode(map[string]any{"source": "/Personal Vault", "state": "excluded_folder", "reason": vaultExclusionReason, "contents_count_known": false}); err != nil {
			return err
		}
	}

	for _, id := range ids {
		var item migration.Item
		if err := store.Load(id, &item); err != nil {
			return err
		}
		row := struct {
			Source      string `json:"source"`
			Bytes       int64  `json:"bytes"`
			State       string `json:"state"`
			Destination string `json:"destination_id,omitempty"`
			Reason      string `json:"reason,omitempty"`
		}{item.Path, item.Source.Size, item.State, item.MediaID, item.Reason}
		if err := encoder.Encode(row); err != nil {
			return err
		}
	}
	return nil
}
func csvSafe(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

func listMigrationJobs(root string, out, errOut io.Writer) int {
	entries, err := os.ReadDir(filepath.Join(root, "od2gp", "jobs"))
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(out, "No saved migration jobs.")
		return 0
	}
	if err != nil {
		fmt.Fprintln(errOut, "Cannot list saved migration jobs.")
		return 1
	}
	for _, entry := range entries {
		if !entry.IsDir() || !migration.ValidID(entry.Name()) {
			continue
		}
		fmt.Fprintf(out, "Saved job: %s (inspect with status --job %s)\n", entry.Name(), entry.Name())
	}
	return 0
}
