package identity

import (
	"github.com/zalando/go-keyring"
	"os"
	"path/filepath"
	"testing"
)

func TestDriveScopeAndCredentialIsolation(t *testing.T) {
	keyring.MockInit()
	p := filepath.Join(t.TempDir(), "client.json")
	if e := os.WriteFile(p, []byte(`{"installed":{"client_id":"test","client_secret":"test"}}`), 0600); e != nil {
		t.Fatal(e)
	}
	env := func(k string) string {
		if k == "OD2GP_DRIVE_CLIENT_FILE" || k == "OD2GP_GOOGLE_CLIENT_FILE" {
			return p
		}
		return ""
	}
	drive, e := Configure("gdrive", env)
	if e != nil {
		t.Fatal(e)
	}
	if len(drive.Required) != 1 || drive.Required[0] != "https://www.googleapis.com/auth/drive.file" {
		t.Fatal("wrong scope")
	}
	photos, e := Configure("google", env)
	if e != nil || photos.Required[0] == drive.Required[0] {
		t.Fatal("Photos permissions changed")
	}
	s := Session{ClientID: "test", Account: Account{Subject: "drive-account"}, RefreshToken: "token"}
	if e := Save("gdrive", s); e != nil {
		t.Fatal(e)
	}
	if _, e := Load("google"); !IsNotSignedIn(e) {
		t.Fatal("Drive token leaked into Photos")
	}
}
