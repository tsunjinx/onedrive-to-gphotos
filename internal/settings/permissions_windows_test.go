package settings

import (
	"golang.org/x/sys/windows"
	"path/filepath"
	"strings"
	"testing"
)

func TestSavedSettingsHaveProtectedWindowsACL(t *testing.T) {
	root := t.TempDir()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Save(root, Settings{GoogleClientFile: "client.json"}); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{filepath.Join(root, "od2gp"), filepath.Join(root, "od2gp", "config.json")} {
			sd, err := windows.GetNamedSecurityInfo(name, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
			if err != nil {
				t.Fatal(err)
			}
			s := sd.String()
			flags := ""
			if filepath.Base(name) == "od2gp" {
				flags = "OICI"
			}
			expected := "D:P(A;" + flags + ";FA;;;" + user.User.Sid.String() + ")(A;" + flags + ";FA;;;SY)"
			// Normalize SID aliases (e.g. the runner's local administrator is LA)
			// through the Windows SDDL serializer before comparing ACE membership.
			canonical, err := windows.SecurityDescriptorFromString(expected)
			if err != nil {
				t.Fatal(err)
			}
			expected = canonical.String()
			userACE := strings.Split(strings.Split(expected, "(")[1], ")")[0]
			// Compare ACE membership independent of canonical ordering by the OS.
			if !strings.HasPrefix(s, "D:P") || strings.Count(s, "(A;") != 2 ||
				!strings.Contains(s, "("+userACE+")") ||
				!strings.Contains(s, "(A;"+flags+";FA;;;SY)") {
				t.Fatalf("unexpected ACL %s; expected equivalent of %s", s, expected)
			}
		}
	}
}
