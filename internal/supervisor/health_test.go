package supervisor

import (
	"testing"
	"time"
)

func TestHealth(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	ok := Status{At: now, HTTPOk: true, AuthState: "auth_required", Unattended: false}
	if err := Evaluate(ok, now); err != nil {
		t.Fatal(err)
	}
	offline := ok
	offline.AuthState = "ready"
	if err := Evaluate(offline, now); err != nil {
		t.Fatal(err)
	}
	stale := ok
	stale.At = now.Add(-3 * time.Minute)
	if err := Evaluate(stale, now); err == nil {
		t.Fatal("stale")
	}
	down := ok
	down.HTTPOk = false
	if err := Evaluate(down, now); err == nil {
		t.Fatal("http")
	}
	back := Status{At: now, HTTPOk: true, AuthState: "backoff", Unattended: true, AuthTroubleSince: now.Add(-16 * time.Minute)}
	if err := Evaluate(back, now); err == nil {
		t.Fatal("backoff")
	}
	back.AuthTroubleSince = now.Add(-time.Minute)
	if err := Evaluate(back, now); err != nil {
		t.Fatal(err)
	}
}
