package flock

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestExclusive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".lock")
	a, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	_, err = Acquire(path)
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("second acquire: %v", err)
	}
	if err := a.Release(); err != nil {
		t.Fatal(err)
	}
	b, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	b.Release()
}
