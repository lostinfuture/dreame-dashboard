// Package flock takes an exclusive non-blocking lock on /data/.lock.
package flock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ErrHeld is returned when another process already holds the lock.
var ErrHeld = errors.New("data directory is locked")

// File is an open lock file. Closing it releases the lock.
type File struct {
	f *os.File
}

// Acquire creates path if needed and takes LOCK_EX|LOCK_NB.
func Acquire(path string) (*File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		f.Close()
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("%w", ErrHeld)
		}
		return nil, err
	}
	return &File{f: f}, nil
}

// Release unlocks and closes the file.
func (f *File) Release() error {
	if f == nil || f.f == nil {
		return nil
	}
	err := syscall.Flock(int(f.f.Fd()), syscall.LOCK_UN)
	cerr := f.f.Close()
	f.f = nil
	if err != nil {
		return err
	}
	return cerr
}
