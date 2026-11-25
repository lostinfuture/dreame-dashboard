package logx

import (
	"bytes"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	stdout := capture(t, func() {
		Info("dmd", "loaded",
			"privatekey", "hidden-private",
			"private_key", "hidden-private-2",
			"preshared", "hidden-psk",
			"psk", "hidden-short",
			"password", "hidden-pass",
			"token", "hidden-token",
			"access_token", "hidden-access",
			"refresh_token", "hidden-refresh",
			"jwt", "hidden-jwt",
			"authorization", "hidden-authz",
			"credential", "hidden-cred",
			"secret", "hidden-secret",
			"auth_state", "ready",
			"token_tail", "abcd",
			"image", "alpine:3.24",
		)
	})
	for _, leak := range []string{
		"hidden-private", "hidden-private-2", "hidden-psk", "hidden-short",
		"hidden-pass", "hidden-token", "hidden-access", "hidden-refresh",
		"hidden-jwt", "hidden-authz", "hidden-cred", "hidden-secret",
	} {
		if strings.Contains(stdout, leak) {
			t.Fatalf("log leaked %q in %s", leak, stdout)
		}
	}
	if !strings.Contains(stdout, "auth_state=ready") {
		t.Fatal(stdout)
	}
	if !strings.Contains(stdout, "token_tail=abcd") {
		t.Fatal(stdout)
	}
	if !strings.Contains(stdout, "image=alpine:3.24") {
		t.Fatal(stdout)
	}
	if strings.Count(stdout, "[redacted]") != 12 {
		t.Fatal(stdout)
	}
}

func TestEscapeQuotes(t *testing.T) {
	stdout := capture(t, func() {
		Warn("dmd", "has space", "note", `say "hi"`)
	})
	if !strings.Contains(stdout, `msg="has space"`) || !strings.Contains(stdout, "note=\"say 'hi'\"") {
		t.Fatal(stdout)
	}
}

func TestDebugGated(t *testing.T) {
	SetLevel("info")
	stdout := capture(t, func() {
		Debug("auth", "skip", "path", "/token")
	})
	if stdout != "" {
		t.Fatal(stdout)
	}
	SetLevel("debug")
	stdout = capture(t, func() {
		Debug("auth", "skip", "path", "/token")
	})
	if !strings.Contains(stdout, "level=debug") || !strings.Contains(stdout, "path=/token") {
		t.Fatal(stdout)
	}
	SetLevel("info")
}

func capture(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	Capture(&buf, fn)
	return buf.String()
}
