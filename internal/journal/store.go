// Package journal persists encrypted migration records in a durable SQLite journal.
package journal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/zalando/go-keyring"
	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	aead cipher.AEAD
	Key  []byte
}

func Key(id string, create bool) ([]byte, error) {
	name := "migration-" + id
	value, err := keyring.Get("od2gp", name)
	if err == nil {
		key, err := hex.DecodeString(value)
		if err == nil && len(key) == 32 {
			return key, nil
		}
		return nil, errors.New("invalid job key")
	}
	if !create || !errors.Is(err, keyring.ErrNotFound) {
		return nil, errors.New("cannot retrieve migration key from credential store")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := keyring.Set("od2gp", name, hex.EncodeToString(key)); err != nil {
		return nil, errors.New("cannot secure migration key")
	}
	return key, nil
}
func Open(path string, key []byte, create bool) (*Store, error) {
	if create {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		file.Close()
	} else if _, err := os.Stat(path); err != nil {
		return nil, errors.New("saved job not found")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, aead: aead, Key: key}
	fail := func(err error) (*Store, error) { db.Close(); return nil, err }
	if _, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;"); err != nil {
		return fail(err)
	}
	if runtime.GOOS == "darwin" {
		if _, err = db.Exec("PRAGMA fullfsync=ON;"); err != nil {
			return fail(err)
		}
	}
	var mode string
	var sync int
	if err = db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		return fail(errors.New("journal WAL mode not applied"))
	}
	if err = db.QueryRow("PRAGMA synchronous").Scan(&sync); err != nil || sync != 2 {
		return fail(errors.New("journal full durability not applied"))
	}
	if create {
		_, err = db.Exec("CREATE TABLE records (id INTEGER PRIMARY KEY, state TEXT NOT NULL, data BLOB NOT NULL);")
	} else {
		var integrity string
		err = db.QueryRow("PRAGMA quick_check").Scan(&integrity)
		if err == nil && integrity != "ok" {
			err = errors.New("job journal integrity check failed")
		}
	}
	if err != nil {
		return fail(err)
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Save(id int64, state string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	aad := []byte(fmt.Sprintf("%d:%s", id, state))
	sealed := s.aead.Seal(nonce, nonce, data, aad)
	_, err = s.db.Exec("INSERT INTO records(id,state,data) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,data=excluded.data", id, state, sealed)
	return err
}
func (s *Store) Load(id int64, value any) error {
	var data []byte
	var state string
	if err := s.db.QueryRow("SELECT state,data FROM records WHERE id=?", id).Scan(&state, &data); err != nil {
		return err
	}
	if len(data) < s.aead.NonceSize() {
		return errors.New("damaged encrypted job record")
	}
	plain, err := s.aead.Open(nil, data[:s.aead.NonceSize()], data[s.aead.NonceSize():], []byte(fmt.Sprintf("%d:%s", id, state)))
	if err != nil {
		return errors.New("job record authentication failed; do not discard the journal")
	}
	return json.Unmarshal(plain, value)
}
func (s *Store) IDs() ([]int64, error) {
	rows, err := s.db.Query("SELECT id FROM records WHERE id>0 ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ResetInventory is allowed only by the coordinator before transfers can begin.
func (s *Store) ResetInventory() error {
	_, err := s.db.Exec("DELETE FROM records WHERE id>0")
	return err
}

// SaveBatch groups metadata-only inventory writes. Transfer checkpoints use Save.
type Record struct {
	ID    int64
	State string
	Value any
}

func (s *Store) SaveBatch(records []Record) error { return s.saveRecords(records, false) }

// SaveUpdates atomically checkpoints a group of encrypted transfer records.
func (s *Store) SaveUpdates(records []Record) error { return s.saveRecords(records, true) }
func (s *Store) saveRecords(records []Record, update bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, record := range records {
		data, err := json.Marshal(record.Value)
		if err != nil {
			return err
		}
		nonce := make([]byte, s.aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return err
		}
		sealed := s.aead.Seal(nonce, nonce, data, []byte(fmt.Sprintf("%d:%s", record.ID, record.State)))
		query := "INSERT INTO records(id,state,data) VALUES(?,?,?)"
		if update {
			query += " ON CONFLICT(id) DO UPDATE SET state=excluded.state,data=excluded.data"
		}
		if _, err := tx.Exec(query, record.ID, record.State, sealed); err != nil {
			return err
		}
	}
	return tx.Commit()
}
