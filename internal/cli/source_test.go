package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"od2gp/internal/onedrive"
	"strings"
	"testing"
)

func TestDrivePickerUsesCurrentDriveAndSupportsCancel(t *testing.T) {
	var q onedrive.Question
	if err := json.Unmarshal([]byte(`{"Option":{"Name":"config_driveid","Default":"hidden","Examples":[{"Value":"hidden","Help":"Archive (personal)"},{"Value":"current","Help":"Photos (personal)"},{"Value":"main","Help":"OneDrive (personal)"}]}}`), &q); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	answer, err := selectDriveQuestion(bufio.NewReader(strings.NewReader("\n")), &out, "current", q, "")
	if err != nil || answer != "current" || !strings.Contains(out.String(), "current") {
		t.Fatalf("lost current drive: %s %v %s", answer, err, out.String())
	}
	_, err = selectDriveQuestion(bufio.NewReader(strings.NewReader("0\n")), &out, "current", q, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("selection did not cancel: %v", err)
	}
}

func TestDrivePickerShowsRetryMessageOnLoopBack(t *testing.T) {
	var q onedrive.Question
	if err := json.Unmarshal([]byte(`{"Option":{"Name":"config_driveid","Examples":[{"Value":"id1","Help":"OneDrive (personal)"}]}}`), &q); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	answer, err := selectDriveQuestion(bufio.NewReader(strings.NewReader("\n")), &out, "", q, "Drive not confirmed. Choose a different drive, or 0 to cancel.")
	if err != nil || answer != "id1" {
		t.Fatalf("unexpected result: %q %v", answer, err)
	}
	if !strings.Contains(out.String(), "Drive not confirmed") {
		t.Fatalf("retry message not shown: %s", out.String())
	}
}

func TestDrivePickerStopsInsteadOfRepeatingAfterRcloneError(t *testing.T) {
	var q onedrive.Question
	if err := json.Unmarshal([]byte(`{"Error":"selected drive unavailable","Option":{"Name":"config_driveid","Examples":[{"Value":"id1","Help":"OneDrive (personal)"}]}}`), &q); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	answer, err := selectDriveQuestion(bufio.NewReader(strings.NewReader("1\n")), &out, "", q, "")
	if answer != "" || err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("rclone error was converted into a repeat prompt: answer=%q err=%v", answer, err)
	}
	if strings.Contains(out.String(), "Choose a drive number") {
		t.Fatal("did not expect another drive-number prompt after rclone rejected the drive")
	}
}

func TestSourceBooleanAnswersRejectTypo(t *testing.T) {
	q := onedrive.Question{}
	q.Option.Name = "config_is_local"
	q.Option.Type = "bool"
	q.Option.Default = true
	for _, tc := range []struct {
		input, want string
		valid       bool
	}{{"tru", "", false}, {"", "true", true}, {"Y", "true", true}, {"no", "false", true}} {
		got, err := normalizeSourceAnswer(q, tc.input)
		if (err == nil) != tc.valid || got != tc.want {
			t.Fatalf("%q returned %q, %v", tc.input, got, err)
		}
	}
}

func TestDriveSelectionDefaultsToPersonalOneDriveAndSupportsNumbers(t *testing.T) {
	var q onedrive.Question
	data := `{"Option":{"Name":"config_driveid","Default":"metadata-id","Examples":[{"Value":"metadata-id","Help":"Metadata (personal)"},{"Value":"real-id","Help":"Bundles (personal)"},{"Value":"real-id","Help":"OneDrive (personal)"}]}}`
	if err := json.Unmarshal([]byte(data), &q); err != nil {
		t.Fatal(err)
	}
	q = sourceQuestionDefaults(q)
	for _, input := range []string{"", "3", "real-id"} {
		got, err := normalizeSourceAnswer(q, input)
		if err != nil || got != "real-id" {
			t.Fatalf("%q returned %q, %v", input, got, err)
		}
	}
	if _, err := normalizeSourceAnswer(q, "99"); err == nil {
		t.Fatal("nonexistent drive accepted")
	}
}
func TestDriveWithoutNamedOneDriveRequiresExplicitSelection(t *testing.T) {
	var q onedrive.Question
	if err := json.Unmarshal([]byte(`{"Option":{"Name":"config_driveid","Default":"metadata","Examples":[{"Value":"metadata","Help":"Archive (personal)"}]}}`), &q); err != nil {
		t.Fatal(err)
	}
	q = sourceQuestionDefaults(q)
	if _, err := normalizeSourceAnswer(q, ""); err == nil {
		t.Fatal("silently selected metadata default")
	}
}
