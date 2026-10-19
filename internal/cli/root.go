// Package cli implements the local setup commands for od2gp.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"od2gp/internal/identity"
	"od2gp/internal/onedrive"
	"od2gp/internal/settings"
	"os"
	"strings"
)

const Version = "0.1.0-dev"

const help = `od2gp - personal photo and video migration
Direction: OneDrive -> Google Photos. Source files are kept.

Usage:
  od2gp menu
  od2gp help
  od2gp version
  od2gp setup
  od2gp setup microsoft [CLIENT_ID]
  od2gp doctor
  od2gp privacy
  od2gp plan --source /Pictures --cache-gib 30
  od2gp run --job JOB_ID [--wait] [--retries N] [--workers 1-8] [--batch-size 1-50]
  od2gp resume --job JOB_ID [--wait] [--retries N] [--workers 1-8] [--batch-size 1-50]
  od2gp status --job JOB_ID
  od2gp report --job JOB_ID --format csv
  od2gp source check
  od2gp source select-drive
  od2gp source scan [--diagnostics] [folder]
  od2gp auth <login|status|check|logout> <onedrive|google>

Doctor checks local OAuth client configuration only; it does not sign in.
Run od2gp setup once to configure Google; OneDrive uses native Graph browser login.
Set OD2GP_SOURCE_BACKEND=rclone for the legacy source. Existing jobs keep their backend.
See docs/setup.md for account registration instructions.
Transfers are checkpointed locally. Run plan before uploading.
`

// Run returns a process exit code: 0 for success, 2 for usage or setup errors.
// Auth commands may contact the selected provider; credentials are never printed.
func Run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	return RunContext(context.Background(), args, strings.NewReader(""), stdout, stderr, getenv)
}

func RunContext(ctx context.Context, args []string, input io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 1 && args[0] == "internal-rclone-password" {
		secret, err := onedrive.Password()
		if err != nil {
			return 1
		}
		fmt.Fprintln(stdout, secret)
		return 0
	}
	if len(args) > 0 && args[0] == "setup" {
		if (len(args) == 2 || len(args) == 3) && args[1] == "microsoft" {
			id := ""
			if len(args) == 3 {
				id = args[2]
			}
			return RunMicrosoftSetup(id, stdout, stderr, getenv)
		}

		if len(args) != 1 {
			fmt.Fprintln(stderr, "Usage: od2gp setup")
			return 2
		}
		return RunSetup(input, stdout, stderr, getenv, identity.OpenSetupURL)
	}
	root := strings.TrimSpace(getenv("OD2GP_CONFIG_DIR"))
	if root == "" {
		var err error
		root, err = settings.Directory()
		if err != nil {
			fmt.Fprintln(stderr, "Cannot locate your user config directory.")
			return 1
		}
	}
	if len(args) > 0 && args[0] == "menu" {
		if len(args) != 1 {
			fmt.Fprintln(stderr, "Usage: od2gp menu")
			return 2
		}
		return interactiveMenu(ctx, input, stdout, stderr, getenv, root)
	}
	if len(args) == 0 && interactiveConsole(input, stdout) {
		return interactiveMenu(ctx, input, stdout, stderr, getenv, root)
	}
	configEnv, configErr := settings.Env(root, getenv)
	if configErr != nil {
		fmt.Fprintln(stderr, configErr)
		return 1
	}
	if len(args) > 0 {
		switch args[0] {
		case "plan", "run", "resume", "status", "report":
			return migrationCommand(ctx, args[0], args[1:], input, stdout, stderr, configEnv, root)
		}
	}
	if len(args) == 3 && args[0] == "auth" && args[2] == "onedrive" {
		if sourceBackend(configEnv) == "rclone" {
			return sourceAuth(ctx, args[1], input, stdout, stderr, configEnv, root)
		}
		if err := validateBackend(sourceBackend(configEnv)); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		unlock, err := lockSource(root)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		defer unlock()
		return authCommand(ctx, args[1:], input, stdout, stderr, configEnv)
	}
	if len(args) > 0 && args[0] == "source" {
		if err := validateBackend(sourceBackend(configEnv)); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		if sourceBackend(configEnv) == "graph" {
			return graphSourceCommand(ctx, args[1:], input, stdout, stderr, configEnv, root)
		}
		return sourceCommand(ctx, args[1:], input, stdout, stderr, configEnv, root)
	}
	if len(args) > 0 && args[0] == "auth" {
		return authCommand(ctx, args[1:], input, stdout, stderr, configEnv)
	}
	if len(args) == 0 {
		fmt.Fprint(stdout, help)
		return listMigrationJobs(root, stdout, stderr)
	}
	if len(args) != 1 {
		fmt.Fprintln(stderr, "Expected one command; use od2gp help.")
		return 2
	}
	switch args[0] {
	case "help", "--help", "-h":
		fmt.Fprint(stdout, help)
		return 0
	case "version", "--version":
		fmt.Fprintln(stdout, "od2gp", Version)
		return 0
	case "privacy":
		fmt.Fprint(stdout, privacyNotice)
		return 0
	case "doctor":
		return doctor(stdout, stderr, configEnv)
	default:
		fmt.Fprintln(stderr, "Unknown command; use od2gp help for implemented commands.")
		return 2
	}
}

func doctor(stdout, stderr io.Writer, getenv func(string) string) int {
	valid := true
	path := strings.TrimSpace(getenv("OD2GP_GOOGLE_CLIENT_FILE"))
	if path == "" {
		fmt.Fprintln(stderr, "Run od2gp setup, or set OD2GP_GOOGLE_CLIENT_FILE to your Google Desktop OAuth JSON file.")
		valid = false
	} else if err := checkGoogleClient(path); err != nil {
		fmt.Fprintln(stderr, err)
		valid = false
	}
	if !valid {
		fmt.Fprintln(stderr, "See docs/setup.md. No sign-in or transfer was attempted.")
		return 2
	}
	fmt.Fprintln(stdout, "Google OAuth client configuration is structurally valid; sign-in is checked separately.")
	fmt.Fprintf(stdout, "OneDrive source backend for new jobs: %s\n", sourceBackend(getenv))
	if sourceBackend(getenv) == "graph" {
		if p, err := identity.Configure("onedrive", getenv); err == nil {
			fmt.Fprintf(stdout, "Microsoft public client ID: %s\n", p.OAuth.ClientID)
		}
	}

	fmt.Fprintln(stdout, "Run auth login onedrive and auth login google, then auth check for each provider.")
	return 0
}

func checkGoogleClient(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("Cannot open the Google Desktop client file; rerun od2gp setup and check the path.")
	}
	defer file.Close()
	const maxBytes = 64 * 1024
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		return fmt.Errorf("Cannot read Google client JSON; file must be at most 64 KiB.")
	}
	var config struct {
		Installed *struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		} `json:"installed"`
	}
	if json.Unmarshal(data, &config) != nil || config.Installed == nil {
		return fmt.Errorf("Google client JSON must contain an installed Desktop app client, not a Web client.")
	}
	if strings.TrimSpace(config.Installed.ClientID) == "" || strings.TrimSpace(config.Installed.ClientSecret) == "" {
		return fmt.Errorf("Google Desktop app JSON is missing client_id or client_secret; download it again from Google Cloud.")
	}
	return nil
}
