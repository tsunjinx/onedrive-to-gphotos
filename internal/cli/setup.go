package cli

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"od2gp/internal/identity"
	"od2gp/internal/settings"
)

const microsoftConsole = "https://entra.microsoft.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade"
const googleConsole = "https://console.cloud.google.com/auth/clients"

func RunSetup(input io.Reader, stdout, stderr io.Writer, getenv func(string) string, open func(string) error) int {
	fmt.Fprintln(stdout, "One-time account setup")
	fmt.Fprintln(stdout, "OneDrive uses native Microsoft Graph browser login with this project's registered public client.")
	reader := bufio.NewReader(io.LimitReader(input, 16*1024))
	fmt.Fprintln(stdout, "Google: enable the Photos Library API, configure the consent screen, create a Desktop app OAuth client, and download its JSON file.")
	if err := open(googleConsole); err != nil {
		fmt.Fprintf(stdout, "Open this Google setup page: %s\n", googleConsole)
	}
	fmt.Fprint(stdout, "Path to the downloaded Google Desktop client JSON: ")
	googleFile, err := readSetupLine(reader)
	if err != nil || checkGoogleClient(googleFile) != nil {
		fmt.Fprintln(stderr, "That file is not a readable Google Desktop OAuth client; settings were not changed.")
		return 2
	}
	clearPromptScreen(stdout)
	googleFile, err = filepath.Abs(googleFile)
	if err != nil {
		fmt.Fprintln(stderr, "Cannot resolve the Google client file path.")
		return 2
	}
	root := strings.TrimSpace(getenv("OD2GP_CONFIG_DIR"))
	if root == "" {
		root, err = settings.Directory()
		if err != nil {
			fmt.Fprintln(stderr, "Cannot locate your user config directory.")
			return 1
		}
	}
	saved, err := settings.Load(root)
	if err != nil {
		saved = settings.Settings{}
	}
	saved.GoogleClientFile = googleFile
	if err := settings.Save(root, saved); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "Setup saved privately on this computer. OAuth JSON remains at its original path.")
	fmt.Fprintln(stdout, "Run: od2gp doctor")
	fmt.Fprintln(stdout, "Then sign in: od2gp auth login onedrive and od2gp auth login google")
	return 0
}
func readSetupLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// RunMicrosoftSetup changes only public app settings, never tokens or jobs.
func RunMicrosoftSetup(clientID string, stdout, stderr io.Writer, getenv func(string) string) int {
	if strings.TrimSpace(clientID) == "" {
		clientID = settings.DefaultMicrosoftClientID
	}
	if _, err := identity.Configure("onedrive", func(key string) string {
		if key == "OD2GP_MICROSOFT_CLIENT_ID" {
			return clientID
		}
		return ""
	}); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	root := strings.TrimSpace(getenv("OD2GP_CONFIG_DIR"))
	if root == "" {
		var err error
		root, err = settings.Directory()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	saved, err := settings.Load(root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	saved.MicrosoftClientID = strings.TrimSpace(clientID)
	saved.SourceBackend = "graph"
	if err := settings.Save(root, saved); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "Microsoft public client configured: %s\nNew jobs use native Graph. Existing jobs keep their backend and progress.\nNext: auth login onedrive\n", saved.MicrosoftClientID)
	return 0
}
