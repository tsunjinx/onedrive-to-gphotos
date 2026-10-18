package identity

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

type Account struct {
	Subject string `json:"sub"`
	Email   string `json:"email,omitempty"`
}
type Session struct {
	ClientID     string  `json:"client_id"`
	Account      Account `json:"account"`
	RefreshToken string  `json:"refresh_token"`
	DriveID      string  `json:"drive_id,omitempty"`
}
type callbackResult struct {
	code string
	err  error
}

func callbackHandler(state string, result chan<- callbackResult) http.Handler {
	var mu sync.Mutex
	used := false
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != "GET" || r.URL.Path != "/" {
			http.Error(w, "Not found", 404)
			return
		}
		q := r.URL.Query()
		if len(q["state"]) != 1 || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid authorization state", 400)
			return
		}
		var got callbackResult
		if q.Get("error") != "" {
			got.err = errors.New("authorization was declined or failed; no credentials saved")
		} else if len(q["code"]) != 1 || q.Get("code") == "" {
			http.Error(w, "Missing authorization code", 400)
			return
		} else {
			got.code = q.Get("code")
		}
		mu.Lock()
		defer mu.Unlock()
		if used {
			http.Error(w, "Authorization already received", 409)
			return
		}
		used = true
		result <- got
		fmt.Fprintln(w, "Authorization response received. Return to your terminal for the result.")
	})
}

func networkClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("unexpected redirect") }}
}

// Login exchanges a browser authorization code and verifies the account via HTTPS UserInfo.
// The caller must obtain affirmative consent before calling this function.
func Login(ctx context.Context, p Provider, open func(string) error) (Session, error) {
	return loginWithClient(ctx, p, open, networkClient())
}
func loginWithClient(ctx context.Context, p Provider, open func(string) error, client *http.Client) (Session, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return Session{}, errors.New("cannot open local browser callback listener")
	}
	return loginWithListener(ctx, p, open, client, listener)
}
func loginWithListener(ctx context.Context, p Provider, open func(string) error, client *http.Client, listener net.Listener) (Session, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	host := "127.0.0.1"
	if p.Name == "onedrive" {
		host = "localhost"
	}
	p.OAuth.RedirectURL = fmt.Sprintf("http://%s:%d/", host, port)
	state := oauth2.GenerateVerifier()
	verifier := oauth2.GenerateVerifier()
	results := make(chan callbackResult, 1)
	server := &http.Server{Handler: callbackHandler(state, results), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, MaxHeaderBytes: 8192}
	defer server.Close()
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}
	if p.Name == "google" {
		opts = append(opts, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent select_account"))
	} else {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", "select_account"))
	}
	if err := open(p.OAuth.AuthCodeURL(state, opts...)); err != nil {
		return Session{}, errors.New("could not open the system browser")
	}
	var response callbackResult
	select {
	case response = <-results:
	case <-ctx.Done():
		return Session{}, errors.New("sign-in cancelled or timed out; no credentials saved")
	case <-serveErrors:
		return Session{}, errors.New("local callback server stopped")
	}
	if response.err != nil {
		return Session{}, response.err
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, client)
	token, err := p.OAuth.Exchange(ctx, response.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Session{}, errors.New("authorization exchange failed; check client registration and consent, then retry")
	}
	scopes, _ := token.Extra("scope").(string)
	if err := requireScopes(scopes, p.Required); err != nil {
		return Session{}, err
	}
	if token.RefreshToken == "" {
		return Session{}, errors.New("provider did not issue offline access; revoke this app's old grant and sign in again")
	}
	account, err := verifiedAccount(ctx, client, p, token.AccessToken)
	if err != nil {
		return Session{}, err
	}
	return Session{ClientID: p.OAuth.ClientID, Account: account, RefreshToken: token.RefreshToken}, nil
}

func verifiedAccount(ctx context.Context, client *http.Client, p Provider, access string) (Account, error) {
	var discovery struct {
		UserInfo string `json:"userinfo_endpoint"`
	}
	if err := getJSON(ctx, client, p.Discovery, "", &discovery); err != nil {
		return Account{}, err
	}
	endpoint, err := url.Parse(discovery.UserInfo)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host != p.UserInfoHost || endpoint.User != nil || endpoint.Fragment != "" {
		return Account{}, errors.New("provider returned an unexpected identity endpoint")
	}
	return fetchAccount(ctx, client, endpoint.String(), access)
}
func fetchAccount(ctx context.Context, client *http.Client, endpoint, access string) (Account, error) {
	var account Account
	if err := getJSON(ctx, client, endpoint, access, &account); err != nil {
		return account, err
	}
	if account.Subject == "" {
		return Account{}, errors.New("provider did not return a stable account identifier")
	}
	return account, nil
}
func getJSON(ctx context.Context, client *http.Client, endpoint, access string, target any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return errors.New("invalid provider request")
	}
	if access != "" {
		req.Header.Set("Authorization", "Bearer "+access)
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("provider identity request failed; check connectivity")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("provider identity request failed (HTTP %d)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(data) > 65536 || json.Unmarshal(data, target) != nil {
		return errors.New("invalid provider identity response")
	}
	return nil
}

func Refresh(ctx context.Context, p Provider, s Session) (Session, error) {
	return refreshWithClient(ctx, p, s, networkClient())
}
func refreshWithClient(ctx context.Context, p Provider, s Session, client *http.Client) (Session, error) {
	if s.ClientID != p.OAuth.ClientID {
		return Session{}, errors.New("OAuth client changed; sign out and sign in again")
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, client)
	token, err := p.OAuth.TokenSource(ctx, &oauth2.Token{RefreshToken: s.RefreshToken}).Token()
	if err != nil {
		return Session{}, errors.New("cannot refresh access; check connectivity or sign in again if consent expired")
	}
	account, err := verifiedAccount(ctx, client, p, token.AccessToken)
	if err != nil {
		return Session{}, err
	}
	if account.Subject != s.Account.Subject {
		return Session{}, errors.New("account identity changed; refusing to continue")
	}
	s.Account = account
	if token.RefreshToken != "" {
		s.RefreshToken = token.RefreshToken
	}
	return s, nil
}
func OpenBrowser(address string) error {
	return openURL(address)
}
func OpenSetupURL(address string) error { return openURL(address) }
func openURL(address string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("/usr/bin/open", address)
	case "windows":
		cmd = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", address)
	default:
		return errors.New("unsupported operating system")
	}
	return cmd.Run()
}
