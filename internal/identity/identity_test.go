package identity

import (
	"context"
	"golang.org/x/oauth2"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestCallbackRejectsWrongStateAndAcceptsOnce(t *testing.T) {
	result := make(chan callbackResult, 1)
	handler := callbackHandler("expected", result)
	for _, raw := range []string{"/?state=wrong&code=secret", "/?code=secret"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", raw, nil))
		if w.Code != 400 {
			t.Fatalf("wrong state accepted: %d", w.Code)
		}
	}
	select {
	case <-result:
		t.Fatal("bad request consumed login")
	default:
	}
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/?state=expected&code=secret", nil))
		if i == 0 && w.Code != 200 {
			t.Fatalf("callback failed: %d", w.Code)
		}
		if i == 1 && w.Code != 409 {
			t.Fatalf("callback replay accepted: %d", w.Code)
		}
	}
	if got := <-result; got.code != "secret" || got.err != nil {
		t.Fatalf("callback = %+v", got)
	}
}

func TestCallbackHandlesDenialWithoutLeakingProviderText(t *testing.T) {
	result := make(chan callbackResult, 1)
	w := httptest.NewRecorder()
	callbackHandler("expected", result).ServeHTTP(w, httptest.NewRequest("GET", "/?state=expected&error=access_denied&error_description=sensitive", nil))
	if strings.Contains(w.Body.String(), "sensitive") {
		t.Fatal("provider text leaked")
	}
	if got := <-result; got.err == nil {
		t.Fatal("denial accepted")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestIdentityRequiresStableSubject(t *testing.T) {
	for _, body := range []string{`{}`, `{"email":"person@example.com"}`, `{"sub":"stable","email":"person@example.com"}`} {
		client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Authorization") != "Bearer access" {
				t.Error("missing bearer")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}
		_, err := fetchAccount(context.Background(), client, "https://example.test/userinfo", "access")
		if (err == nil) != strings.Contains(body, `"sub"`) {
			t.Fatalf("body=%s err=%v", body, err)
		}
	}
}

func TestScopesRequireEveryPermission(t *testing.T) {
	required := []string{"openid", "email", "photoslibrary.appendonly"}
	if err := requireScopes("openid email", required); err == nil {
		t.Fatal("missing permission accepted")
	}
	if err := requireScopes("email photoslibrary.appendonly openid", required); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshRejectsChangedClientBeforeNetwork(t *testing.T) {
	p, err := Configure("onedrive", func(string) string { return "11111111-2222-3333-4444-555555555555" })
	if err != nil {
		t.Fatal(err)
	}
	_, err = Refresh(context.Background(), p, Session{ClientID: "different"})
	if err == nil || !strings.Contains(err.Error(), "client changed") {
		t.Fatalf("err=%v", err)
	}
}

func TestRefreshRotatesTokenAndRejectsDifferentAccount(t *testing.T) {
	for _, subject := range []string{"stable", "different"} {
		// The HTTP integration uses an explicit test provider, without credential files.
		provider := Provider{Name: "google", UserInfoHost: "identity.test", Discovery: "https://identity.test/discovery"}
		provider.OAuth.ClientID = "client"
		provider.OAuth.ClientSecret = "client-secret"
		provider.OAuth.Endpoint.TokenURL = "https://identity.test/token"
		provider.OAuth.Endpoint.AuthStyle = oauth2.AuthStyleInParams
		client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			body := ""
			switch r.URL.Path {
			case "/token":
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if r.PostForm.Get("refresh_token") != "old-refresh" || r.PostForm.Get("grant_type") != "refresh_token" {
					t.Error("wrong refresh request")
				}
				body = `{"access_token":"access","token_type":"Bearer","expires_in":3600,"refresh_token":"rotated"}`
			case "/discovery":
				body = `{"userinfo_endpoint":"https://identity.test/userinfo"}`
			case "/userinfo":
				body = `{"sub":"` + subject + `","email":"user@example.com"}`
			default:
				t.Fatalf("unexpected request %s", r.URL.Path)
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		session := Session{ClientID: "client", Account: Account{Subject: "stable"}, RefreshToken: "old-refresh"}
		got, err := refreshWithClient(context.Background(), provider, session, client)
		if subject == "different" {
			if err == nil {
				t.Fatal("changed account accepted")
			}
		} else if err != nil || got.RefreshToken != "rotated" {
			t.Fatalf("refresh failed: %v", err)
		}
	}
}

func TestBrowserLoginUsesPKCEAndVerifiesIdentity(t *testing.T) {
	provider := Provider{Name: "google", Discovery: "https://identity.test/discovery", UserInfoHost: "identity.test", Required: []string{"upload"}}
	provider.OAuth = oauth2.Config{ClientID: "client", Scopes: []string{"upload"}, Endpoint: oauth2.Endpoint{AuthURL: "https://identity.test/auth", TokenURL: "https://identity.test/token", AuthStyle: oauth2.AuthStyleInParams}}
	challenge := ""
	listener := &pipeListener{connections: make(chan net.Conn), closed: make(chan struct{})}
	callbackClient := &http.Client{Transport: &http.Transport{DialContext: listener.dial}}
	defer callbackClient.CloseIdleConnections()
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			verifier := r.PostForm.Get("code_verifier")
			if verifier == "" || oauth2.S256ChallengeFromVerifier(verifier) != challenge {
				t.Error("PKCE verifier does not match authorization challenge")
			}
			if r.PostForm.Get("code") != "test-code" {
				t.Error("wrong code exchanged")
			}
			body = `{"access_token":"access","refresh_token":"refresh","token_type":"Bearer","scope":"upload"}`
		case "/discovery":
			body = `{"userinfo_endpoint":"https://identity.test/userinfo"}`
		case "/userinfo":
			body = `{"sub":"stable"}`
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	open := func(address string) error {
		u, err := url.Parse(address)
		if err != nil {
			return err
		}
		q := u.Query()
		challenge = q.Get("code_challenge")
		if q.Get("code_challenge_method") != "S256" || q.Get("access_type") != "offline" || q.Get("state") == "" {
			t.Error("missing OAuth protection")
		}
		callback, err := url.Parse(q.Get("redirect_uri"))
		if err != nil {
			return err
		}
		values := url.Values{"state": []string{q.Get("state")}, "code": []string{"test-code"}}
		callback.RawQuery = values.Encode()
		response, err := callbackClient.Get(callback.String())
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			t.Errorf("callback status %d", response.StatusCode)
		}
		return nil
	}
	session, err := loginWithListener(context.Background(), provider, open, client, listener)
	if err != nil || session.Account.Subject != "stable" || session.RefreshToken != "refresh" {
		t.Fatalf("login failed: %v", err)
	}
}

// net.Pipe exercises the HTTP callback server without requiring a TCP bind.
type pipeListener struct {
	connections chan net.Conn
	closed      chan struct{}
	once        sync.Once
}

func (p *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-p.connections:
		return c, nil
	case <-p.closed:
		return nil, net.ErrClosed
	}
}
func (p *pipeListener) Close() error   { p.once.Do(func() { close(p.closed) }); return nil }
func (p *pipeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1} }
func (p *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case p.connections <- server:
		return client, nil
	case <-ctx.Done():
		client.Close()
		server.Close()
		return nil, ctx.Err()
	case <-p.closed:
		client.Close()
		server.Close()
		return nil, net.ErrClosed
	}
}
