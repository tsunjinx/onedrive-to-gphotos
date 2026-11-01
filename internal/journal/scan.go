package journal

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

func scanKey(id string) string { h := sha256.Sum256([]byte(id)); return hex.EncodeToString(h[:]) }
func (s *Store) initScan() error {
	_, err := s.db.Exec("CREATE TABLE IF NOT EXISTS scan (id TEXT PRIMARY KEY, data BLOB NOT NULL)")
	return err
}
func (s *Store) scanSeal(id string, value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, data, []byte("scan:"+id)), nil
}

// SaveScanPage commits the page and continuation atomically. IDs are hashed;
// metadata and continuation URLs are authenticated and encrypted with the job key.
func (s *Store) SaveScanPage(state any, entries map[string]json.RawMessage) error {
	if err := s.initScan(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	save := func(id string, value any) error {
		sealed, err := s.scanSeal(id, value)
		if err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO scan(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", id, sealed)
		return err
	}
	for id, entry := range entries {
		if err := save(scanKey(id), entry); err != nil {
			return err
		}
	}
	if err := save("state", state); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) LoadScan(id string, value any) error { return s.loadScanKey(scanKey(id), value) }
func (s *Store) LoadScanState(value any) error       { return s.loadScanKey("state", value) }
func (s *Store) loadScanKey(id string, value any) error {
	if err := s.initScan(); err != nil {
		return err
	}
	var data []byte
	if err := s.db.QueryRow("SELECT data FROM scan WHERE id=?", id).Scan(&data); err != nil {
		return err
	}
	n := s.aead.NonceSize()
	if len(data) < n {
		return errors.New("damaged scan checkpoint")
	}
	plain, err := s.aead.Open(nil, data[:n], data[n:], []byte("scan:"+id))
	if err != nil {
		return errors.New("scan checkpoint authentication failed")
	}
	return json.Unmarshal(plain, value)
}
func (s *Store) ResetScan() error {
	if err := s.initScan(); err != nil {
		return err
	}
	_, err := s.db.Exec("DELETE FROM scan")
	return err
}

// WalkScan closes each bounded key-page query before callbacks perform lookups.
func (s *Store) WalkScan(visit func(json.RawMessage) error) error {
	after := ""
	for {
		rows, err := s.db.Query("SELECT id FROM scan WHERE id>? AND id!='state' ORDER BY id LIMIT 256", after)
		if err != nil {
			return err
		}
		var keys []string
		for rows.Next() {
			var key string
			if err = rows.Scan(&key); err != nil {
				rows.Close()
				return err
			}
			keys = append(keys, key)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(keys) == 0 {
			return nil
		}
		for _, key := range keys {
			var raw json.RawMessage
			if err = s.loadScanKey(key, &raw); err != nil {
				return err
			}
			if err = visit(raw); err != nil {
				return err
			}
		}
		after = keys[len(keys)-1]
	}
}
