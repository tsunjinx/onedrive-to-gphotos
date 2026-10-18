package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvOverridesSavedConfigAndSaveIsPrivate(t *testing.T) {
	root := t.TempDir()
	s := Settings{MicrosoftClientID: "saved", GoogleClientFile: "client.json"}
	if err := Save(root, s); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil || loaded != s {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	file := filepath.Join(root, "od2gp", "config.json")
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatalf("file mode too broad: %o", info.Mode().Perm())
	}
	env, err := Env(root, func(k string) string {
		if k == "OD2GP_MICROSOFT_CLIENT_ID" {
			return "override"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if env("OD2GP_MICROSOFT_CLIENT_ID") != "override" || env("OD2GP_GOOGLE_CLIENT_FILE") != "client.json" {
		t.Fatal("environment did not override saved configuration")
	}
}
func TestLoadRejectsCorruptConfig(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "od2gp")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil {
		t.Fatal("corrupt config accepted")
	}
}

func TestNativeProjectDefaultsDoNotRequireWritingGlobalSettings(t *testing.T) {
	env, err := Env(t.TempDir(), func(string) string { return "" })
	if err != nil || env("OD2GP_SOURCE_BACKEND") != "graph" || env("OD2GP_MICROSOFT_CLIENT_ID") != DefaultMicrosoftClientID {
		t.Fatalf("wrong native defaults: %v", err)
	}
	env, err = Env(t.TempDir(), func(k string) string {
		if k == "OD2GP_SOURCE_BACKEND" {
			return "rclone"
		}
		return ""
	})
	if err != nil || env("OD2GP_SOURCE_BACKEND") != "rclone" {
		t.Fatal("legacy override ignored")
	}
}
