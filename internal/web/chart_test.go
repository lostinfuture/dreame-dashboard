package web

import "testing"

func TestMonotoneDoesNotOvershootBelowZero(t *testing.T) {
	xs := []float64{0, 1, 2, 3, 4}
	ys := []float64{0, 8, 0, 4, 0}
	got := SampleMonotone(xs, ys, 20)
	for i := 0; i < len(xs)-1; i++ {
		lo, hi := ys[i], ys[i+1]
		if lo > hi {
			lo, hi = hi, lo
		}
		for s := 0; s < 20; s++ {
			y := got[i*20+s]
			if y < lo-0.001 || y > hi+0.001 {
				t.Fatalf("segment %d sample %d y=%v outside [%v,%v]", i, s, y, lo, hi)
			}
		}
	}
}

func TestGapSplit(t *testing.T) {
	paths := SplitPaths([]Pt{{X: 0, Y: 1}, {X: 1, Y: 2}, {X: 4, Y: 2, Gap: true}, {X: 5, Y: 3}})
	if len(paths) != 2 {
		t.Fatalf("%d paths: %v", len(paths), paths)
	}
	if paths[0] == "" || paths[1] == "" {
		t.Fatal(paths)
	}
}

func TestBandsZoneAndBounds(t *testing.T) {
	cases := []struct {
		id, unit string
		v        float64
		want     string
	}{
		{"pm25", "µg/m³", 4, "ok"},
		{"pm25", "µg/m³", 12, "warn"},
		{"pm25", "µg/m³", 40, "danger"},
		{"pm10", "µg/m³", 19, "ok"},
		{"pm10", "µg/m³", 25, "warn"},
		{"pm10", "µg/m³", 55, "danger"},
		{"pm1", "µg/m³", 2, "ok"},
		{"pm1", "µg/m³", 15, "warn"},
		{"pm1", "µg/m³", 30, "danger"},
		{"aq", "%", 29, "ok"},
		{"aq", "%", 35, "warn"},
		{"aq", "%", 70, "danger"},
		{"t", "°C", 15, "danger"},
		{"t", "°C", 17, "warn"},
		{"t", "°C", 22, "ok"},
		{"t", "°C", 25, "ok"},
		{"t", "°C", 26, "warn"},
		{"t", "°C", 29, "danger"},
		{"t", "°F", 72, "ok"},
		{"t", "°F", 80, "warn"},
		{"t", "°F", 85, "danger"},
		{"rh", "%", 20, "danger"},
		{"rh", "%", 30, "warn"},
		{"rh", "%", 45, "ok"},
		{"rh", "%", 60, "warn"},
		{"rh", "%", 70, "danger"},
		{"hcho", "mg/m³", 0.037, "ok"},
		{"hcho", "mg/m³", 0.06, "warn"},
		{"hcho", "mg/m³", 0.11, "danger"},
		{"tvoc", "µg/m³", 120, "ok"},
		{"tvoc", "µg/m³", 250, "warn"},
		{"tvoc", "µg/m³", 550, "danger"},
	}
	for _, tc := range cases {
		b := BandsFor(tc.id, tc.unit)
		if b == nil {
			t.Fatalf("missing bands for %s %s", tc.id, tc.unit)
		}
		if MetricMeaning(tc.id, tc.unit) == "" {
			t.Fatalf("missing meaning for %s %s", tc.id, tc.unit)
		}
		if got := ZoneOf(tc.v, b); got != tc.want {
			t.Fatalf("%s(%s) v=%v zone=%q want %q", tc.id, tc.unit, tc.v, got, tc.want)
		}
	}

	// Low PM2.5 noise (1-2) normalizes across the full low/mid/high threshold range (0..40).
	_, _, minY, maxY, ok := bandBounds([]Pt{{X: 0, Y: 1}, {X: 1, Y: 2}}, BandsFor("pm25", "µg/m³"))
	if !ok || minY != 0 || maxY != 40 {
		t.Fatalf("pm25 bounds got [%v,%v] ok=%v", minY, maxY, ok)
	}

	// Narrow temperature band (25-26 °C) normalizes across the full indoor comfort frame (14..30 °C).
	_, _, minY, maxY, ok = bandBounds([]Pt{{X: 0, Y: 25}, {X: 1, Y: 26}}, BandsFor("t", "°C"))
	if !ok || minY != 14 || maxY != 30 {
		t.Fatalf("temp bounds got [%v,%v] ok=%v", minY, maxY, ok)
	}

	// Humidity normalizes across dry/comfort/humid range (15..75%) and expands if values exceed it.
	_, _, minY, maxY, ok = bandBounds([]Pt{{X: 0, Y: 40}, {X: 1, Y: 44}}, BandsFor("rh", "%"))
	if !ok || minY != 15 || maxY != 75 {
		t.Fatalf("rh bounds got [%v,%v] ok=%v", minY, maxY, ok)
	}
	_, _, minY, maxY, ok = bandBounds([]Pt{{X: 0, Y: 40}, {X: 1, Y: 82}}, BandsFor("rh", "%"))
	if !ok || minY != 15 || maxY < 84 {
		t.Fatalf("rh expanded bounds got [%v,%v] ok=%v", minY, maxY, ok)
	}

	// TVOC (34-309) stays inside its normalized 0..600 threshold frame.
	_, _, minY, maxY, ok = bandBounds([]Pt{{X: 0, Y: 34}, {X: 1, Y: 309}}, BandsFor("tvoc", "µg/m³"))
	if !ok || minY != 0 || maxY != 600 {
		t.Fatalf("tvoc bounds got [%v,%v] ok=%v", minY, maxY, ok)
	}

	// HCHO spanning all three zones (0.031-0.132) expands cleanly above 0.12 to 0..0.16.
	hchoBands := BandsFor("hcho", "mg/m³")
	_, _, minY, maxY, ok = bandBounds([]Pt{{X: 0, Y: 0.031}, {X: 1, Y: 0.132}}, hchoBands)
	if !ok || minY != 0 || maxY != 0.16 {
		t.Fatalf("hcho bounds got [%v,%v]", minY, maxY)
	}
	stops := gradientStops(minY, maxY, 108, 8, hchoBands)
	if len(stops) != 6 {
		t.Fatalf("expected 6 stops across 3 zones, got %d: %+v", len(stops), stops)
	}
	if stops[0].Color != "var(--danger)" || stops[2].Color != "var(--warn)" || stops[4].Color != "var(--ok)" {
		t.Fatalf("unexpected stop colors: %+v", stops)
	}
	if !(stops[0].Offset == 0 && stops[1].Offset == stops[2].Offset && stops[3].Offset == stops[4].Offset && stops[5].Offset == 1) {
		t.Fatalf("unexpected stop offsets: %+v", stops)
	}
	marks := bandThresholds(hchoBands, minY, maxY)
	if len(marks) != 2 || marks[0].Value != 0.08 || marks[1].Value != 0.05 {
		t.Fatalf("unexpected hcho threshold marks: %+v", marks)
	}
}
