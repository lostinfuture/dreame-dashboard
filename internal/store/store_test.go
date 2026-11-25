package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func f64(v float64) *float64 { return &v }
func i64(v int) *int         { return &v }

func TestCorruptStatePreservesHistory(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Lock()
	s.State().Status = "online"
	if err := s.SaveState(); err != nil {
		t.Fatal(err)
	}
	tok := filepath.Join(dir, "tokens.json")
	if err := os.WriteFile(tok, []byte("{\"access_token\":\"keep\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendReading(Reading{TS: time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC).Unix(), PM25: f64(4)}); err != nil {
		t.Fatal(err)
	}
	s.Unlock()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s2.State().Status != "boot" {
		t.Fatal(s2.State().Status)
	}
	b, _ := os.ReadFile(tok)
	if string(b) != "{\"access_token\":\"keep\"}\n" {
		t.Fatal(string(b))
	}
	pts := s2.History(time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC), "6h", "pm25", 90*time.Second)
	if len(pts) != 1 || pts[0].V != 4 {
		t.Fatal(pts)
	}
}

func TestAtomicRewrite(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Lock()
	s.State().Device.DID = "-100000001"
	if err := s.SaveState(); err != nil {
		t.Fatal(err)
	}
	s.Unlock()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if len(e.Name()) >= 4 && e.Name()[:4] == ".tmp" {
			t.Fatal("temp left behind", e.Name())
		}
	}
	s2, _ := Open(dir)
	if s2.State().Device.DID != "-100000001" {
		t.Fatal(s2.State().Device.DID)
	}
}

func TestGapAndDownsample(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	base := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	s.Lock()
	for i := 0; i < 1000; i++ {
		ts := base.Add(time.Duration(i) * time.Second)
		v := float64(i % 7)
		if err := s.AppendReading(Reading{TS: ts.Unix(), PM25: f64(v)}); err != nil {
			t.Fatal(err)
		}
	}
	// A later point far enough to be a gap after downsampling is asserted on a short series.
	if err := s.AppendReading(Reading{TS: base.Add(30 * time.Minute).Unix(), PM25: f64(9)}); err != nil {
		t.Fatal(err)
	}
	s.Unlock()
	pts := s.History(base.Add(31*time.Minute), "6h", "pm25", 3*time.Second)
	if len(pts) == 0 || len(pts) > 360 {
		t.Fatalf("len %d", len(pts))
	}
	gaps := 0
	for i, p := range pts {
		if p.V < 0 {
			t.Fatal(p.V)
		}
		if p.Gap {
			gaps++
			if i == 0 {
				t.Fatal("leading gap")
			}
			continue
		}
	}
	if gaps != 1 {
		t.Fatalf("gaps %d len %d", gaps, len(pts))
	}
	day := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 24*120; i++ {
		ts := day.Add(time.Duration(i) * 30 * time.Second)
		v := float64(i % 5)
		if err := s.AppendReading(Reading{TS: ts.Unix(), PM25: f64(v)}); err != nil {
			t.Fatal(err)
		}
	}
	dense := s.History(day.Add(24*time.Hour), "24h", "pm25", 90*time.Second)
	if len(dense) < 2 {
		t.Fatal(len(dense))
	}
	for _, p := range dense {
		if p.Gap {
			t.Fatal("24h contiguous samples marked as a gap")
		}
	}
}

func TestBackfillAcrossMidnightAndProvisional(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.Lock()
	defer s.Unlock()
	a := time.Date(2026, 11, 21, 23, 30, 0, 0, time.UTC)
	b := time.Date(2026, 11, 22, 0, 30, 0, 0, time.UTC)
	c := time.Date(2026, 11, 22, 2, 15, 0, 0, time.UTC)
	for _, row := range []Reading{
		{TS: a.Unix(), PM25: f64(3)},
		{TS: b.Unix(), PM25: f64(5)},
		{TS: c.Unix(), PM25: f64(9)},
	} {
		if err := s.AppendReading(row); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 11, 22, 2, 45, 0, 0, time.UTC)
	if err := s.Backfill(now); err != nil {
		t.Fatal(err)
	}
	rows, err := s.loadRollups()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("hours %+v", rows)
	}
	if rows[0].Hour != "2026-11-21T23:00:00Z" || rows[1].N != 1 {
		t.Fatalf("%+v %+v", rows[0], rows[1])
	}
	pts := s.History(now, "7d", "pm25", time.Hour)
	if len(pts) < 2 {
		t.Fatal(pts)
	}
	last := pts[len(pts)-1]
	if !last.TS.Equal(now.Truncate(time.Hour)) || last.V != 9 {
		t.Fatalf("provisional %+v", last)
	}
	// Second backfill is a no-op.
	if err := s.Backfill(now); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.loadRollups()
	if len(rows) != 2 {
		t.Fatal(len(rows))
	}
}

