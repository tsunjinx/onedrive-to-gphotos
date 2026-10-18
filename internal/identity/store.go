package identity

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/zalando/go-keyring"
)

const service = "od2gp"

var errNotSignedIn = errors.New("not signed in")

func IsNotSignedIn(err error) bool { return errors.Is(err, errNotSignedIn) }
func CheckStore() error {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return errors.New("cannot initialize secure store probe")
	}
	name := "probe-" + hex.EncodeToString(random[:])
	if err := keyring.Set(service, name, "probe"); err != nil {
		return errors.New("OS credential store unavailable; sign-in stopped")
	}
	if err := keyring.Delete(service, name); err != nil {
		return errors.New("cannot remove secure store probe; sign-in stopped")
	}
	return nil
}

// Only a compact offline credential is persisted; access/ID tokens stay in memory.
// Both supported keyrings impose size limits. Fail closed rather than writing a file.
func Save(provider string, s Session) error {
	if provider != "onedrive" && provider != "google" {
		return errors.New("invalid provider")
	}
	data, err := json.Marshal(s)
	if err != nil {
		return errors.New("cannot encode credentials")
	}
	if len(data) > 2000 {
		return errors.New("offline credential exceeds secure-store limit; no plaintext fallback is available")
	}
	if err := keyring.Set(service, provider, string(data)); err != nil {
		return errors.New("cannot save credentials to the OS credential store")
	}
	return nil
}
func Load(provider string) (Session, error) {
	var s Session
	data, err := keyring.Get(service, provider)
	if errors.Is(err, keyring.ErrNotFound) {
		return s, errNotSignedIn
	}
	if err != nil {
		return s, errors.New("cannot read the OS credential store")
	}
	if json.Unmarshal([]byte(data), &s) != nil || s.RefreshToken == "" || s.Account.Subject == "" || s.ClientID == "" {
		return Session{}, errors.New("stored credentials are invalid; sign out and sign in again")
	}
	return s, nil
}
func Logout(provider string) error {
	if provider != "onedrive" && provider != "google" {
		return errors.New("invalid provider")
	}
	err := keyring.Delete(service, provider)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return errors.New("could not remove credentials from the OS credential store")
	}
	return nil
}
