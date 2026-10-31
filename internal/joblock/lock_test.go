package joblock

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExclusiveLockReleasesWithoutRemovingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.lock")
	release, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if unexpected, err := Acquire(path); err == nil {
		unexpected()
		release()
		t.Fatal("simultaneous lock acquired")
	}
	release()
	again, err := Acquire(path)
	if err != nil {
		t.Fatal("released lock remained blocked:", err)
	}
	again()
}

// Exercise OS lock ownership across processes, including release on forced exit.
func TestLockReleasedAfterProcessKill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockChildProcess$")
	cmd.Env = append(os.Environ(), "OD2GP_LOCK_TEST_PATH="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if strings.TrimSpace(line) != "locked" {
			t.Fatalf("child did not acquire lock: %q", line)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("child lock timed out")
	}
	if release, err := Acquire(path); err == nil {
		release()
		t.Fatal("another process acquired active lock")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	release, err := Acquire(path)
	if err != nil {
		t.Fatal("killed process retained lock:", err)
	}
	release()
}

func TestLockChildProcess(t *testing.T) {
	path := os.Getenv("OD2GP_LOCK_TEST_PATH")
	if path == "" {
		return
	}
	release, err := Acquire(path)
	if err != nil {
		os.Exit(2)
	}
	defer release()
	fmt.Println("locked")
	for {
		time.Sleep(time.Second)
	}
}
