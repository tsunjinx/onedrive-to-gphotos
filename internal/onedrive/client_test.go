package onedrive

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestCommandStripsRcloneOverridesAndNeverPassesPassword(t *testing.T) {
	t.Setenv("RCLONE_CONFIG_PASS", "sensitive")
	t.Setenv("RCLONE_ONEDRIVE_ACCESS_SCOPES", "Files.ReadWrite")
	c := Client{Binary: "rclone", Config: "private/config", Helper: "/path with spaces/od2gp"}
	cmd := c.command(context.Background(), "lsjson", "onedrive:")
	joined := strings.Join(cmd.Args, " ")
	if strings.Contains(joined, "sensitive") {
		t.Fatal("secret in arguments")
	}
	for _, value := range cmd.Env {
		if strings.HasPrefix(value, "RCLONE_") {
			t.Fatal("inherited rclone setting can override secure source")
		}
	}
	if !strings.Contains(joined, "internal-rclone-password") {
		t.Fatal("missing protected password helper")
	}
}
func TestPrepareRejectsPlaintextConfig(t *testing.T) {
	keyring.MockInit()
	path := filepath.Join(t.TempDir(), "rclone.conf")
	original := []byte("[onedrive]\ntoken=secret\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	c := Client{Config: path}
	if c.Prepare(context.Background()) == nil {
		t.Fatal("plaintext config accepted")
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) {
		t.Fatal("config altered")
	}
}
func TestCappedOutput(t *testing.T) {
	b := &cappedBuffer{limit: 3}
	if _, err := b.Write([]byte("1234")); err == nil {
		t.Fatal("unbounded output accepted")
	}
}
func TestInventoryItemRetainsSourceIdentity(t *testing.T) {
	var item Item
	if err := json.Unmarshal([]byte(`{"Path":"photo.jpg","ID":"id","Size":12,"ModTime":"time","Hashes":{"QuickXorHash":"hash"}}`), &item); err != nil {
		t.Fatal(err)
	}
	if item.ID != "id" || item.ModTime != "time" || item.Hashes["QuickXorHash"] != "hash" {
		t.Fatal("source version metadata lost")
	}
}

func TestEncryptionHeaderAllowsOfficialCommentsOnly(t *testing.T) {
	for _, tc := range []struct {
		content string
		want    bool
	}{
		{"# Encrypted rclone configuration File\n\nRCLONE_ENCRYPT_V0:\nbody", true},
		{"[onedrive]\ntoken=plaintext\nRCLONE_ENCRYPT_V0:", false},
		{"RCLONE_ENCRYPT_V1:\nbody", false},
	} {
		if got := encryptedHeader([]byte(tc.content)); got != tc.want {
			t.Fatalf("header recognized=%v want %v", got, tc.want)
		}
	}
}

func TestIncompleteRemoteRestartsConfigurationWithoutDeleting(t *testing.T) {
	calls := 0
	output := func(_ context.Context, args ...string) ([]byte, error) {
		calls++
		if calls == 1 {
			if strings.Join(args[:3], " ") != "config update onedrive" {
				t.Fatalf("did not restart existing remote: %v", args)
			}
			return []byte(`{"State":"refresh","Option":{"Name":"config_refresh_token","Type":"bool","Default":true}}`), nil
		}
		if !strings.Contains(strings.Join(args, " "), "--continue") {
			t.Fatal("did not continue config")
		}
		return []byte(`{"State":""}`), nil
	}
	err := configureRemote(context.Background(), true, output, func(q Question) (string, error) {
		if q.Option.Name != "config_refresh_token" {
			t.Fatal("wrong question")
		}
		return "true", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestConfigurationStopsOnBackendErrorWithoutQuestion(t *testing.T) {
	for _, state := range []string{"choose_type", ""} {
		calls := 0
		err := configureRemote(context.Background(), true, func(_ context.Context, args ...string) ([]byte, error) {
			calls++
			if calls > 1 {
				return []byte(`{"State":""}`), nil
			}
			return json.Marshal(map[string]any{"State": state, "Error": "Failed to query root for drive private-drive: accessDenied private-token", "Option": nil})
		}, func(Question) (string, error) {
			t.Fatal("backend failure must not become a new prompt")
			return "", nil
		})
		if err == nil || calls != 1 {
			t.Fatalf("backend error swallowed: err=%v calls=%d", err, calls)
		}
		if strings.Contains(err.Error(), "private-") {
			t.Fatalf("provider details leaked: %v", err)
		}
	}
}

func TestSelectDriveWorksWithFreshLoginAndNoDriveID(t *testing.T) {
	// Simulate rawSettings on a config that has a token but no drive_id yet.
	tokenJSON := `{"access_token":"tok","refresh_token":"ref"}`
	calls := 0
	output := func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) >= 2 && args[0] == "config" && args[1] == "dump" {
			data, _ := json.Marshal(map[string]map[string]string{
				remote: {"type": "onedrive", "token": tokenJSON},
			})
			return data, nil
		}
		calls++
		switch calls {
		case 1:
			return []byte(`{"State":"drive","Option":{"Name":"config_driveid","Examples":[{"Value":"drive1","Help":"OneDrive (personal)"}]}}`), nil
		case 2:
			return []byte(`{"State":"ok","Option":{"Name":"config_drive_ok","Type":"bool","Default":true}}`), nil
		default:
			return []byte(`{"State":""}`), nil
		}
	}
	// rawSettings must succeed without a drive_id.
	raw, err := Client{}.rawSettingsFromOutput(context.Background(), output)
	if err != nil || raw["token"] != tokenJSON {
		t.Fatalf("rawSettings on fresh login: %v %q", err, raw["token"])
	}
	// configureDriveSelection must ask for and return the selected drive.
	asked := false
	id, err := configureDriveSelection(context.Background(), output, func(q Question) (string, error) {
		asked = true
		return "drive1", nil
	})
	if err != nil || id != "drive1" || !asked {
		t.Fatalf("configureDriveSelection: %v %q asked=%v", err, id, asked)
	}
}
