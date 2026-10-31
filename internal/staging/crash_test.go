package staging

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This is process termination, not simulated physical power loss. The OS stays
// alive; the test verifies handle release and authenticated prefix recovery.
func TestResumeStagingAfterWriterProcessKilled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partial.stage")
	cmd := exec.Command(os.Args[0], "-test.run=^TestStagingWriterChild$")
	cmd.Env = append(os.Environ(), "OD2GP_STAGE_TEST_PATH="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if strings.TrimSpace(line) != "staged" {
			t.Fatalf("child staging failed: %q", line)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("child timed out")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	key := bytes.Repeat([]byte{3}, 32)
	data := bytes.Repeat([]byte{8}, Chunk+177)
	w, offset, err := Resume(context.Background(), path, key, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if offset != Chunk {
		w.Abort()
		t.Fatalf("resume offset %d", offset)
	}
	if _, err := w.Write(data[offset:]); err != nil {
		w.Abort()
		t.Fatal(err)
	}
	hash, err := w.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if hash != fmt.Sprintf("%x", sha256.Sum256(data)) {
		t.Fatal("recovered plaintext hash mismatch")
	}
	// Windows refuses deletion with non-sharing handles left open.
	if err := os.Remove(path); err != nil {
		t.Fatal("staging handle leaked:", err)
	}
}

func TestStagingWriterChild(t *testing.T) {
	path := os.Getenv("OD2GP_STAGE_TEST_PATH")
	if path == "" {
		return
	}
	w, err := Create(path, bytes.Repeat([]byte{3}, 32), Chunk+177)
	if err != nil {
		os.Exit(2)
	}
	if _, err = w.Write(bytes.Repeat([]byte{8}, Chunk+100)); err != nil {
		os.Exit(3)
	}
	fmt.Println("staged")
	for {
		time.Sleep(time.Second)
	}
}
