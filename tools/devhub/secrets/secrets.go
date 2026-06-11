// Package secrets is the DevHub's encrypted-at-rest secrets store (S47b).
// Server admin keys live age-encrypted on the DevHub box and are decrypted
// ONLY into this process' memory:
//
//	<dir>/age.key      X25519 identity (0600), generated once — never leaves the box
//	<dir>/secrets.age  age-encrypted JSON {"<server_id>": {"admin_key": "..."}}
//
// Invariants: plaintext never touches disk, SQLite, API responses or logs.
// Every Get for an operation is reported through the Audit callback — the
// audit records USAGE, never values. Writes re-encrypt the full set and land
// atomically (tmp + rename, 0600).
package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"filippo.io/age"
)

const (
	keyFile     = "age.key"
	secretsFile = "secrets.age"
)

// AuditFn records that a secret was used (serverID + operation, never the
// value). May be nil.
type AuditFn func(serverID, operation string)

// Store holds the decrypted secrets in memory; the encrypted file is the
// source of truth after every Set/Delete.
type Store struct {
	mu        sync.Mutex
	dir       string
	identity  *age.X25519Identity
	recipient *age.X25519Recipient
	data      map[string]map[string]string // serverID -> key -> value

	// Audit is invoked on every successful Get. Set it right after Open.
	Audit AuditFn
}

// Open loads (or creates, on first run) the age identity at dir/age.key and
// decrypts dir/secrets.age into memory. A missing secrets file is an empty
// store, not an error.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secrets dir: %w", err)
	}
	s := &Store{dir: dir, data: map[string]map[string]string{}}
	if err := s.loadOrCreateIdentity(); err != nil {
		return nil, err
	}
	if err := s.loadFile(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) loadOrCreateIdentity() error {
	path := filepath.Join(s.dir, keyFile)
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		id, err := age.GenerateX25519Identity()
		if err != nil {
			return fmt.Errorf("generate age identity: %w", err)
		}
		body := fmt.Sprintf("# devhub secrets store identity — BACK THIS FILE UP; without it secrets.age is unrecoverable\n# created: %s\n# public key: %s\n%s\n",
			time.Now().UTC().Format(time.RFC3339), id.Recipient(), id)
		if err := atomicWrite(path, []byte(body)); err != nil {
			return fmt.Errorf("write age identity: %w", err)
		}
		log.Printf("secrets: NEW age identity generated at %s — back it up (without it the store is unrecoverable)", path)
		s.identity = id
	case err != nil:
		return fmt.Errorf("read age identity: %w", err)
	default:
		ids, err := age.ParseIdentities(bytes.NewReader(raw))
		if err != nil || len(ids) == 0 {
			return fmt.Errorf("parse age identity %s: %w", path, err)
		}
		x, ok := ids[0].(*age.X25519Identity)
		if !ok {
			return fmt.Errorf("identity in %s is not X25519", path)
		}
		s.identity = x
	}
	s.recipient = s.identity.Recipient()
	return nil
}

func (s *Store) loadFile() error {
	path := filepath.Join(s.dir, secretsFile)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil // empty store
	}
	if err != nil {
		return fmt.Errorf("open secrets file: %w", err)
	}
	defer f.Close() //nolint:errcheck
	r, err := age.Decrypt(f, s.identity)
	if err != nil {
		return fmt.Errorf("decrypt secrets file (wrong/replaced identity?): %w", err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return fmt.Errorf("read decrypted secrets: %w", err)
	}
	if err := json.Unmarshal(buf.Bytes(), &s.data); err != nil {
		return fmt.Errorf("parse decrypted secrets: %w", err)
	}
	return nil
}

// persist re-encrypts the whole set and writes it atomically. Caller holds mu.
func (s *Store) persist() error {
	plain, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	var ct bytes.Buffer
	w, err := age.Encrypt(&ct, s.recipient)
	if err != nil {
		return fmt.Errorf("age encrypt: %w", err)
	}
	if _, err := w.Write(plain); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.dir, secretsFile), ct.Bytes())
}

// atomicWrite writes content to a same-directory temp file (0600) and renames
// it over path, so readers never observe a partial file.
func atomicWrite(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) //nolint:errcheck // no-op after successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Get returns a secret and reports the usage (operation) to the Audit
// callback. The operation names the caller's intent ('metrics_scrape', ...).
func (s *Store) Get(serverID, key, operation string) (string, bool) {
	s.mu.Lock()
	v, ok := s.data[serverID][key]
	audit := s.Audit
	s.mu.Unlock()
	if ok && audit != nil {
		audit(serverID, operation)
	}
	return v, ok
}

// Has reports existence without touching the audit trail (status badges).
func (s *Store) Has(serverID, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.data[serverID][key]
	return ok
}

// Set stores a secret and re-encrypts the file.
func (s *Store) Set(serverID, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data[serverID] == nil {
		s.data[serverID] = map[string]string{}
	}
	s.data[serverID][key] = value
	return s.persist()
}

// Delete removes ALL secrets of a server (it was deleted from the registry).
func (s *Store) Delete(serverID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[serverID]; !ok {
		return nil
	}
	delete(s.data, serverID)
	return s.persist()
}

// DeleteKey removes one secret of a server (rotation/cleanup).
func (s *Store) DeleteKey(serverID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[serverID][key]; !ok {
		return nil
	}
	delete(s.data[serverID], key)
	if len(s.data[serverID]) == 0 {
		delete(s.data, serverID)
	}
	return s.persist()
}
