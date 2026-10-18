package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"od2gp/internal/journal"
	"od2gp/internal/migration"
	"od2gp/internal/onedrive"
)

func TestPlanValidationDoesNotLeaveJobsOrSuggestResume(t *testing.T) {
	for _, folder := range []string{"../Pictures", `Pictures\Test`, "/Pictures/Test"} {
		t.Run(folder, func(t *testing.T) {
			root := t.TempDir()
			var out, errOut bytes.Buffer
			code := migrationCommand(context.Background(), "plan", []string{"--source", folder}, strings.NewReader(""), &out, &errOut, func(string) string { return "" }, root)
			if code != 1 {
				t.Fatalf("code=%d: %s", code, errOut.String())
			}
			if strings.Contains(errOut.String(), "Resume with:") {
				t.Fatalf("suggested resuming an uninitialized job: %s", errOut.String())
			}
			entries, err := os.ReadDir(filepath.Join(root, "od2gp", "jobs"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatal("failed validation left a job behind")
			}
		})
	}
}

func TestMissingJobDoesNotSuggestResume(t *testing.T) {
	var out, errOut bytes.Buffer
	code := migrationCommand(context.Background(), "resume", []string{"--job", strings.Repeat("a", 32)}, strings.NewReader(""), &out, &errOut, func(string) string { return "" }, t.TempDir())
	if code != 1 || strings.Contains(errOut.String(), "Resume with:") {
		t.Fatalf("misleading recovery output: code=%d %s", code, errOut.String())
	}
}

func TestWaitFlagRequiresTransferCommand(t *testing.T) {
	for _, command := range []string{"plan", "status", "report"} {
		var out, errOut bytes.Buffer
		code := migrationCommand(context.Background(), command, []string{"--wait"}, strings.NewReader(""), &out, &errOut, func(string) string { return "" }, t.TempDir())
		if code != 2 || !strings.Contains(errOut.String(), "--wait is supported by run and resume") {
			t.Fatalf("%s accepted --wait: code=%d %s", command, code, errOut.String())
		}
	}
}

func TestRetryFlagValidationPrecedesJobAccess(t *testing.T) {
	for _, args := range [][]string{{"--retries", "-1"}, {"--retries", "11"}} {
		var out, errOut bytes.Buffer
		code := migrationCommand(context.Background(), "resume", args, strings.NewReader(""), &out, &errOut, func(string) string { return "" }, t.TempDir())
		if code != 2 || !strings.Contains(errOut.String(), "--retries must be between 0 and 10") {
			t.Fatalf("invalid retry count accepted: code=%d %s", code, errOut.String())
		}
	}
	for _, command := range []string{"plan", "status", "report"} {
		var out, errOut bytes.Buffer
		code := migrationCommand(context.Background(), command, []string{"--retries", "0"}, strings.NewReader(""), &out, &errOut, func(string) string { return "" }, t.TempDir())
		if code != 2 || !strings.Contains(errOut.String(), "--retries is supported by run and resume") {
			t.Fatalf("%s accepted retry flag: code=%d %s", command, code, errOut.String())
		}
	}
}

func TestReportContainsNoUploadSecretsAndMakesCSVSafe(t *testing.T) {
	store, err := journal.Open(filepath.Join(t.TempDir(), "journal.db"), bytes.Repeat([]byte{8}, 32), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item := migration.Item{Path: "=FORMULA.jpg", State: "uploading", Token: "private-token", Source: onedrive.Item{Size: 10}}
	item.Upload.URL = "https://photoslibrary.googleapis.com/private-session"
	if err := store.Save(1, item.State, item); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "csv"} {
		var out bytes.Buffer
		if err := writeMigrationReport(store, migration.Meta{}, format, &out); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "private-") {
			t.Fatal("report disclosed an upload credential")
		}
		if format == "csv" && !strings.Contains(out.String(), "'=FORMULA.jpg") {
			t.Fatal("CSV formula injection not escaped")
		}
	}
}

func TestVaultExclusionCanOnlyChangeIncompleteRootInventory(t *testing.T) {
	for _, meta := range []migration.Meta{{InventoryComplete: true}, {Folder: "Pictures"}} {
		if err := enableVaultExclusion(&meta); err == nil || meta.ExcludePersonalVault {
			t.Fatalf("changed bound scope: %+v %v", meta, err)
		}
	}
	meta := migration.Meta{ID: strings.Repeat("a", 32), CacheBytes: 10 << 30}
	if err := enableVaultExclusion(&meta); err != nil || !meta.ExcludePersonalVault || meta.CacheBytes != 10<<30 {
		t.Fatalf("failed to preserve job: %+v %v", meta, err)
	}
	store, err := journal.Open(filepath.Join(t.TempDir(), "job.db"), bytes.Repeat([]byte{1}, 32), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(0, "job", meta); err != nil {
		t.Fatal(err)
	}
	var restored migration.Meta
	if err := store.Load(0, &restored); err != nil || !restored.ExcludePersonalVault {
		t.Fatalf("scope not persisted: %+v %v", restored, err)
	}
	for _, format := range []string{"csv", "json"} {
		var out bytes.Buffer
		if err := writeMigrationReport(store, restored, format, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "/Personal Vault") || !strings.Contains(out.String(), "excluded_folder") || !strings.Contains(out.String(), "unknown") {
			t.Fatalf("exclusion hidden from %s report: %s", format, out.String())
		}
	}
	var out bytes.Buffer
	if err := migrationSummary(store, restored, &out); err != nil || !strings.Contains(out.String(), "file count and size are unknown") {
		t.Fatalf("exclusion hidden from summary: %v %s", err, out.String())
	}
}

func TestOldJobsStayOnRcloneAndNativeBindingSurvivesReopen(t *testing.T) {
	if migrationBackend(migration.Meta{}) != "rclone" {
		t.Fatal("old job silently changed source backend")
	}
	meta := migration.Meta{SourceBackend: "graph", SourceSubject: "microsoft-account", SourceClient: "client", DriveID: "drive", ExcludePersonalVault: true}
	store, err := journal.Open(filepath.Join(t.TempDir(), "job.db"), bytes.Repeat([]byte{2}, 32), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(0, "job", meta); err != nil {
		t.Fatal(err)
	}
	var restored migration.Meta
	if err := store.Load(0, &restored); err != nil || migrationBackend(restored) != "graph" || restored.SourceSubject != meta.SourceSubject || restored.SourceClient != meta.SourceClient || !restored.ExcludePersonalVault {
		t.Fatalf("lost native binding: %+v %v", restored, err)
	}
}

func TestWorkerFlagValidationPrecedesJobAccess(t *testing.T) {
	for _, tc := range []struct{ command, value, want string }{{"resume", "0", "between 1 and 8"}, {"run", "9", "between 1 and 8"}, {"plan", "3", "supported by run and resume"}, {"status", "1", "supported by run and resume"}} {
		var out, errOut bytes.Buffer
		code := migrationCommand(context.Background(), tc.command, []string{"--workers", tc.value}, strings.NewReader(""), &out, &errOut, func(string) string { return "" }, t.TempDir())
		if code != 2 || !strings.Contains(errOut.String(), tc.want) {
			t.Fatalf("invalid workers accepted: %d %s", code, errOut.String())
		}
	}
}
