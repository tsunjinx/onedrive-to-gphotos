package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"od2gp/internal/onedrive"
)

func sourceSelectDrive(ctx context.Context, input io.Reader, out, errOut io.Writer, getenv func(string) string, root string) int {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	unlock, err := lockSource(root)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer unlock()
	client, err := sourceClient(ctx, getenv, root, errOut)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	// A fresh login has no drive_id yet; treat that as no current selection.
	current, _ := client.DriveID(ctx)
	fmt.Fprintln(out, "Choose a drive from the connected Microsoft account. No files are transferred.\nSaved jobs keep their original drive binding; changing drives does not move their progress.")
	reader := bufio.NewReader(input)
	retryMsg := ""
	_, err = client.SelectDrive(ctx,
		func(q onedrive.Question) (string, error) {
			msg := retryMsg
			retryMsg = ""
			return selectDriveQuestion(reader, out, current, q, msg)
		},
		func(candidate onedrive.Client) (bool, error) {
			accepted, err := reviewDriveContents(ctx, reader, out, candidate)
			if !accepted && err == nil {
				retryMsg = "Drive not confirmed. Choose a different drive, or 0 to cancel."
			}
			return accepted, err
		},
		func(message string) {
			fmt.Fprintln(out, message)
		},
	)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(errOut, "Drive selection cancelled. The previous drive is kept.")
		} else {
			fmt.Fprintln(errOut, "Drive selection was not saved. The previous drive is kept.")
			fmt.Fprintln(errOut, err)
		}
		return 1
	}
	clearPromptScreen(out)
	fmt.Fprintln(out, "Drive selected and checked. New plans will use this drive.\nSaved jobs remain bound to their original drives.")
	return 0
}

func selectDriveQuestion(reader *bufio.Reader, out io.Writer, current string, q onedrive.Question, message string) (string, error) {
	clearPromptScreen(out)
	if message != "" {
		fmt.Fprintln(out, message)
	}
	if q.Error != "" {
		fmt.Fprintln(out, "That drive could not be used. Choose another, or cancel.")
		return "", errors.New("rclone rejected the selected OneDrive drive")
	}
	q = sourceQuestionDefaults(q)
	if q.Option.Name == "config_driveid" {
		for _, example := range q.Option.Examples {
			if example.Value == current {
				q.Option.Default = current
				break
			}
		}
	}
	fmt.Fprintln(out, q.Option.Help)
	defaultChoice := ""
	prompt := "Use this drive? yes/no"
	if q.Option.Name == "config_driveid" {
		prompt = "Choose a drive number"
		for i, example := range q.Option.Examples {
			marker := ""
			if example.Value == current {
				marker = " — current"
			}
			fmt.Fprintf(out, "%d) %q%s\n", i+1, example.Help, marker)
			if q.Option.Default != nil && example.Value == fmt.Sprint(q.Option.Default) && (defaultChoice == "" || strings.EqualFold(strings.TrimSpace(example.Help), "OneDrive (personal)")) {
				defaultChoice = fmt.Sprint(i + 1)
			}
		}
	} else if q.Option.Default != nil {
		defaultChoice = fmt.Sprint(q.Option.Default)
	}
	fmt.Fprintln(out, "0) Cancel and keep the previous drive")
	for {
		fmt.Fprintf(out, "%s [%s]: ", prompt, defaultChoice)
		answer, err := readSetupLine(reader)
		if err != nil {
			return "", err
		}
		if answer == "0" {
			return "", context.Canceled
		}
		if answer == "" && defaultChoice != "" {
			answer = defaultChoice
		}
		value, err := normalizeSourceAnswer(q, answer)
		if err == nil {
			if q.Option.Name == "config_driveid" {
				fmt.Fprintln(out, "Checking access to that drive (up to 30 seconds)...")
			}
			return value, nil
		}
		fmt.Fprintln(out, err)
	}
}
