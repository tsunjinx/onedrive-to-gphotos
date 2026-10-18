package identity

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
)

func TestNativeRefreshPersistsRotationAndKeepsDriveBinding(t *testing.T) {
	for _, subject := range []string{"stable", "changed"} {
		keyring.MockInit()
		p := Provider{Name: "onedrive", Discovery: "https://identity.test/discovery", UserInfoHost: "identity.test"}
		p.OAuth = oauth2.Config{ClientID: "client", Endpoint: oauth2.Endpoint{TokenURL: "https://identity.test/token", AuthStyle: oauth2.AuthStyleInParams}}
		saved := Session{ClientID: "client", Account: Account{Subject: "stable"}, RefreshToken: "old", DriveID: "drive"}
		if err := Save("onedrive", saved); err != nil {
			t.Fatal(err)
		}
		calls := 0
		client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			body := ""
			switch r.URL.Path {
			case "/token":
				body = `{"access_token":"access","refresh_token":"rotated","token_type":"Bearer","expires_in":3600}`
			case "/discovery":
				body = `{"userinfo_endpoint":"https://identity.test/userinfo"}`
			case "/userinfo":
				body = `{"sub":"` + subject + `"}`
			default:
				t.Fatalf("unexpected call: %s", r.URL)
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		source := persistentSource{provider: p, session: saved, ctx: context.WithValue(context.Background(), oauth2.HTTPClient, client), base: client}
		_, err := source.Token()
		if (subject == "stable") != (err == nil) {
			t.Fatalf("wrong identity result: %v", err)
		}
		restored, err := Load("onedrive")
		if err != nil || restored.RefreshToken != "rotated" || restored.DriveID != "drive" || restored.Account.Subject != "stable" {
			t.Fatalf("lost token rotation or binding: %+v %v", restored, err)
		}
		if subject == "stable" {
			if _, err := source.Token(); err != nil || calls != 3 {
				t.Fatalf("access token was not reused: calls=%d err=%v", calls, err)
			}
		}
		if _, err := Load("google"); !IsNotSignedIn(err) {
			t.Fatal("Microsoft credential was saved under Google")
		}
	}
}
