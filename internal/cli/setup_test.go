package cli

import (
	"bytes"
	"encoding/json"
	"od2gp/internal/settings"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupGuidesValidatesAndSavesReusableSettings(t *testing.T) {
	root := t.TempDir()
	clientFile := filepath.Join(root, "Google client.json")
	document := `{"installed":{"client_id":"desktop-id","client_secret":"desktop-secret"}}`
	if err := os.WriteFile(clientFile, []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(root, "user config")
	getenv := func(key string) string {
		if key == "OD2GP_CONFIG_DIR" {
			return configDir
		}
		return ""
	}
	input := strings.NewReader(clientFile + "\n")
	var out, errOut bytes.Buffer
	opened := 0
	code := RunSetup(input, &out, &errOut, getenv, func(string) error { opened++; return nil })
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut.String())
	}
	if opened != 1 {
		t.Fatalf("opened %d setup consoles", opened)
	}
	data, err := os.ReadFile(filepath.Join(configDir, "od2gp", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "desktop-secret") {
		t.Fatal("copied OAuth secret into app config")
	}
	var saved map[string]string
	if json.Unmarshal(data, &saved) != nil || saved["microsoft_client_id"] != "" || saved["google_client_file"] != clientFile {
		t.Fatalf("bad config: %s", data)
	}
	info, err := os.Stat(filepath.Join(configDir, "od2gp"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatalf("config directory permissions are too broad: %o", info.Mode().Perm())
	}
}

func TestSetupRejectsInvalidInputAndKeepsExistingSettings(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	appDir := filepath.Join(configDir, "od2gp")
	if err := os.MkdirAll(appDir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(appDir, "config.json")
	original := []byte(`{"microsoft_client_id":"old","google_client_file":"old.json"}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	getenv := func(key string) string {
		if key == "OD2GP_CONFIG_DIR" {
			return configDir
		}
		return ""
	}
	input := strings.NewReader("bad-id\nmissing.json\n")
	var out, errOut bytes.Buffer
	code := RunSetup(input, &out, &errOut, getenv, func(string) error { return nil })
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if code == 0 || !bytes.Equal(original, saved) {
		t.Fatalf("code=%d existing settings overwritten: %s", code, saved)
	}
}

func TestMicrosoftSetupPreservesGoogleAndLeavesJobsUntouched(t *testing.T) {
	root := t.TempDir()
	if err := settings.Save(root, settings.Settings{MicrosoftClientID: "old", GoogleClientFile: "google.json"}); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := RunMicrosoftSetup("dd4ceec5-81a2-4c43-b4f7-5680436926aa", &out, &errOut, func(k string) string {
		if k == "OD2GP_CONFIG_DIR" {
			return root
		}
		return ""
	})
	saved, err := settings.Load(root)
	if code != 0 || err != nil || saved.SourceBackend != "graph" || saved.GoogleClientFile != "google.json" || saved.MicrosoftClientID != "dd4ceec5-81a2-4c43-b4f7-5680436926aa" {
		t.Fatalf("setup failed: %+v %v %s", saved, err, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(root, "od2gp", "jobs")); !os.IsNotExist(err) {
		t.Fatal("setup touched migration jobs")
	}
}
