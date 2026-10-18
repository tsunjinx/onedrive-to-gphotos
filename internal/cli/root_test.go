package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpDoesNotRequireCredentials(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runIsolated(t, []string{"--help"}, &out, &errOut, func(string) string { return "" })
	if code != 0 || !strings.Contains(out.String(), "OneDrive -> Google Photos") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}

func TestDoctorReportsMissingConfiguration(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runIsolated(t, []string{"doctor"}, &out, &errOut, func(string) string { return "" })
	if code != 2 || !strings.Contains(errOut.String(), "OD2GP_GOOGLE_CLIENT_FILE") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}

func TestDoctorValidatesDesktopConfigWithoutLeakingSecrets(t *testing.T) {
	for _, tc := range []struct {
		name, document string
		want           int
	}{
		{"desktop", `{"installed":{"client_id":"example.apps.googleusercontent.com","client_secret":"sensitive-fixture","redirect_uris":["http://localhost"]}}`, 0},
		{"web client", `{"web":{"client_id":"example.apps.googleusercontent.com","client_secret":"sensitive-fixture"}}`, 2},
		{"missing secret", `{"installed":{"client_id":"example.apps.googleusercontent.com"}}`, 2},
		{"invalid JSON", `sensitive-fixture`, 2},
		{"trailing data", `{"installed":{"client_id":"example.apps.googleusercontent.com","client_secret":"sensitive-fixture"}} trailing`, 2},
		{"oversized", strings.Repeat("sensitive-fixture", 5000), 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "client.json")
			if err := os.WriteFile(path, []byte(tc.document), 0600); err != nil {
				t.Fatal(err)
			}
			env := func(key string) string {
				switch key {
				case "OD2GP_MICROSOFT_CLIENT_ID":
					return "11111111-2222-3333-4444-555555555555"
				case "OD2GP_GOOGLE_CLIENT_FILE":
					return path
				}
				return ""
			}
			var out, errOut bytes.Buffer
			code := runIsolated(t, []string{"doctor"}, &out, &errOut, env)
			if code != tc.want {
				t.Fatalf("code=%d want=%d stderr=%q", code, tc.want, errOut.String())
			}
			if strings.Contains(out.String()+errOut.String(), "sensitive-fixture") {
				t.Fatal("credential content leaked")
			}
			if code == 0 && !strings.Contains(out.String(), "sign-in is checked separately") {
				t.Fatal("configuration check must not imply authentication")
			}
		})
	}
}

func TestUnknownCommandsAndExtraArgumentsFail(t *testing.T) {
	for _, args := range [][]string{{"run"}, {"doctor", "--unknown"}, {"version", "extra"}} {
		var out, errOut bytes.Buffer
		if code := runIsolated(t, args, &out, &errOut, func(string) string { return "" }); code != 2 {
			t.Fatalf("%v: code=%d", args, code)
		}
	}
}

func TestDoctorDoesNotRequireMicrosoftAppRegistration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(path, []byte(`{"installed":{"client_id":"id","client_secret":"secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := runIsolated(t, []string{"doctor"}, &out, &errOut, func(key string) string {
		if key == "OD2GP_GOOGLE_CLIENT_FILE" {
			return path
		}
		return ""
	})
	if code != 0 {
		t.Fatalf("doctor still requires Microsoft app: %s", errOut.String())
	}
}

func TestLoginDeclinedBeforeBrowserOrSecureStore(t *testing.T) {
	var out, errOut bytes.Buffer
	env := func(key string) string {
		if key == "OD2GP_MICROSOFT_CLIENT_ID" {
			return "11111111-2222-3333-4444-555555555555"
		}
		return ""
	}
	code := runIsolated(t, []string{"auth", "login", "onedrive"}, &out, &errOut, env)
	if code != 1 || !strings.Contains(errOut.String(), "cancelled") || !strings.Contains(out.String(), "read-only file access") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}

func runIsolated(t *testing.T, args []string, out, errOut io.Writer, getenv func(string) string) int {
	t.Helper()
	root := t.TempDir()
	return Run(args, out, errOut, func(key string) string {
		if key == "OD2GP_CONFIG_DIR" {
			return root
		}
		return getenv(key)
	})
}
