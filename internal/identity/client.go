package identity

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// GoogleClient verifies account binding and persists every refreshed offline token.
func GoogleClient(ctx context.Context, p Provider) (*http.Client, Session, error) {
	return AuthorizedClient(ctx, p)
}

// AuthorizedClient persists rotated refresh credentials for either provider.
func AuthorizedClient(ctx context.Context, p Provider) (*http.Client, Session, error) {
	saved, err := Load(p.Name)
	if err != nil {
		return nil, Session{}, err
	}
	if saved.ClientID != p.OAuth.ClientID {
		return nil, saved, errors.New("OAuth client changed; sign in again")
	}
	base := networkClient()
	source := &persistentSource{ctx: context.WithValue(ctx, oauth2.HTTPClient, base), provider: p, session: saved, base: base}
	if _, err := source.Token(); err != nil {
		return nil, saved, err
	}
	client := &http.Client{Timeout: 10 * time.Minute, Transport: &oauth2.Transport{Source: source, Base: http.DefaultTransport}, CheckRedirect: base.CheckRedirect}
	return client, source.session, nil
}

type persistentSource struct {
	mu       sync.Mutex
	ctx      context.Context
	provider Provider
	session  Session
	current  *oauth2.Token
	base     *http.Client
}

func (s *persistentSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current.Valid() && (s.current.Expiry.IsZero() || time.Until(s.current.Expiry) > time.Minute) {
		return s.current, nil
	}
	token, err := s.provider.OAuth.TokenSource(s.ctx, &oauth2.Token{RefreshToken: s.session.RefreshToken}).Token()
	if err != nil {
		return nil, errors.New("access could not be refreshed; sign in again if consent expired")
	}
	// Save rotation before any further network call can fail.
	if token.RefreshToken != "" {
		s.session.RefreshToken = token.RefreshToken
	}
	if err := Save(s.provider.Name, s.session); err != nil {
		return nil, err
	}
	account, err := verifiedAccount(s.ctx, s.base, s.provider, token.AccessToken)
	if err != nil {
		return nil, err
	}
	if account.Subject != s.session.Account.Subject {
		return nil, errors.New("account changed; refusing transfer")
	}
	s.session.Account = account
	s.current = token
	return token, nil
}
