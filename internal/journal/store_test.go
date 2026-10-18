package journal

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestJournalReopenEncryptsRecordsAndRejectsWrongKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.db")
	key := bytes.Repeat([]byte{2}, 32)
	store, err := Open(path, key, true)
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]string{"path": "secret-album/photo.jpg", "session": "secret-upload-session"}
	if err := store.Save(1, "uploading", value); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("secret-album")) || bytes.Contains(data, []byte("secret-upload")) {
		t.Fatal("sensitive journal content stored in plaintext")
	}
	store, err = Open(path, key, false)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := store.Load(1, &got); err != nil || got["path"] != value["path"] {
		t.Fatalf("saved progress lost: %v", err)
	}
	store.Close()
	wrong, err := Open(path, bytes.Repeat([]byte{3}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.Close()
	if err := wrong.Load(1, &got); err == nil {
		t.Fatal("wrong credential key accepted")
	}
}

func TestJournalSurvivesAbruptProcessExit(t *testing.T) {
	if path := os.Getenv("OD2GP_CRASH_JOURNAL"); path != "" {
		store, err := Open(path, bytes.Repeat([]byte{6}, 32), true)
		if err != nil {
			os.Exit(10)
		}
		if err := store.Save(1, "completed", map[string]string{"media": "durably-created"}); err != nil {
			os.Exit(11)
		}
		// Deliberately bypass Close/checkpoint to leave the WAL as after a crash.
		os.Exit(17)
	}
	path := filepath.Join(t.TempDir(), "crash.db")
	command := exec.Command(os.Args[0], "-test.run=^TestJournalSurvivesAbruptProcessExit$")
	command.Env = append(os.Environ(), "OD2GP_CRASH_JOURNAL="+path)
	err := command.Run()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 17 {
		t.Fatalf("child did not reach committed checkpoint: %v", err)
	}
	recovered, err := Open(path, bytes.Repeat([]byte{6}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	var value map[string]string
	if err := recovered.Load(1, &value); err != nil || value["media"] != "durably-created" {
		t.Fatalf("crash lost committed state: %v", err)
	}
}
