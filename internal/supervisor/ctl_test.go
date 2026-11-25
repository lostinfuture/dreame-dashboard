package supervisor

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCtl(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ctl.sock")
	if _, err := CallCtl(path, "verify-map"); !errors.Is(err, ErrCtlUnavailable) {
		t.Fatalf("missing socket err = %v, want ErrCtlUnavailable", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- ServeCtl(ctx, path, func(verb string) (string, error) {
			if verb == "poll-now" {
				return "polled", nil
			}
			return "", errString("verify failed")
		})
	}()
	deadline := time.Now().Add(2 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		msg, err := CallCtl(path, "poll-now")
		if err == nil {
			if msg != "polled" {
				t.Fatal(msg)
			}
			if _, rerr := CallCtl(path, "verify-map"); rerr == nil || errors.Is(rerr, ErrCtlUnavailable) || rerr.Error() != "verify failed" {
				t.Fatalf("remote error = %v", rerr)
			}
			cancel()
			<-done
			return
		}
		last = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(last)
}

type errString string

func (e errString) Error() string { return string(e) }
