package onedrive

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestInventoryReportsTruncatedStreamAfter1800Files(t *testing.T) {
	var data strings.Builder
	data.WriteString("[")
	for i := 0; i < 1800; i++ {
		if i > 0 {
			data.WriteString(",")
		}
		fmt.Fprintf(&data, `{"Path":"photo-%d.jpg","Size":12}`, i)
	}
	data.WriteString(`,{"Path":"private-name`)
	count := 0
	err := decodeInventory(strings.NewReader(data.String()), func(Item) error { count++; return nil })
	if count != 1800 || err == nil || !strings.Contains(err.Error(), "ended early after 1800") {
		t.Fatalf("wrong truncated listing diagnostic: count=%d err=%v", count, err)
	}
	if strings.Contains(err.Error(), "private-name") {
		t.Fatal("source data leaked")
	}
}

func TestInventoryRejectsInvalidEntriesWithoutSkipping(t *testing.T) {
	for _, tc := range []struct{ data, want string }{
		{`[{"Path":"secret.jpg","Size":"secret-value"}]`, `field "Size"`},
		{`[{"Path":"secret.jpg"},broken]`, "invalid JSON"},
		{`[{"Path":"ok.jpg"}`, "ended early after 1"},
	} {
		err := decodeInventory(strings.NewReader(tc.data), func(Item) error { return nil })
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("wrong diagnostic: %v", err)
		}
	}
}

func TestInventoryReadsCompleteListingIncludingDirectories(t *testing.T) {
	var items []Item
	err := decodeInventory(strings.NewReader(`[{"Path":"Pictures","IsDir":true},{"Path":"Pictures/photo.jpg","Size":123,"Hashes":{"quickxor":"abc"}}]`), func(item Item) error { items = append(items, item); return nil })
	if err != nil || len(items) != 2 || !items[0].IsDir || items[1].Hashes["quickxor"] != "abc" {
		t.Fatalf("valid listing failed: %+v %v", items, err)
	}
}

func TestInventoryChild(t *testing.T) {
	if os.Getenv("OD2GP_TEST_INVENTORY_CHILD") != "1" {
		return
	}
	fmt.Fprint(os.Stdout, `[{"Path":"photo.jpg","Size":12}`)
	fmt.Fprintln(os.Stderr, "ERROR : private-folder/private-photo.jpg: AccessDenied private-token https://private.example/")
	os.Exit(3)
}

func TestInventoryKeepsProviderFailureAfterIncompleteJSON(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestInventoryChild$")
	cmd.Env = append(os.Environ(), "OD2GP_TEST_INVENTORY_CHILD=1")
	count := 0
	err := runInventoryCommand(context.Background(), cmd, func(Item) error { count++; return nil })
	if count != 1 || err == nil || !strings.Contains(err.Error(), "denied access") || !strings.Contains(err.Error(), "exit 3") {
		t.Fatalf("provider failure was lost: count=%d err=%v", count, err)
	}
	if strings.Contains(err.Error(), "invalid JSON") || strings.Contains(err.Error(), "private") {
		t.Fatalf("misleading or unsafe diagnostic: %v", err)
	}
}

func TestInventoryDiagnosticTailIsBoundedAndNonFailing(t *testing.T) {
	b := &inventoryDiagnostics{}
	data := []byte(strings.Repeat("private", 10000))
	n, err := b.Write(data)
	if n != len(data) || err != nil || len(b.data) > 16<<10 {
		t.Fatal("diagnostic writer failed or exceeded bound")
	}
	b.Write([]byte(" AccessDenied"))
	if len(b.data) > 16<<10 || !strings.HasSuffix(string(b.data), " AccessDenied") {
		t.Fatal("latest diagnostic lost")
	}
}

func TestInventoryDiagnosticRedactsURLsAndCredentialFields(t *testing.T) {
	input := `private-folder/photo.jpg: https://example.com/path?access_token=URLSECRET authorization: Bearer HEADERSECRET refresh_token="REFRESHSECRET" {"client_secret":"CLIENTSECRET"} password=PASSSECRET`
	got := redactInventoryDiagnostic(input)
	for _, secret := range []string{"URLSECRET", "HEADERSECRET", "REFRESHSECRET", "CLIENTSECRET", "PASSSECRET", "example.com"} {
		if strings.Contains(got, secret) {
			t.Fatalf("diagnostic leaked %s: %s", secret, got)
		}
	}
	if !strings.Contains(got, "private-folder/photo.jpg") {
		t.Fatal("lost useful filename in opt-in diagnostic")
	}
}

func TestOptInInventoryDiagnosticIsAvailableOnFailure(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestInventoryChild$")
	cmd.Env = append(os.Environ(), "OD2GP_TEST_INVENTORY_CHILD=1")
	var diagnostic strings.Builder
	err := runInventoryCommand(context.Background(), cmd, func(Item) error { return nil }, &diagnostic)
	if err == nil || !strings.Contains(diagnostic.String(), "private-folder") || strings.Contains(diagnostic.String(), "https://private.example") {
		t.Fatalf("opt-in diagnostic unavailable or URL leaked: %v %s", err, diagnostic.String())
	}
}

func TestVaultExclusionIsExplicitAnchoredAndDisablesListR(t *testing.T) {
	args, err := inventoryArgs("", []string{"--recursive", "--files-only", "--hash"}, true)
	if err != nil || !strings.Contains(strings.Join(args, " "), "--disable ListR --exclude /Personal Vault/**") {
		t.Fatalf("vault exclusion missing: %v %v", args, err)
	}
	args, err = inventoryArgs("", nil, false)
	if err != nil || strings.Contains(strings.Join(args, " "), "--exclude") {
		t.Fatal("silently excluded vault")
	}
	args, err = inventoryArgs("Pictures", nil, true)
	if err != nil || strings.Contains(strings.Join(args, " "), "--exclude") {
		t.Fatal("excluded a normal nested folder")
	}
	for _, folder := range []string{"Personal Vault", "Personal Vault/Photos"} {
		if _, err := inventoryArgs(folder, nil, true); err == nil {
			t.Fatalf("claimed an excluded source was scanned: %s", folder)
		}
	}
}
