package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"od2gp/internal/drivemigration"
	"od2gp/internal/googledrive"
	"od2gp/internal/identity"
	"od2gp/internal/joblock"
	"od2gp/internal/journal"
	"od2gp/internal/migration"
	"od2gp/internal/msgraph"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func driveTask(ctx context.Context, args []string, input io.Reader, out, errOut io.Writer, getenv func(string) string, root string) int {
	if len(args) == 0 {
		return 2
	}
	command := args[0]
	switch command {
	case "plan", "run", "resume", "status", "report", "verify":
	default:
		fmt.Fprintln(errOut, "Use drive menu, plan, run, resume, status, report or verify")
		return 2
	}
	fs := flag.NewFlagSet("drive "+command, flag.ContinueOnError)
	fs.SetOutput(errOut)
	id := fs.String("job", "", "saved Drive job ID")
	folder := fs.String("source", "/", "OneDrive folder")
	name := fs.String("name", "OneDrive Backup", "new destination root name")
	cache := fs.Int64("cache-gib", 30, "encrypted staging GiB")
	vault := fs.Bool("exclude-personal-vault", false, "explicitly omit Personal Vault")
	workers := fs.String("workers", "auto", "1-8 or auto")
	yes := fs.Bool("yes", false, "confirm upload")
	format := fs.String("format", "json", "report json or csv")
	if fs.Parse(args[1:]) != nil || fs.NArg() != 0 {
		return 2
	}
	if command != "plan" && !migration.ValidID(*id) {
		fmt.Fprintln(errOut, "A valid Drive --job ID is required")
		return 2
	}
	if *cache < 1 || *cache > 1024 {
		return 2
	}
	auto := *workers == "auto"
	count := 3
	if !auto {
		n, e := strconv.Atoi(*workers)
		if e != nil || n < 1 || n > 8 {
			return 2
		}
		count = n
	}
	if *format != "json" && *format != "csv" {
		return 2
	}
	selected := strings.Trim(*folder, "/")
	if selected != "" && (path.Clean(selected) != selected || strings.ContainsAny(selected, "\\\x00") || selected == ".." || strings.HasPrefix(selected, "../")) {
		return 2
	}
	fail := func(e error) int {
		fmt.Fprintln(errOut, e)
		if *id != "" {
			fmt.Fprintln(errOut, "Saved Drive job:", *id)
		}
		return 1
	}
	base := filepath.Join(root, "od2gp", "drive-jobs")
	var m drivemigration.Meta
	var store *journal.Store
	if command != "plan" {
		key, e := journal.Key(*id, false)
		if e != nil {
			return fail(e)
		}
		dir := filepath.Join(base, *id)
		unlock, e := joblock.Acquire(filepath.Join(dir, "job.lock"))
		if e != nil {
			return fail(e)
		}
		defer unlock()
		store, e = journal.Open(filepath.Join(dir, "progress.db"), key, false)
		if e != nil {
			return fail(e)
		}
		defer store.Close()
		if e = store.Load(0, &m); e != nil {
			return fail(e)
		}
		if m.Kind != drivemigration.Kind || m.ID != *id {
			return fail(errors.New("not a Google Drive migration job"))
		}
		if command == "report" {
			if e = driveReport(store, m, *format, out); e != nil {
				return fail(e)
			}
			return 0
		}
		if command == "status" {

			if e = driveSummary(store, m, out); e != nil {
				return fail(e)
			}
			return 0
		}
	}
	unlockSource, e := lockSource(root)
	if e != nil {
		return fail(e)
	}
	defer unlockSource()
	if sourceBackend(getenv) != "graph" {
		return fail(errors.New("Drive tree migration requires the native Graph source"))
	}
	var source msgraph.Client
	var sourceSession identity.Session
	if command == "plan" {
		source, sourceSession, e = graphSourceClient(ctx, getenv)
	} else {
		p, err := identity.Configure("onedrive", getenv)
		if err != nil {
			return fail(err)
		}
		h, s, err := identity.AuthorizedClient(ctx, p)
		if err != nil {
			return fail(err)
		}
		sourceSession = s
		source = msgraph.Client{HTTP: h, Drive: m.DriveID}
		e = source.Check(ctx)
	}
	if e != nil {
		return fail(e)
	}
	provider, e := identity.Configure("gdrive", getenv)
	if e != nil {
		return fail(e)
	}
	h, destinationSession, e := identity.AuthorizedClient(ctx, provider)
	if e != nil {
		return fail(e)
	}
	destination := googledrive.Client{HTTP: h}
	hash := sha256.Sum256([]byte(destinationSession.Account.Subject))
	unlockDestination, e := joblock.Acquire(filepath.Join(root, "od2gp", "drive-account-"+hex.EncodeToString(hash[:])+".lock"))
	if e != nil {
		return fail(e)
	}
	defer unlockDestination()
	if command == "plan" {
		if *id != "" {
			return fail(errors.New("plan generates its own job ID"))
		}
		parentID, err := destination.RootID(ctx)
		if err != nil {
			return fail(err)
		}
		*id, e = migration.ID()
		if e != nil {
			return fail(e)
		}
		m = drivemigration.Meta{Kind: drivemigration.Kind, RootParentID: parentID, ID: *id, Folder: selected, RootName: *name, DriveID: source.Drive, SourceSubject: sourceSession.Account.Subject, SourceClient: sourceSession.ClientID, DestinationSubject: destinationSession.Account.Subject, DestinationClient: destinationSession.ClientID, CacheBytes: *cache << 30, ExcludeVault: *vault, Created: time.Now().UTC()}
		key, e := journal.Key(*id, true)
		if e != nil {
			return fail(e)
		}
		store, e = journal.Open(filepath.Join(base, *id, "progress.db"), key, true)
		if e != nil {
			return fail(e)
		}
		defer store.Close()
		if e = store.Save(0, "drive-job", m); e != nil {
			return fail(e)
		}
	} else {
		if m.SourceSubject != sourceSession.Account.Subject || m.SourceClient != sourceSession.ClientID || m.DestinationSubject != destinationSession.Account.Subject || m.DestinationClient != destinationSession.ClientID {
			return fail(errors.New("saved Drive job account/client binding changed; refusing transfer"))
		}
		source.Drive = m.DriveID
	}
	source.ExcludePersonalVault = m.ExcludeVault
	if m.ExcludeVault {
		fmt.Fprintln(errOut, vaultExclusionNotice)
	}
	if !m.InventoryComplete {
		if e = drivemigration.Discover(ctx, source, store, &m, errOut); e != nil {
			return fail(e)
		}
	}
	fmt.Fprintf(out, "Source account: %q; destination account: %q\n", accountLabel(sourceSession), accountLabel(destinationSession))
	if e = driveSummary(store, m, out); e != nil {
		return fail(e)
	}
	about, e := destination.About(ctx)
	if e != nil {
		return fail(e)
	}
	nodes, e := drivemigration.Load(store)
	if e != nil {
		return fail(e)
	}
	var remaining int64
	for _, n := range nodes {
		if !n.Source.IsDir && n.State != "completed" && n.State != "skipped" {
			remaining += n.Source.Size
		}
	}
	if about.Storage.Limit > 0 && remaining > about.Storage.Limit-about.Storage.Usage {
		return fail(errors.New("insufficient reported Google Drive capacity"))
	}
	if command == "plan" {
		fmt.Fprintln(out, "Drive job:", m.ID)
		fmt.Fprintln(out, "Plan saved. No destination folders or files created.")
		return 0
	}
	if command != "verify" && !*yes {
		fmt.Fprintf(out, "Copy to Google Drive account %q, new root %q? Type yes: ", accountLabel(destinationSession), m.RootName)
		answer, e := readSetupLine(bufio.NewReader(input))
		if e != nil || answer != "yes" {
			return 1
		}
	}
	opts, _ := loadMenuOptions(root)
	progress, closeProgress := startTransferDisplay(store, errOut, false, opts.Language)
	defer closeProgress()
	engine := drivemigration.Engine{Store: store, Source: &source, Destination: destination, Meta: &m, Directory: filepath.Join(base, *id), Workers: count, Auto: auto, Progress: progress}
	if command != "verify" {
		if e = engine.Run(ctx); e != nil {
			return fail(e)
		}
	}
	m.VerifiedAt = time.Time{}
	m.FinalError = ""
	if e = store.Save(0, "drive-job", m); e != nil {
		return fail(e)
	}
	// Final verification may also outlast a temporary provider outage.
	for attempt := 0; ; attempt++ {
		e = engine.Verify(ctx)
		if e == nil {
			break
		}
		var api *googledrive.Error
		var sourcePause interface{ RetryTime() time.Time }
		var temporarySource interface{ SourceTemporary() bool }
		retryable := errors.As(e, &api) && api.Temporary() || errors.As(e, &sourcePause) || errors.As(e, &temporarySource) && temporarySource.SourceTemporary()
		if !retryable {
			m.FinalError = e.Error()
			if saveErr := store.Save(0, "drive-job", m); saveErr != nil {
				return fail(saveErr)
			}
			return fail(e)
		}
		delay := min(time.Duration(1<<min(attempt, 8))*time.Second, 5*time.Minute)
		if sourcePause != nil && sourcePause.RetryTime().After(time.Now().Add(delay)) {
			delay = time.Until(sourcePause.RetryTime())
		}
		if api != nil && api.Code != 0 {
			delay = max(delay, 30*time.Second)
		}
		if api != nil && api.RetryAt.After(time.Now().Add(delay)) {
			delay = time.Until(api.RetryAt)
		}
		fmt.Fprintf(progress, "Verification waiting %s after %s\n", delay.Round(time.Second), e)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fail(ctx.Err())
		case <-timer.C:
		}
	}
	m.VerifiedAt = time.Now().UTC()
	if e = store.Save(0, "drive-job", m); e != nil {
		return fail(e)
	}
	closeProgress()
	if e = driveSummary(store, m, out); e != nil {
		return fail(e)
	}
	fmt.Fprintln(out, "Drive transfer and final tree verification finished. Review checksum strength and exclusions in the report.")
	return 0
}
func driveSummary(s *journal.Store, m drivemigration.Meta, out io.Writer) error {
	counts, e := s.StateCounts()
	if e != nil {
		return e
	}
	fmt.Fprintf(out, "Drive job: %s\nSource: /%s → Google Drive /%s\nInventory complete: %t; states: %v\n", m.ID, m.Folder, m.RootName, m.InventoryComplete, counts)
	nodes, err := drivemigration.Load(s)
	if err != nil {
		return err
	}
	files, folders, empty := 0, 0, 0
	var total, done, largest int64
	for _, n := range nodes {
		if n.Source.IsDir {
			folders++
			continue
		}
		files++
		if n.State == "skipped" && n.Reason == "skipped_empty_file" {
			empty++
			continue
		}
		total += n.Source.Size
		largest = max(largest, n.Source.Size)
		if n.State == "completed" {
			done += n.Source.Size
		}
	}
	fmt.Fprintf(out, "Files: %d; folders (including destination root): %d; empty files excluded: %d\nEligible: %.3f GiB; completed: %.3f GiB; largest file: %.3f GiB; cache cap: %.1f GiB\n", files, folders, empty, float64(total)/(1<<30), float64(done)/(1<<30), float64(largest)/(1<<30), float64(m.CacheBytes)/(1<<30))
	fmt.Fprintf(out, "Final verification: %s; issue: %s\n", m.VerifiedAt.Format(time.RFC3339), m.FinalError)
	if m.ExcludeVault {
		fmt.Fprintln(out, vaultExclusionNotice)
	}
	return nil
}