func TestRetention(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.Lock()
	defer s.Unlock()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	old := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	if err := s.AppendReading(Reading{TS: old.Unix(), PM25: f64(1)}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendReading(Reading{TS: now.Add(-time.Hour).Unix(), PM25: f64(2)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Backfill(now); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendCommand(CommandRecord{TS: now.AddDate(0, 0, -200).Unix(), Target: "mode", Detail: "old", Result: "OK"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent(EventRecord{TS: now.Unix(), Kind: "auth", Detail: "login"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Retain(now, 90); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "readings", "2026-11-18.jsonl")); !os.IsNotExist(err) {
		t.Fatal("old reading kept", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "readings", "2026-11-22.jsonl")); err != nil {
		t.Fatal(err)
	}
	acts := s.ActivitySince(now.AddDate(0, 0, -180))
	if len(acts) != 1 || acts[0].Kind != "auth" {
		t.Fatalf("%+v", acts)
	}
}

func TestHistoryMultiSinglePass(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 11, 22, 11, 30, 0, 0, time.UTC)
	for _, r := range []Reading{
		{TS: t0.Unix(), PM25: f64(4), PM10: f64(7), PM1: f64(2), T: f64(22.5), RH: f64(44), HCHO: f64(1), TVOC: f64(310)},
		{TS: t1.Unix(), PM25: f64(6), PM10: f64(9), PM1: f64(3), T: f64(23.0), RH: f64(46), HCHO: f64(2), TVOC: f64(350)},
	} {
		if err := s.AppendReading(r); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 11, 22, 11, 45, 0, 0, time.UTC)
	if err := s.Backfill(now); err != nil {
		t.Fatal(err)
	}
	metrics := []string{"pm25", "pm10", "pm1", "t", "rh", "hcho", "tvoc"}
	for _, span := range []string{"6h", "7d"} {
		got := s.HistoryMulti(now, span, metrics, 2*time.Hour)
		for _, m := range metrics {
			want := s.History(now, span, m, 2*time.Hour)
			if len(got[m]) != len(want) || len(got[m]) != 2 {
				t.Fatalf("span=%s metric=%s got=%d want=%d", span, m, len(got[m]), len(want))
			}
			if got[m][0].V != want[0].V || got[m][1].V != want[1].V {
				t.Fatalf("span=%s metric=%s mismatch %+v vs %+v", span, m, got[m], want)
			}
		}
	}
}

func TestStandbyZerosFilteredFromHistory(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	for _, r := range []Reading{
		{TS: t0.Unix(), Pwr: i64(1), PM25: f64(2), T: f64(25), RH: f64(42), HCHO: f64(41), TVOC: f64(150)},
		{TS: t0.Add(30 * time.Second).Unix(), Pwr: i64(2), PM25: f64(0), T: f64(0), RH: f64(0), HCHO: f64(0), TVOC: f64(160)},
		{TS: t0.Add(3 * time.Minute).Unix(), Pwr: i64(1), PM25: f64(0), T: f64(0), RH: f64(0), HCHO: f64(0), TVOC: f64(165)},
		{TS: t0.Add(4 * time.Minute).Unix(), Pwr: i64(1), PM25: f64(2), T: f64(25), RH: f64(41), HCHO: f64(46), TVOC: f64(170)},
	} {
		if err := s.AppendReading(r); err != nil {
			t.Fatal(err)
		}
	}
	got := s.HistoryMulti(t0.Add(5*time.Minute), "6h", []string{"t", "rh", "hcho", "pm25", "tvoc"}, 90*time.Second)
	for _, m := range []string{"t", "rh", "hcho", "pm25"} {
		if len(got[m]) != 2 || got[m][0].V == 0 || got[m][1].V == 0 || !got[m][1].Gap {
			t.Fatalf("metric %s expected 2 non-zero points with standby gap, got %+v", m, got[m])
		}
	}
	if len(got["tvoc"]) != 4 {
		t.Fatalf("tvoc should stay live during standby, got %d points", len(got["tvoc"]))
	}
}

func TestFiveMinuteChartGranularity(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	// Write 40 readings at 30s intervals (20 minutes -> 4 exact 5-minute buckets).
	for i := 0; i < 40; i++ {
		v := float64((i / 10) + 1)
		if err := s.AppendReading(Reading{TS: base.Add(time.Duration(i) * 30 * time.Second).Unix(), PM25: f64(v)}); err != nil {
			t.Fatal(err)
		}
	}
	pts := s.History(base.Add(21*time.Minute), "6h", "pm25", 90*time.Second)
	if len(pts) != 4 {
		t.Fatalf("expected 4 five-minute buckets over 20m, got %d: %+v", len(pts), pts)
	}
	for i, want := range []float64{1, 2, 3, 4} {
		if pts[i].V != want {
			t.Fatalf("bucket %d avg=%v want %v", i, pts[i].V, want)
		}
	}
}
