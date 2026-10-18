package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"od2gp/internal/joblock"
	"os"
	"path/filepath"
	"strings"
	"time"

	"od2gp/internal/onedrive"
)

func sourceClient(ctx context.Context, getenv func(string) string, root string, stdout io.Writer) (onedrive.Client, error) {
	tools := getenv("OD2GP_TOOL_DIR")
	if tools == "" {
		tools = filepath.Join(root, "od2gp", "tools")
	}
	fmt.Fprintln(stdout, "Checking rclone...")
	binary, err := onedrive.Ensure(ctx, tools)
	if err != nil {
		return onedrive.Client{}, err
	}
	return onedrive.New(binary, filepath.Join(root, "od2gp", "source", "rclone.conf"))
}
func sourceAuth(ctx context.Context, action string, input io.Reader, stdout, stderr io.Writer, getenv func(string) string, root string) int {
	if action != "login" && action != "check" && action != "status" && action != "logout" {
		fmt.Fprintln(stderr, "Use auth <login|check|status|logout> onedrive")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	reader := bufio.NewReader(input)
	if action == "login" {
		fmt.Fprintln(stdout, "Connect OneDrive through rclone with read-only file access. Its encrypted config and refresh credentials stay on this computer; its encryption password is stored in the OS credential store. No files are transferred by signing in. See docs/privacy.md.")
		fmt.Fprint(stdout, "Continue? Type yes: ")
		line, err := readSetupLine(reader)
		if err != nil || line != "yes" {
			fmt.Fprintln(stderr, "Sign-in cancelled.")
			return 1
		}
		clearPromptScreen(stdout)
	}
	unlock, err := lockSource(root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer unlock()
	client, err := sourceClient(ctx, getenv, root, stdout)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	switch action {
	case "login":
		err = client.Configure(ctx, func(q onedrive.Question) (string, error) {
			clearPromptScreen(stdout)
			q = sourceQuestionDefaults(q)
			if q.Error != "" {
				fmt.Fprintln(stdout, "The previous configuration answer was rejected; try again.")
			}
			fmt.Fprintln(stdout, q.Option.Help)
			for i, example := range q.Option.Examples {
				fmt.Fprintf(stdout, "  %d) %s\n", i+1, example.Help)
			}
			defaultValue := ""
			if q.Option.Default != nil {
				defaultValue = fmt.Sprint(q.Option.Default)
			}
			promptName := q.Option.Name
			if q.Option.Name == "config_driveid" {
				promptName = "Choose a drive number"
				for _, example := range q.Option.Examples {
					if example.Value == defaultValue && strings.EqualFold(example.Help, "OneDrive (personal)") {
						defaultValue = example.Help
						break
					}
				}
			}
			for {
				fmt.Fprintf(stdout, "%s [%s]: ", promptName, defaultValue)
				answer, err := readSetupLine(reader)
				if err != nil {
					return "", errors.New("source setup cancelled")
				}
				normalized, err := normalizeSourceAnswer(q, answer)
				if err == nil {
					clearPromptScreen(stdout)
					fmt.Fprintln(stdout, "Continuing OneDrive sign-in... Complete any browser prompt that opens.")
					return normalized, nil
				}
				fmt.Fprintln(stdout, err)
			}
		})
		if err == nil {
			err = client.Check(ctx)
		}
		if err == nil {
			clearPromptScreen(stdout)
			fmt.Fprintln(stdout, "OneDrive connected through rclone. No Azure app registration needed.")
		}
	case "check", "status":
		err = client.Check(ctx)
		if err == nil {
			fmt.Fprintln(stdout, "OneDrive remote is accessible (online check).")
		}
	case "logout":
		err = client.Logout(ctx)
		if err == nil {
			fmt.Fprintln(stdout, "OneDrive remote removed locally. Revoke rclone's grant at https://account.live.com/consent/Manage if desired.")
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
func sourceCommand(ctx context.Context, args []string, input io.Reader, stdout, stderr io.Writer, getenv func(string) string, root string) int {
	if len(args) == 1 && args[0] == "select-drive" {
		return sourceSelectDrive(ctx, input, stdout, stderr, getenv, root)
	}
	if len(args) == 1 && args[0] == "check" {
		return sourceAuth(ctx, "check", input, stdout, stderr, getenv, root)
	}
	if len(args) < 1 || args[0] != "scan" {
		fmt.Fprintln(stderr, "Usage: od2gp source check | source select-drive | source scan [--diagnostics] [folder]")
		return 2
	}
	flags := flag.NewFlagSet("source scan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	excludeVault := flags.Bool("exclude-personal-vault", false, "explicitly exclude inaccessible /Personal Vault from the scan")
	diagnostics := flags.Bool("diagnostics", false, "show bounded source failure details in the terminal (may include filenames)")
	if flags.Parse(args[1:]) != nil || flags.NArg() > 1 {
		return 2
	}
	unlock, err := lockSource(root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer unlock()
	client, err := sourceClient(ctx, getenv, root, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *diagnostics {
		fmt.Fprintln(stderr, "Read-only diagnostic scan. No uploads or saved-job changes. Failure details may include filenames; review before sharing.")
		client.Diagnostics = stderr
	}
	folder := ""
	if flags.NArg() == 1 {
		folder = strings.TrimPrefix(flags.Arg(0), "onedrive:")
	}
	client.ExcludePersonalVault = *excludeVault
	if *excludeVault {
		fmt.Fprintln(stderr, vaultExclusionNotice)
	}
	var count, bytes int64
	err = client.Walk(ctx, folder, func(item onedrive.Item) error {
		if item.Size < 0 {
			return nil
		}
		count++
		bytes += item.Size
		return nil
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "Source inventory: %d files, %d bytes. This includes all file types; no upload was performed.\n", count, bytes)
	return 0
}

func lockSource(root string) (func(), error) {
	dir := filepath.Join(root, "od2gp", "source")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return joblock.Acquire(filepath.Join(dir, "source.lock"))
}

func normalizeSourceAnswer(q onedrive.Question, answer string) (string, error) {
	answer = strings.TrimSpace(answer)
	if answer == "" && q.Option.Default != nil {
		answer = fmt.Sprint(q.Option.Default)
	}
	if q.Option.Type == "bool" || q.Option.Name == "config_is_local" || q.Option.Name == "config_refresh_token" {
		switch strings.ToLower(answer) {
		case "true", "yes", "y":
			return "true", nil
		case "false", "no", "n":
			return "false", nil
		default:
			return "", errors.New("Please enter yes or no, or press Enter to use the default.")
		}
	}
	if q.Option.Exclusive || q.Option.Name == "config_driveid" {
		for i, example := range q.Option.Examples {
			if answer == example.Value || answer == fmt.Sprint(i+1) {
				return example.Value, nil
			}
		}
		return "", errors.New("Choose one of the displayed values.")
	}
	return answer, nil
}

func sourceQuestionDefaults(q onedrive.Question) onedrive.Question {
	if q.Option.Name == "config_driveid" {
		// Do not default to hidden metadata/archive drives returned by /me/drives.
		q.Option.Default = nil
		for _, example := range q.Option.Examples {
			if strings.EqualFold(strings.TrimSpace(example.Help), "OneDrive (personal)") {
				q.Option.Default = example.Value
				break
			}
		}
	}
	return q
}
