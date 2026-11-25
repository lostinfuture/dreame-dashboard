// Package logx writes one-line stdout records.
package logx

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	outMu sync.Mutex
	out   io.Writer = os.Stdout
	capMu sync.Mutex
	lvlMu sync.RWMutex
	level = "info"
)

// SetLevel accepts info or debug. Any other value stays at info.
func SetLevel(v string) {
	lvlMu.Lock()
	defer lvlMu.Unlock()
	if v == "debug" {
		level = "debug"
	} else {
		level = "info"
	}
}

// DebugEnabled reports whether debug lines are written.
func DebugEnabled() bool {
	lvlMu.RLock()
	defer lvlMu.RUnlock()
	return level == "debug"
}

// Capture sends log lines to w for the duration of fn.
// Overlapping calls run one at a time.
func Capture(w io.Writer, fn func()) {
	capMu.Lock()
	defer capMu.Unlock()
	outMu.Lock()
	old := out
	out = w
	outMu.Unlock()
	defer func() {
		outMu.Lock()
		out = old
		outMu.Unlock()
	}()
	fn()
}

// Info writes one record.
func Info(component, msg string, kv ...string) {
	write("info", component, msg, kv...)
}

// Warn writes one record at warning level.
func Warn(component, msg string, kv ...string) {
	write("warn", component, msg, kv...)
}

// Error writes one record at error level.
func Error(component, msg string, kv ...string) {
	write("error", component, msg, kv...)
}

// Debug writes one record when the level is debug.
func Debug(component, msg string, kv ...string) {
	if !DebugEnabled() {
		return
	}
	write("debug", component, msg, kv...)
}

func write(level, component, msg string, kv ...string) {
	var b strings.Builder
	fmt.Fprintf(&b, "time=%s level=%s component=%s msg=%s", time.Now().UTC().Format(time.RFC3339), level, component, escape(msg))
	for i := 0; i+1 < len(kv); i += 2 {
		val := kv[i+1]
		if secretKey(kv[i]) {
			val = "[redacted]"
		}
		fmt.Fprintf(&b, " %s=%s", kv[i], escape(val))
	}
	outMu.Lock()
	w := out
	if w == nil {
		w = os.Stdout
	}
	fmt.Fprintln(w, b.String())
	outMu.Unlock()
}

func secretKey(key string) bool {
	raw := strings.ToLower(key)
	stripped := strings.Map(func(r rune) rune {
		if r == '-' || r == '_' {
			return -1
		}
		return r
	}, raw)
	switch stripped {
	case "token", "accesstoken", "refreshtoken", "jwt", "authorization", "apikey", "openaikey", "openaiapikey":
		return true
	}
	return strings.Contains(stripped, "pass") ||
		strings.Contains(stripped, "secret") ||
		strings.Contains(stripped, "credential") ||
		strings.Contains(stripped, "privatekey") ||
		strings.Contains(stripped, "preshared") ||
		strings.Contains(stripped, "psk")
}

func escape(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if strings.ContainsAny(s, " \t\"") {
		return `"` + strings.ReplaceAll(s, `"`, `'`) + `"`
	}
	return s
}
