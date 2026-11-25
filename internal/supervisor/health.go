package supervisor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"dreame-dashboard/internal/filex"
)

// Status is the file dmd healthcheck reads.
type Status struct {
	At               time.Time `json:"at"`
	HTTPOk           bool      `json:"http_ok"`
	AuthState        string    `json:"auth_state"`
	Unattended       bool      `json:"unattended"`
	AuthTroubleSince time.Time `json:"auth_trouble_since,omitempty"`
}

// WriteStatus writes the health file atomically.
func WriteStatus(path string, st Status) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return filex.WriteAtomic(path, b)
}

// ReadStatus loads the health file.
func ReadStatus(path string) (Status, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Status{}, err
	}
	var st Status
	if err := json.Unmarshal(b, &st); err != nil {
		return Status{}, err
	}
	return st, nil
}

// Evaluate decides the healthcheck exit.
// Device offline and interactive AuthRequired stay healthy.
// Unattended auth stuck in backoff for more than 15 minutes fails.
func Evaluate(st Status, now time.Time) error {
	if st.At.IsZero() || now.Sub(st.At) > 120*time.Second {
		return errors.New("status stale")
	}
	if !st.HTTPOk {
		return errors.New("http")
	}
	if st.Unattended && st.AuthState == "backoff" && !st.AuthTroubleSince.IsZero() && now.Sub(st.AuthTroubleSince) > 15*time.Minute {
		return errors.New("auth backoff")
	}
	return nil
}
