package onedrive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDriveSelectionReusesTokenAndAlwaysOffersDrives(t *testing.T) {
	responses := []string{
		`{"State":"refresh","Option":{"Name":"config_refresh_token"}}`,
		`{"State":"type","Option":{"Name":"config_type"}}`,
		`{"State":"drive","Option":{"Name":"config_driveid","Examples":[{"Value":"old","Help":"Old (personal)"},{"Value":"chosen","Help":"OneDrive (personal)"}]}}`,
		`{"State":"confirm","Option":{"Name":"config_drive_ok","Type":"bool","Default":true}}`,
		`{"State":""}`,
	}
	answers := []string{"false", "onedrive", "chosen", "true"}
	calls, prompts := 0, 0
	id, err := configureDriveSelection(context.Background(), func(_ context.Context, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "config update onedrive") || !strings.Contains(joined, "config_refresh_token false") || !strings.Contains(joined, "access_scopes Files.Read offline_access") || !strings.Contains(joined, "config_is_local false") || strings.Contains(joined, "config_is_local true") {
			t.Fatalf("selection changed credentials/scopes: %v", args)
		}
		if calls > 0 && !strings.Contains(joined, "--result "+answers[calls-1]) {
			t.Fatalf("wrong selection transition: %v", args)
		}
		result := responses[calls]
		calls++
		return []byte(result), nil
	}, func(q Question) (string, error) {
		prompts++
		if q.Option.Name == "config_driveid" {
			return "chosen", nil
		}
		if q.Option.Name == "config_drive_ok" {
			return "true", nil
		}
		t.Fatalf("unexpected prompt: %s", q.Option.Name)
		return "", nil
	})
	if err != nil || id != "chosen" || calls != 5 || prompts != 1 {
		t.Fatalf("drive picker failed: id=%s err=%v calls=%d prompts=%d", id, err, calls, prompts)
	}
}

func TestDriveSelectionRejectsUnexpectedAuthAndUnlistedDrives(t *testing.T) {
	for _, question := range []string{
		`{"State":"auth","Option":{"Name":"config_is_local"}}`,
		`{"State":"drive","Option":{"Name":"config_driveid","Examples":[{"Value":"listed"}]}}`,
		`{"State":""}`,
	} {
		_, err := configureDriveSelection(context.Background(), func(context.Context, ...string) ([]byte, error) { return []byte(question), nil }, func(Question) (string, error) { return "unlisted", nil })
		if err == nil {
			t.Fatalf("invalid selection accepted: %s", question)
		}
	}
}

func TestDriveSelectionStartsEachCandidateFromOriginalTemporaryConfig(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "rclone.conf")
	original := []byte("RCLONE_ENCRYPT_V0:\noriginal")
	if err := os.WriteFile(config, original, 0600); err != nil {
		t.Fatal(err)
	}
	attempt := 0
	err := editEncryptedSourceConfig(config, func(file string) error {
		for attempt < 2 {
			if err := os.WriteFile(file, original, 0600); err != nil {
				return err
			}
			current, err := os.ReadFile(file)
			if err != nil || string(current) != string(original) {
				t.Fatalf("attempt %d reused previous candidate config: %q %v", attempt, current, err)
			}
			attempt++
			if attempt == 1 {
				if err := os.WriteFile(file, []byte("RCLONE_ENCRYPT_V0:\nfirst candidate"), 0600); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil || attempt != 2 {
		t.Fatalf("fresh candidate setup failed: attempts=%d err=%v", attempt, err)
	}
}

func TestEncryptedDriveEditPreservesOriginalOnCancelOrInvalidOutput(t *testing.T) {
	for _, mode := range []string{"cancel", "plaintext", "success"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, "rclone.conf")
			original := []byte("RCLONE_ENCRYPT_V0:\noriginal encrypted settings")
			updated := []byte("RCLONE_ENCRYPT_V0:\nupdated encrypted settings")
			if err := os.WriteFile(file, original, 0600); err != nil {
				t.Fatal(err)
			}
			err := editEncryptedSourceConfig(file, func(copy string) error {
				if copy == file {
					t.Fatal("edited active settings before validation")
				}
				data, err := os.ReadFile(copy)
				if err != nil || string(data) != string(original) {
					t.Fatalf("encrypted copy invalid: %v", err)
				}
				if mode == "cancel" {
					if err := os.WriteFile(copy, updated, 0600); err != nil {
						t.Fatal(err)
					}
					return context.Canceled
				}
				if mode == "plaintext" {
					return os.WriteFile(copy, []byte("token=plaintext"), 0600)
				}
				return os.WriteFile(copy, updated, 0600)
			})
			if mode == "success" && err != nil {
				t.Fatal(err)
			}
			if mode != "success" && err == nil {
				t.Fatal("unsafe edit committed")
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			want := original
			if mode == "success" {
				want = updated
			}
			data, readErr := os.ReadFile(file)
			if readErr != nil || string(data) != string(want) {
				t.Fatalf("active settings changed unsafely: %s %v", data, readErr)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 1 {
				t.Fatalf("temporary settings left behind: %v %v", entries, err)
			}
		})
	}
}

func TestDriveSelectionDoesNotReopenPickerAfterRootFailure(t *testing.T) {
	calls, prompts := 0, 0
	_, err := configureDriveSelection(context.Background(), func(ctx context.Context, args ...string) ([]byte, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Fatal("selection request is not bounded")
		}
		switch calls {
		case 1:
			return []byte(`{"State":"driveid_final","Option":{"Name":"config_driveid","Examples":[{"Value":"chosen"}]}}`), nil
		case 2:
			return []byte(`{"State":"choose_type","Error":"Failed to query root for drive secret-id: accessDenied secret-token","Option":null}`), nil
		default:
			return []byte(`{"State":""}`), nil
		}
	}, func(Question) (string, error) { prompts++; return "chosen", nil })
	if err == nil || calls != 2 || prompts != 1 {
		t.Fatalf("rejection silently retried: err=%v calls=%d prompts=%d", err, calls, prompts)
	}
	if !strings.Contains(err.Error(), "selected drive") || strings.Contains(err.Error(), "secret-") {
		t.Fatalf("unsafe or unhelpful error: %v", err)
	}
}

func TestDriveSelectionPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	_, err := configureDriveSelection(ctx, func(ctx context.Context, _ ...string) ([]byte, error) {
		cancel()
		<-ctx.Done()
		return nil, errors.New("rclone request failed")
	}, func(Question) (string, error) { t.Fatal("unexpected prompt"); return "", nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}
