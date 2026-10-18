package onedrive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// SelectDrive reuses the existing sign-in and publishes only a checked encrypted
// configuration. Review sees the temporary candidate; false selects another
// drive and an error aborts. The caller holds the source lock throughout.
func (c Client) SelectDrive(ctx context.Context, ask func(Question) (string, error), review func(Client) (bool, error), status ...func(string)) (string, error) {
	report := func(message string) {
		if len(status) > 0 && status[0] != nil {
			status[0](message)
		}
	}
	// Read the token from config without requiring a drive_id; a fresh login
	// has a valid token but no selected drive yet.
	raw, err := c.rawSettings(ctx)
	if err != nil {
		return "", err
	}
	var token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	if json.Unmarshal([]byte(raw["token"]), &token) != nil || (token.Access == "" && token.Refresh == "") {
		return "", errors.New("connect OneDrive before choosing a drive; no stored sign-in is available")
	}
	var selected string
	err = editEncryptedSourceConfig(c.Config, func(file string) error {
		staged := c
		staged.Config = file
		// Each attempt must start from the same pre-selection config. Rclone
		// updates the temporary config while it walks its interactive states;
		// reusing that candidate can skip or retain the prior drive choice.
		original, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		for {
			if err := os.WriteFile(file, original, 0600); err != nil {
				return err
			}
			id, err := configureDriveSelection(ctx, staged.output, ask)
			if err != nil {
				return err
			}
			actual, err := staged.DriveID(ctx)
			if err != nil {
				return err
			}
			if actual != id {
				return errors.New("selected source drive does not match the verified configuration")
			}
			report("Checking the selected OneDrive drive...")
			checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err = staged.Check(checkCtx)
			cancel()
			if err != nil {
				return err
			}
			report("Drive check passed. Loading a small contents preview...")
			if review != nil {
				accepted, err := review(staged)
				if err != nil {
					return err
				}
				if !accepted {
					continue
				}
			}
			report("Saving the selected drive...")
			selected = actual
			return ctx.Err()
		}
	})
	return selected, err
}

func configureDriveSelection(ctx context.Context, output func(context.Context, ...string) ([]byte, error), ask func(Question) (string, error)) (string, error) {
	selected := ""
	// Bound each provider request, not the user's time answering a question.
	boundedOutput := func(ctx context.Context, args ...string) ([]byte, error) {
		request, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		data, err := output(request, args...)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if request.Err() != nil {
			return nil, fmt.Errorf("OneDrive drive selection timed out; check connectivity and retry: %w", request.Err())
		}
		return data, err
	}
	err := configureRemote(ctx, true, boundedOutput, func(q Question) (string, error) {
		switch q.Option.Name {
		case "config_refresh_token":
			return "false", nil
		case "config_type":
			return "onedrive", nil
		case "config_driveid":
			answer, err := ask(q)
			if err != nil {
				return "", err
			}
			for _, example := range q.Option.Examples {
				if example.Value == answer {
					selected = answer
					return answer, nil
				}
			}
			return "", errors.New("choose one of the listed OneDrive drives")
		case "config_drive_ok":
			// This only accepts the candidate inside the temporary config. The
			// content review decides whether to publish it to the active config.
			return "true", nil
		default:
			return "", errors.New("drive selection needs a working OneDrive sign-in; reconnect OneDrive first")
		}
	}, "config_refresh_token", "false", "config_is_local", "false")
	if err != nil {
		return "", err
	}
	if selected == "" {
		return "", errors.New("no OneDrive drive was selected")
	}
	return selected, nil
}

func editEncryptedSourceConfig(path string, change func(string) error) error {
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(source, (2<<20)+1))
	source.Close()
	if readErr != nil {
		return readErr
	}
	if len(data) > 2<<20 || !encryptedHeader(data) {
		return errors.New("source config is not a supported encrypted file")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".drive-*.conf")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := change(temporary); err != nil {
		return err
	}
	file, err = os.OpenFile(temporary, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	var header [256]byte
	n, readErr := file.Read(header[:])
	if readErr != nil && readErr != io.EOF {
		file.Close()
		return readErr
	}
	if !encryptedHeader(header[:n]) {
		file.Close()
		return errors.New("updated source config is not encrypted; keeping the previous drive")
	}
	permissionErr := file.Chmod(0600)
	syncErr := file.Sync()
	closeErr = file.Close()
	if permissionErr != nil {
		return permissionErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temporary, path)
}
