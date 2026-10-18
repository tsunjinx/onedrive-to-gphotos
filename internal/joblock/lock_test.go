package joblock

import (
	"path/filepath"
	"testing"
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
