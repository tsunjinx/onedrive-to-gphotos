package identity

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"

	"golang.org/x/oauth2"
)

type Provider struct {
	Name         string
	OAuth        oauth2.Config
	Discovery    string
	UserInfoHost string
	Required     []string
}

func Configure(name string, getenv func(string) string) (Provider, error) {
	p := Provider{Name: name}
	switch name {
	case "onedrive":
		id := strings.TrimSpace(getenv("OD2GP_MICROSOFT_CLIENT_ID"))
		if !regexp.MustCompile(`(?i)^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`).MatchString(id) {
			return p, errors.New("set OD2GP_MICROSOFT_CLIENT_ID to your Microsoft Application (client) ID")
		}
		p.OAuth = oauth2.Config{ClientID: id, Scopes: []string{"openid", "email", "offline_access", "https://graph.microsoft.com/Files.Read"}, Endpoint: oauth2.Endpoint{AuthURL: "https://login.microsoftonline.com/consumers/oauth2/v2.0/authorize", TokenURL: "https://login.microsoftonline.com/consumers/oauth2/v2.0/token", AuthStyle: oauth2.AuthStyleInParams}}
		p.Discovery = "https://login.microsoftonline.com/consumers/v2.0/.well-known/openid-configuration"
		p.UserInfoHost = "graph.microsoft.com"
		p.Required = []string{"Files.Read"}
	case "google":
		file, err := os.Open(getenv("OD2GP_GOOGLE_CLIENT_FILE"))
		if err != nil {
			return p, errors.New("set OD2GP_GOOGLE_CLIENT_FILE to a readable Google Desktop app JSON file")
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, 65537))
		if err != nil || len(data) > 65536 {
			return p, errors.New("Google client file cannot be read or exceeds 64 KiB")
		}
		var cfg struct {
			Installed *struct {
				ID     string `json:"client_id"`
				Secret string `json:"client_secret"`
			} `json:"installed"`
		}
		if json.Unmarshal(data, &cfg) != nil || cfg.Installed == nil || strings.TrimSpace(cfg.Installed.ID) == "" || strings.TrimSpace(cfg.Installed.Secret) == "" {
			return p, errors.New("Google client file must contain an installed Desktop app client_id and client_secret")
		}
		p.Required = []string{"https://www.googleapis.com/auth/photoslibrary.appendonly", "https://www.googleapis.com/auth/photoslibrary.readonly.appcreateddata"}
		p.OAuth = oauth2.Config{ClientID: cfg.Installed.ID, ClientSecret: cfg.Installed.Secret, Scopes: append([]string{"openid", "email"}, p.Required...), Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", AuthStyle: oauth2.AuthStyleInParams}}
		p.Discovery = "https://accounts.google.com/.well-known/openid-configuration"
		p.UserInfoHost = "openidconnect.googleapis.com"
	default:
		return p, errors.New("provider must be onedrive or google")
	}
	return p, nil
}

func requireScopes(granted string, required []string) error {
	set := map[string]bool{}
	for _, scope := range strings.Fields(granted) {
		set[strings.TrimPrefix(scope, "https://graph.microsoft.com/")] = true
	}
	for _, scope := range required {
		if !set[strings.TrimPrefix(scope, "https://graph.microsoft.com/")] {
			return errors.New("required permissions were not granted; sign in again and grant the requested access")
		}
	}
	return nil
}
