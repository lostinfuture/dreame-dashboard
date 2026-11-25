// Package store persists dashboard state as atomic files under /data.
package store

import (
	"os"
	"path/filepath"
	"sync"

	"dreame-dashboard/internal/filex"
)

// Store is the /data directory.
type Store struct {
	mu     sync.Mutex
	fileMu sync.Mutex
	root   string
	state  *State
}

// Open creates the data directories and loads state.json.
// A missing or invalid state file is rebuilt. Other files are left in place.
func Open(root string) (*Store, error) {
	s := &Store{root: root}
	for _, d := range []string{root, filepath.Join(root, "readings")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
		if err := os.Chmod(d, 0o700); err != nil {
			return nil, err
		}
	}
	s.loadState()
	return s, nil
}

// Lock guards mutations.
func (s *Store) Lock() { s.mu.Lock() }

// Unlock releases Lock.
func (s *Store) Unlock() { s.mu.Unlock() }

func (s *Store) path(name string) string { return filepath.Join(s.root, name) }

func writeAtomic(path string, b []byte) error { return filex.WriteAtomic(path, b) }
