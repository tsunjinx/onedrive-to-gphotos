package staging

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestResumeAuthenticatesPrefixDropsTornTailAndRebuildsHash(t *testing.T) {
	key := bytes.Repeat([]byte{3}, 32)
	data := bytes.Repeat([]byte{8}, 2*Chunk+177)
	name := filepath.Join(t.TempDir(), "partial")
	w, err := Create(name, key, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write(data[:Chunk+100]); err != nil {
		t.Fatal(err)
	}
	w.Abort()
	f, err := os.OpenFile(name, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("torn ciphertext"))
	f.Close()
	w, offset, err := Resume(context.Background(), name, key, int64(len(data)))
	if err != nil || offset != Chunk {
		t.Fatalf("resume offset=%d: %v", offset, err)
	}
	if _, err = w.Write(data[offset:]); err != nil {
		t.Fatal(err)
	}
	hash, err := w.Finish()
	if err != nil || hash != fmt.Sprintf("%x", sha256.Sum256(data)) {
		t.Fatalf("incorrect hash: %s %v", hash, err)
	}
	w, offset, err = Resume(context.Background(), name, key, int64(len(data)))
	if err != nil || offset != int64(len(data)) {
		t.Fatalf("finished download not recovered: %d %v", offset, err)
	}
	if got, err := w.Finish(); err != nil || got != hash {
		t.Fatal("complete hash changed", err)
	}
}
func TestResumeRejectsCorruptionAndOversizeWithoutTruncating(t *testing.T) {
	key := bytes.Repeat([]byte{3}, 32)
	for _, oversized := range []bool{false, true} {
		name := filepath.Join(t.TempDir(), "partial")
		w, err := Create(name, key, 2*Chunk)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(bytes.Repeat([]byte{8}, Chunk))
		w.Abort()
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if oversized {
			raw = bytes.Repeat([]byte{1}, int(Required(2*Chunk))+1)
		} else {
			raw[30] ^= 1
		}
		os.WriteFile(name, raw, 0600)
		if w, _, err := Resume(context.Background(), name, key, 2*Chunk); err == nil {
			w.Abort()
			t.Fatal("accepted damaged staging")
		}
		after, _ := os.ReadFile(name)
		if !bytes.Equal(raw, after) {
			t.Fatal("changed damaged evidence")
		}
	}
}
