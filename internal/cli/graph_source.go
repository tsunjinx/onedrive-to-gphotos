package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"od2gp/internal/identity"
	"od2gp/internal/msgraph"
	"od2gp/internal/onedrive"
)

func sourceBackend(getenv func(string) string) string {
	backend := strings.TrimSpace(getenv("OD2GP_SOURCE_BACKEND"))
	if backend == "" {
		return "graph"
	}
	return backend
}
func graphSourceClient(ctx context.Context, getenv func(string) string) (msgraph.Client, identity.Session, error) {
	p, err := identity.Configure("onedrive", getenv)
	if err != nil {
		return msgraph.Client{}, identity.Session{}, err
	}
	httpClient, session, err := identity.AuthorizedClient(ctx, p)
	if err != nil {
		return msgraph.Client{}, session, err
	}
	client := msgraph.Client{HTTP: httpClient, Drive: session.DriveID}
	if client.Drive == "" {
		drive, err := client.DefaultDrive(ctx)
		if err != nil {
			return client, session, err
		}
		// Never guess a metadata drive when the default drive is unavailable.
		client.Drive = drive.ID
	}
	if err := client.Check(ctx); err != nil {
		return client, session, err
	}
	return client, session, nil
}
func graphSelectDrive(ctx context.Context, input io.Reader, out, errOut io.Writer, getenv func(string) string, root string) int {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	unlock, err := lockSource(root)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer unlock()
	// A failed previous selection must not prevent listing other drives.
	p, err := identity.Configure("onedrive", getenv)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	httpClient, session, err := identity.AuthorizedClient(ctx, p)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	client := msgraph.Client{HTTP: httpClient}
	request, cancelRequest := context.WithTimeout(ctx, 30*time.Second)
	drives, err := client.Drives(request)
	cancelRequest()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	reader := bufio.NewReader(input)
	for {
		var q onedrive.Question
		q.Option.Name = "config_driveid"
		q.Option.Help = "Select a OneDrive drive (native Microsoft Graph). 0 keeps the previous choice."
		for _, drive := range drives {
			q.Option.Examples = append(q.Option.Examples, struct{ Value, Help string }{drive.ID, fmt.Sprintf("%s (%s)", drive.Name, drive.Type)})
		}
		choice, err := selectDriveQuestion(reader, out, session.DriveID, q, "")
		if err != nil {
			fmt.Fprintln(errOut, "Drive selection cancelled or stopped; previous choice kept.")
			return 1
		}
		candidate := client
		candidate.Drive = choice
		request, cancelRequest := context.WithTimeout(ctx, 30*time.Second)
		err = candidate.Check(request)
		cancelRequest()
		if err != nil {
			fmt.Fprintln(errOut, err)
			fmt.Fprintln(errOut, "The previous drive choice is kept.")
			return 1
		}
		accepted, err := reviewDriveContents(ctx, reader, out, candidate)
		if err != nil {
			fmt.Fprintln(errOut, "Drive selection cancelled; previous choice kept.")
			return 1
		}
		if !accepted {
			continue
		}
		// Refresh may have rotated the token during inspection. Preserve the latest
		// stored credential rather than replacing it with the initial session copy.
		latest, err := identity.Load("onedrive")
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		if latest.Account.Subject != session.Account.Subject || latest.ClientID != session.ClientID {
			fmt.Fprintln(errOut, "Microsoft account changed during selection; choice was not saved.")
			return 1
		}
		latest.DriveID = choice
		if err := identity.Save("onedrive", latest); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		fmt.Fprintln(out, "Native Graph drive selected. No media transferred. Existing jobs keep their original backend and drive.")
		return 0
	}
}
func graphSourceCommand(ctx context.Context, args []string, input io.Reader, out, errOut io.Writer, getenv func(string) string, root string) int {
	if len(args) == 1 && args[0] == "select-drive" {
		return graphSelectDrive(ctx, input, out, errOut, getenv, root)
	}
	if len(args) == 0 || (args[0] != "scan" && args[0] != "check") {
		fmt.Fprintln(errOut, "Usage: source check | source select-drive | source scan [--exclude-personal-vault] [folder]")
		return 2
	}
	flags := flag.NewFlagSet("source", flag.ContinueOnError)
	flags.SetOutput(errOut)
	exclude := flags.Bool("exclude-personal-vault", false, "explicitly omit Personal Vault")
	flags.Bool("diagnostics", false, "Graph errors are sanitized by default")
	if flags.Parse(args[1:]) != nil || flags.NArg() > 1 {
		return 2
	}
	unlock, err := lockSource(root)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer unlock()
	client, session, err := graphSourceClient(ctx, getenv)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if args[0] == "check" {
		fmt.Fprintf(out, "Native Microsoft Graph account %q and selected drive checked.\n", accountLabel(session))
		return 0
	}
	folder := ""
	if flags.NArg() == 1 {
		folder = strings.Trim(strings.TrimPrefix(flags.Arg(0), "onedrive:"), "/")
	}
	client.ExcludePersonalVault = *exclude
	if *exclude {
		fmt.Fprintln(out, vaultExclusionNotice)
	}
	count := 0
	var bytes int64
	err = client.Walk(ctx, folder, func(item onedrive.Item) error {
		count++
		bytes += max(item.Size, 0)
		if count%100 == 0 {
			fmt.Fprintf(errOut, "Scanned %d source files...\n", count)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "Source inventory (native Graph): %d files, %d bytes. No media transferred.\n", count, bytes)
	return 0
}

func validateBackend(backend string) error {
	if backend != "graph" && backend != "rclone" {
		return errors.New("source backend must be graph or rclone")
	}
	return nil
}
