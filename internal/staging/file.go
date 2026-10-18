// Package staging holds authenticated encrypted file chunks with bounded buffers.
package staging

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
)

const Chunk = 4 << 20
const overhead = 12 + 16

type Writer struct {
	file               *os.File
	aead               cipher.AEAD
	buffer             []byte
	hash               hash.Hash
	size, limit, index int64
}

func Create(path string, key []byte, limit int64) (*Writer, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	return &Writer{file: file, aead: aead, hash: sha256.New(), limit: limit}, nil
}
func (w *Writer) Write(p []byte) (int, error) {
	if int64(len(p))+w.size > w.limit {
		return 0, errors.New("source exceeds expected file size")
	}
	original := len(p)
	w.hash.Write(p)
	w.size += int64(len(p))
	for len(p) > 0 {
		n := min(len(p), Chunk-len(w.buffer))
		w.buffer = append(w.buffer, p[:n]...)
		p = p[n:]
		if len(w.buffer) == Chunk {
			if err := w.flush(); err != nil {
				return 0, err
			}
		}
	}
	return original, nil
}
func (w *Writer) flush() error {
	if len(w.buffer) == 0 {
		return nil
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	var aad [8]byte
	binary.BigEndian.PutUint64(aad[:], uint64(w.index))
	sealed := w.aead.Seal(nonce, nonce, w.buffer, aad[:])
	if _, err := w.file.Write(sealed); err != nil {
		return err
	}
	w.index++
	w.buffer = w.buffer[:0]
	return nil
}
func (w *Writer) Finish() (string, error) {
	if w.size != w.limit {
		w.file.Close()
		return "", errors.New("source download length mismatch")
	}
	if err := w.flush(); err != nil {
		w.file.Close()
		return "", err
	}
	err := w.file.Sync()
	closeErr := w.file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	return hex.EncodeToString(w.hash.Sum(nil)), nil
}
func (w *Writer) Abort() { w.file.Close() }

type Reader struct {
	file *os.File
	aead cipher.AEAD
	size int64
}

func Open(path string, key []byte, size int64) (*Reader, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || info.Size() != size+((size+Chunk-1)/Chunk)*overhead {
		file.Close()
		return nil, errors.New("staging file is incomplete or damaged")
	}
	return &Reader{file: file, aead: aead, size: size}, nil
}
func (r *Reader) Close() error { return r.file.Close() }
func (r *Reader) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, errors.New("invalid staging offset")
	}
	total := 0
	for len(p) > 0 {
		if offset >= r.size {
			return total, io.EOF
		}
		index := offset / Chunk
		within := offset % Chunk
		length := min(int64(Chunk), r.size-index*Chunk)
		sealed := make([]byte, length+overhead)
		if _, err := r.file.ReadAt(sealed, index*(Chunk+overhead)); err != nil {
			return total, err
		}
		var aad [8]byte
		binary.BigEndian.PutUint64(aad[:], uint64(index))
		plain, err := r.aead.Open(nil, sealed[:12], sealed[12:], aad[:])
		if err != nil {
			return total, errors.New("staging chunk authentication failed")
		}
		n := copy(p, plain[within:])
		p = p[n:]
		offset += int64(n)
		total += n
	}
	return total, nil
}
func Required(size int64) int64 { return size + ((size+Chunk-1)/Chunk)*overhead }
