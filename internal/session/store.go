// Package session persists an imported YouTube Music session between runs.
//
// Only the cookie header plus a little display metadata is stored: SID hashes
// are derived at load time by ytm.ParseToken, so the on-disk format stays
// stable if the hashing scheme changes.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go-chordly/internal/ytm"
)

var (
	// ErrNotFound is returned by Load when no usable session is stored.
	ErrNotFound = errors.New("session: no saved session")
	// ErrNoPath is returned when the store has no filesystem location.
	ErrNoPath = errors.New("session: no store path configured")
)

// Store reads and writes the saved session. It is safe for concurrent use.
type Store struct {
	path string
	mu   sync.Mutex
}

// persisted is the on-disk schema. It deliberately excludes the raw cookie
// map, Authorization header, and any other derived credential material.
type persisted struct {
	Cookie      string          `json:"cookie,omitempty"`
	SAPISID     string          `json:"sapisid,omitempty"`
	VisitorData string          `json:"visitor_data,omitempty"`
	AccountName string          `json:"account_name,omitempty"`
	Source      ytm.TokenSource `json:"source,omitempty"`
}

// DefaultPath returns $XDG_CONFIG_HOME/chordly/session.json (or the platform
// equivalent), or "" when the config directory cannot be determined.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "chordly", "session.json")
}

// Open returns a store backed by path. An empty path makes Save/Clear fail and
// Load report ErrNotFound, which keeps persistence optional in tests.
func Open(path string) *Store {
	return &Store{path: path}
}

// Path reports the backing file path.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Save atomically writes bundle to disk with owner-only permissions.
func (s *Store) Save(bundle *ytm.TokenBundle) error {
	if s == nil || s.path == "" {
		return ErrNoPath
	}
	if bundle == nil {
		return s.Clear()
	}

	record := persisted{
		Cookie:      bundle.Cookie,
		SAPISID:     bundle.SAPISID,
		VisitorData: bundle.VisitorData,
		AccountName: bundle.AccountName,
		Source:      bundle.Source,
	}
	if strings.TrimSpace(record.Cookie) == "" && strings.TrimSpace(record.SAPISID) == "" {
		// Nothing usable to restore.
		return s.Clear()
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("session: encode: %w", err)
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("session: create config dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return fmt.Errorf("session: create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("session: set permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("session: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("session: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("session: close: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("session: replace: %w", err)
	}
	return nil
}

// Load reads the saved session and rebuilds a validated TokenBundle.
func (s *Store) Load() (*ytm.TokenBundle, error) {
	if s == nil || s.path == "" {
		return nil, ErrNotFound
	}

	s.mu.Lock()
	data, err := os.ReadFile(s.path)
	s.mu.Unlock()

	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("session: read: %w", err)
	}

	var record persisted
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("session: decode: %w", err)
	}
	if strings.TrimSpace(record.Cookie) == "" && strings.TrimSpace(record.SAPISID) == "" {
		return nil, ErrNotFound
	}

	bundle := &ytm.TokenBundle{
		Cookie:      record.Cookie,
		SAPISID:     record.SAPISID,
		VisitorData: record.VisitorData,
		AccountName: record.AccountName,
		Source:      record.Source,
	}

	// Re-parse the cookie header so cookie maps and SID hashes are derived by
	// the same code path as a fresh import.
	if record.Cookie != "" {
		if parsed, parseErr := ytm.ParseToken([]byte(record.Cookie)); parseErr == nil {
			bundle = parsed
			bundle.VisitorData = record.VisitorData
			bundle.AccountName = record.AccountName
			if record.Source != "" {
				bundle.Source = record.Source
			}
		}
	}

	return bundle, nil
}

// Clear removes the saved session. A missing file is not an error.
func (s *Store) Clear() error {
	if s == nil || s.path == "" {
		return ErrNoPath
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("session: clear: %w", err)
	}
	return nil
}
