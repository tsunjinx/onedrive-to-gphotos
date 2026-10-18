package staging

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptedChunksSupportResumedReadsAndDetectCorruption(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	data := bytes.Repeat([]byte("private-photo"), Chunk/6)
	path := filepath.Join(t.TempDir(), "stage.bin")
	writer, err := Create(path, key, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(data[:123]); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(data[123:]); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	encrypted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("private-photo")) {
		t.Fatal("plaintext media leaked")
	}
	reader, err := Open(path, key, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	part := make([]byte, 1234)
	offset := int64(Chunk - 17)
	if _, err := reader.ReadAt(part, offset); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(part, data[offset:offset+int64(len(part))]) {
		t.Fatal("offset read differs from original")
	}
	reader.Close()
	encrypted[20] ^= 1
	if err := os.WriteFile(path, encrypted, 0600); err != nil {
		t.Fatal(err)
	}
	reader, err = Open(path, key, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := io.Copy(io.Discard, io.NewSectionReader(reader, 0, int64(len(data)))); err == nil {
		t.Fatal("corrupt chunk accepted")
	}
}
func TestShortDownloadCannotBeCommitted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stage")
	writer, err := Create(path, bytes.Repeat([]byte{1}, 32), 100)
	if err != nil {
		t.Fatal(err)
	}
	writer.Write([]byte("short"))
	if _, err := writer.Finish(); err == nil {
		t.Fatal("short source accepted")
	}
	if _, err := Open(path, bytes.Repeat([]byte{1}, 32), 100); err == nil {
		t.Fatal("partial stage accepted")
	}
}
