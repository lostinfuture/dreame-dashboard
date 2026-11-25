package supervisor

import (
	"context"
	"strings"
	"testing"
	"time"

	"dreame-dashboard/internal/config"
	"dreame-dashboard/internal/dreame"
	"dreame-dashboard/internal/store"
	"dreame-dashboard/internal/web"
)

func TestJitterAndOfflineWait(t *testing.T) {
	base := 30 * time.Second
	lo := Jitter(base, 0)
	hi := Jitter(base, 1)
	if lo < 26*time.Second || lo > 28*time.Second || hi < 32*time.Second || hi > 34*time.Second {
		t.Fatal(lo, hi)
	}
	if NextWait(base, 4) != base || NextWait(base, 5) != time.Minute {
		t.Fatal(NextWait(base, 5))
	}
	if NextWait(3*time.Minute, 9) != 5*time.Minute {
		t.Fatal(NextWait(3*time.Minute, 9))
	}
}

func TestReadOnlyBeatsSaved(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	off := false
	st.Lock()
	st.State().Overrides.ReadOnly = &off
	st.State().Status = "online"
	st.Unlock()
	cfg := config.Config{DataDir: dir, ReadOnly: true, PollInterval: 30 * time.Second, TempUnit: "C", Region: "us", RunDir: dir}
	auth := dreame.NewManager("")
	auth.Password = "x"
	app := NewApp(cfg, st, auth, nil)
	app.booted.Store(true)
	snap := app.Page("device", nil)
	if !snap.ReadOnly || !snap.ReadOnlyLocked || snap.ReadOnlySource != "env" {
		t.Fatalf("%v %v %s", snap.ReadOnly, snap.ReadOnlyLocked, snap.ReadOnlySource)
	}
	body, err := web.RenderHTML(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Locked to read-only by DMD_READ_ONLY=true.") {
		t.Fatal("missing lock note")
	}
	if err := app.Settings(context.Background(), web.SettingsForm{Poll: "45"}); err != nil {
		t.Fatal(err)
	}
	if app.pollInterval() != 45*time.Second {
		t.Fatal(app.pollInterval())
	}
	if err := app.Settings(context.Background(), web.SettingsForm{Reset: true}); err != nil {
		t.Fatal(err)
	}
	if app.pollInterval() != 30*time.Second {
		t.Fatal(app.pollInterval())
	}
}

func TestPickOnePurifier(t *testing.T) {
	d, err := pickDevice([]dreame.Device{{DID: "1", Model: "dreame.airp.u2402"}, {DID: "2", Model: "vacuum"}}, "")
	if err != nil || d.DID != "1" {
		t.Fatal(d, err)
	}
	if _, err := pickDevice([]dreame.Device{{DID: "1"}, {DID: "2"}}, ""); err == nil {
		t.Fatal("expected did required")
	}
}
