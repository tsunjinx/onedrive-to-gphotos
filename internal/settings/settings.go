// Package settings stores non-secret OAuth app setup for this user's CLI.
package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const DefaultMicrosoftClientID = "dd4ceec5-81a2-4c43-b4f7-5680436926aa"

type Settings struct {
	SourceBackend     string `json:"source_backend,omitempty"`
	MicrosoftClientID string `json:"microsoft_client_id"`
	GoogleClientFile  string `json:"google_client_file"`
}

func Directory() (string, error) { return os.UserConfigDir() }
func Load(root string) (Settings, error) {
	var value Settings
	path := filepath.Join(root, "od2gp", "config.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return value, nil
	}
	if err != nil {
		return value, errors.New("cannot read od2gp settings; check local config permissions")
	}
	if len(data) > 16*1024 || json.Unmarshal(data, &value) != nil {
		return value, errors.New("od2gp settings are invalid; rerun setup to replace them")
	}
	value.MicrosoftClientID = strings.TrimSpace(value.MicrosoftClientID)
	value.GoogleClientFile = strings.TrimSpace(value.GoogleClientFile)
	return value, nil
}
func Save(root string, value Settings) error {
	dir := filepath.Join(root, "od2gp")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("cannot create private od2gp config directory")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return errors.New("cannot secure od2gp config directory")
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.New("cannot encode od2gp settings")
	}
	data = append(data, '\n')
	file, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return errors.New("cannot write od2gp settings")
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.New("cannot write od2gp settings")
	}
	if err = os.Rename(temp, filepath.Join(dir, "config.json")); err != nil {
		return errors.New("cannot save od2gp settings")
	}
	return nil
}

func Env(root string, getenv func(string) string) (func(string) string, error) {
	saved, err := Load(root)
	if err != nil {
		return nil, err
	}
	return func(key string) string {
		if value := strings.TrimSpace(getenv(key)); value != "" {
			return value
		}
		switch key {
		case "OD2GP_MICROSOFT_CLIENT_ID":
			if saved.MicrosoftClientID != "" {
				return saved.MicrosoftClientID
			}
			return DefaultMicrosoftClientID
		case "OD2GP_SOURCE_BACKEND":
			if saved.SourceBackend != "" {
				return saved.SourceBackend
			}
			return "graph"
		case "OD2GP_GOOGLE_CLIENT_FILE":
			return saved.GoogleClientFile
		default:
			return ""
		}
	}, nil
}
