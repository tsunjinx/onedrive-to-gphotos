package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"od2gp/internal/identity"
)

const privacyNotice = `od2gp personal-use privacy notice

Direction: your OneDrive photos and videos -> your Google Photos library.
OneDrive requests read-only file access. Google access permits uploading and reading
items created by this app so it can check video processing. Basic account
identity is used to show the connected account and prevent account mix-ups.

Signing in does not start a transfer. Your password stays with the provider.
Google and native Microsoft Graph refresh credentials are stored in macOS
Keychain or Windows Credential Manager. Access tokens stay in memory. Legacy
rclone jobs use their encrypted config and OS-held encryption password.
There is no hosted relay, telemetry, advertising, sale of data or AI training.
Transfers stage encrypted media locally and save encrypted progress.

Use 'auth logout onedrive' or 'auth logout google' to delete the saved local
credential. This does not delete cloud files or revoke the provider's grant.
Revoke access at https://account.live.com/consent/Manage or
https://myaccount.google.com/connections. Your downloaded OAuth client JSON
remains where you saved it and can be deleted manually when no longer needed.
The use of Google API data is subject to the Google API Services User Data
Policy, including Limited Use: https://developers.google.com/terms/api-services-user-data-policy
`

func authCommand(ctx context.Context, args []string, input io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) != 2 || (args[1] != "onedrive" && args[1] != "google") {
		fmt.Fprintln(stderr, "Usage: od2gp auth <login|status|check|logout> <onedrive|google>")
		return 2
	}
	action, name := args[0], args[1]
	switch action {
	case "logout":
		if err := identity.Logout(name); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "Local credential removed. Cloud files are unchanged.")
		if name == "google" {
			fmt.Fprintln(stdout, "Revoke the provider grant at https://myaccount.google.com/connections")
		} else {
			fmt.Fprintln(stdout, "Revoke the provider grant at https://account.live.com/consent/Manage")
		}
		return 0
	case "status":
		s, err := identity.Load(name)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s saved account: %q (local record; access has not been checked)\n", name, accountLabel(s))
		return 0
	case "login", "check":
	default:
		fmt.Fprintln(stderr, "Unknown auth command; use login, status, check or logout.")
		return 2
	}
	p, err := identity.Configure(name, getenv)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if action == "check" {
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		_, updated, err := identity.AuthorizedClient(ctx, p)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s account %q: refreshed access and identity verified.\n", name, accountLabel(updated))
		return 0
	}
	fmt.Fprint(stdout, privacyNotice)
	fmt.Fprintf(stdout, "Connect %s using the browser and save its offline credential? Type yes: ", name)
	// A bounded line is sufficient for consent; EOF and every other response decline.
	line, err := bufio.NewReader(io.LimitReader(input, 64)).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "yes" {
		fmt.Fprintln(stderr, "Sign-in cancelled; no browser opened.")
		return 1
	}
	clearPromptScreen(stdout)
	if err := identity.CheckStore(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	previous, err := identity.Load(name)
	if err != nil && !identity.IsNotSignedIn(err) {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "Opening your browser. Complete sign-in within five minutes; Ctrl+C cancels.")
	s, err := identity.Login(ctx, p, identity.OpenBrowser)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if previous.Account.Subject != "" && (previous.Account.Subject != s.Account.Subject || previous.ClientID != s.ClientID) {
		fmt.Fprintln(stderr, "A different account or OAuth client is already saved. Log out explicitly before changing accounts; the saved credential was kept.")
		return 1
	}
	if previous.Account.Subject == s.Account.Subject && previous.ClientID == s.ClientID {
		s.DriveID = previous.DriveID
	}
	if err := identity.Save(name, s); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "Connected %s account %q. No media transferred.\n", name, accountLabel(s))
	return 0
}
func accountLabel(s identity.Session) string {
	if s.Account.Email != "" {
		return s.Account.Email
	}
	return s.Account.Subject
}
