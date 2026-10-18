package identity

import (
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestSecureSessionRoundTripAndLogout(t *testing.T) {
	keyring.MockInit()
	if err := CheckStore(); err != nil {
		t.Fatal(err)
	}
	session := Session{ClientID: "client", Account: Account{Subject: "stable", Email: "user@example.com"}, RefreshToken: "private-refresh"}
	if err := Save("google", session); err != nil {
		t.Fatal(err)
	}
	got, err := Load("google")
	if err != nil || got != session {
		t.Fatalf("round trip failed: %v", err)
	}
	if err := Logout("google"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("google"); !IsNotSignedIn(err) {
		t.Fatalf("expected not signed in: %v", err)
	}
}

func TestOversizedCredentialDoesNotReplaceExisting(t *testing.T) {
	keyring.MockInit()
	original := Session{ClientID: "client", Account: Account{Subject: "stable"}, RefreshToken: "original"}
	if err := Save("google", original); err != nil {
		t.Fatal(err)
	}
	oversized := original
	oversized.RefreshToken = strings.Repeat("secret", 1000)
	if err := Save("google", oversized); err == nil {
		t.Fatal("oversized credential saved")
	}
	got, err := Load("google")
	if err != nil || got != original {
		t.Fatal("existing credential lost")
	}
}
