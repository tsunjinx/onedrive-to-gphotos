package staging

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"errors"
	"io"
	"os"
)

// Resume authenticates complete encrypted records, reconstructs the plaintext
// hash, and discards only an incomplete trailing record. Nothing is uploaded
// until the completed file and its source version have both been verified.
func Resume(ctx context.Context, path string, key []byte, limit int64) (*Writer, int64, error) {
	if limit <= 0 {
		return nil, 0, errors.New("invalid staging size")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		w, err := Create(path, key, limit)
		return w, 0, err
	}
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > Required(limit) {
		return nil, 0, errors.New("retained staging is not a valid regular file")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, 0, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, 0, err
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return nil, 0, err
	}
	fail := func(err error) (*Writer, int64, error) { file.Close(); return nil, 0, err }
	w := &Writer{file: file, aead: aead, hash: sha256.New(), limit: limit}
	reader := &Reader{file: file, aead: aead, size: limit}
	buffer := make([]byte, Chunk)
	var end int64
	for w.size < limit {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		length := min(int64(Chunk), limit-w.size)
		if info.Size()-end < length+overhead {
			break
		}
		if _, err := reader.ReadAt(buffer[:length], w.size); err != nil {
			return fail(errors.New("retained staging chunk authentication failed; refusing download resume"))
		}
		w.hash.Write(buffer[:length])
		w.size += length
		w.index++
		end += length + overhead
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := file.Truncate(end); err != nil {
		return fail(err)
	}
	if _, err := file.Seek(end, io.SeekStart); err != nil {
		return fail(err)
	}
	return w, w.size, nil
}