type driveReportRow struct {
	Source        string `json:"source"`
	Folder        bool   `json:"folder"`
	Bytes         int64  `json:"bytes"`
	State         string `json:"state"`
	DestinationID string `json:"destination_id"`
	Reason        string `json:"reason,omitempty"`
	Verification  string `json:"verification,omitempty"`
}

func driveReport(s *journal.Store, m drivemigration.Meta, format string, out io.Writer) error {
	nodes, e := drivemigration.Load(s)
	if e != nil {
		return e
	}
	var rows []driveReportRow
	for _, n := range nodes {
		rows = append(rows, driveReportRow{n.Source.Path, n.Source.IsDir, n.Source.Size, n.State, n.TargetID, n.Reason, n.Verification})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Source < rows[j].Source })
	if format == "json" {
		return json.NewEncoder(out).Encode(struct {
			Job   drivemigration.Meta `json:"job"`
			Items []driveReportRow    `json:"items"`
		}{m, rows})
	}
	w := csv.NewWriter(out)
	if e = w.Write([]string{"source", "folder", "bytes", "state", "destination_id", "reason", "verification"}); e != nil {
		return e
	}
	for _, r := range rows {
		source := r.Source
		trimmed := strings.TrimLeft(source, " \t\r\n")
		if strings.HasPrefix(trimmed, "=") || strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "@") {
			source = "'" + source
		}
		if e = w.Write([]string{source, strconv.FormatBool(r.Folder), strconv.FormatInt(r.Bytes, 10), r.State, r.DestinationID, r.Reason, r.Verification}); e != nil {
			return e
		}
	}
	if m.ExcludeVault {
		if e = w.Write([]string{"/Personal Vault", "true", "", "accepted_exclusion", "", vaultExclusionReason, ""}); e != nil {
			return e
		}
	}
	status := "job_unverified"
	if !m.VerifiedAt.IsZero() {
		status = "job_verified"
		if m.ExcludeVault {
			status = "job_verified_with_exclusions"
		}
	}
	if err := w.Write([]string{"", "", "", status, "", m.FinalError, ""}); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}
