package web

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"dreame-dashboard/internal/ai"
)

func TestMarkup(t *testing.T) {
	now := time.Date(2026, 11, 22, 15, 0, 0, 0, time.UTC)
	base := Snapshot{
		Now: now, Loc: time.UTC, Token: "tok", Name: "Air Purifier PM20",
		Status: "online", StatusAt: now.Add(-14 * time.Second),
		Power: 1, PowerKnown: true, Mode: 0, ModeKnown: true, Fan: 2, FanKnown: true,
		WritesOK: true, PM25: 4, PM25Known: true, AQ: 96, AQKnown: true,
		HEPA: 84, HEPAKnown: true, HEPADays: 720, HEPADaysKnown: true, FilterHours: 2760, FilterHoursKnown: true,
		TempC: 22.4, TempKnown: true, RH: 46, RHKnown: true,
		Identity: [][2]string{{"Model", "dreame.airp.u2402"}, {"DID", "-100000001"}},
		Props:    []PropRow{{Key: "pm25", ID: "3.5", Value: "4", Age: "14 s ago", State: "Active"}},
		Rows:     []Act{{TS: now, Kind: "command", Target: "Mode (2.3)", Detail: "AI -> Sleep", Result: "OK (0)"}},
		Verified: "read-sweep", PollSeconds: 30,
	}
	pages := []string{
		must(t, Snapshot{Phase: "boot", Now: now, Loc: time.UTC}),
		must(t, Snapshot{Phase: "auth_required", Now: now, Loc: time.UTC, Region: "us"}),
		must(t, base),
		must(t, withPage(base, "device")),
		must(t, withPage(base, "activity")),
	}
	for i, body := range pages {
		if strings.Count(body, "<h1") != 1 {
			t.Fatalf("page %d h1 %d", i, strings.Count(body, "<h1"))
		}
		if !strings.Contains(body, `class="skip"`) || !strings.Contains(body, `href="#main"`) {
			t.Fatalf("page %d skip", i)
		}
		if !strings.Contains(body, `name="theme-color" content="#101410"`) {
			t.Fatal("theme")
		}
		if strings.Contains(body, "<script>") {
			t.Fatal("inline script")
		}
		if strings.Contains(body, "style=") {
			t.Fatalf("page %d style", i)
		}
		if strings.Contains(body, "<table") && (!strings.Contains(body, "<caption") || !strings.Contains(body, `scope="col"`)) {
			t.Fatalf("page %d table", i)
		}
		if ariaPressedOnLink.MatchString(body) {
			t.Fatalf("page %d aria-pressed on link", i)
		}
	}
	if strings.Count(pages[0], `http-equiv="refresh"`) != 1 {
		t.Fatal("boot refresh")
	}
	for _, body := range pages[1:] {
		if strings.Contains(body, "http-equiv=\"refresh\"") {
			t.Fatal("refresh outside boot")
		}
	}
	if !strings.Contains(pages[2], `id="age"`) || strings.Contains(pages[2], `id="controls"`) || strings.Contains(pages[2], `role="progressbar"`) {
		t.Fatal("overview should focus on air data without controls or filter bar")
	}
	if strings.Contains(pages[2], `class="tabs"`) || !strings.Contains(pages[2], `class="icon-btn gear-btn"`) {
		t.Fatal("overview topbar should use gear icon instead of top tabs")
	}
	if !strings.Contains(pages[3], `id="ota"`) || !strings.Contains(pages[3], `action="/ota/check"`) || !strings.Contains(pages[3], `class="diag-details workspace-diag"`) || !strings.Contains(pages[3], `class="session-row"`) {
		t.Fatal("device page OTA and layout assertions failed")
	}
	if strings.Index(pages[3], `class="panel tool-danger workspace-session"`) < strings.Index(pages[3], `class="diag-details workspace-diag"`) {
		t.Fatal("session block should come at the end of mobile flow")
	}
	if strings.Contains(pages[2], `id="age" aria-live`) || strings.Contains(pages[2], `aria-live="polite" id="age"`) {
		t.Fatal("age live")
	}
	if !strings.Contains(pages[2], `id="announcer"`) || !strings.Contains(pages[2], `role="status"`) {
		t.Fatal("announcer")
	}
	if !strings.Contains(pages[2], `translate="no"`) {
		t.Fatal("translate")
	}
	if !strings.Contains(pages[3], `role="progressbar"`) || !strings.Contains(pages[3], `aria-pressed=`) || !strings.Contains(pages[3], `id="controls"`) {
		t.Fatal("device controls and filter bar")
	}
	css, err := os.ReadFile("page.css")
	if err != nil {
		t.Fatal(err)
	}
	text := string(css)
	if strings.Contains(text, "transition: all") {
		t.Fatal("transition all")
	}
	if !strings.Contains(text, ":focus-visible") || !strings.Contains(text, "outline: 2px solid var(--ink)") {
		t.Fatal("focus")
	}
	if !strings.Contains(text, "color-scheme: dark") {
		t.Fatal("scheme")
	}
	if !strings.Contains(text, "select option") || !strings.Contains(text, "background: var(--panel)") {
		t.Fatal("select colors")
	}
	device := pages[3]
	keyAt := strings.Index(device, ">pm25</td>")
	idAt := strings.Index(device, ">3.5</td>")
	if keyAt < 0 || idAt < 0 || keyAt > idAt {
		t.Fatal("device column order")
	}
	if strings.Index(device, ">Key</th>") > strings.Index(device, ">Property</th>") {
		t.Fatal("device header order")
	}
	t0 := now.Add(-time.Minute)
	particles := base
	particles.Metric = "particles"
	particles.Series = []Series{
		{Name: "PM2.5", Unit: "µg/m³", Class: "series-a", Fill: true, Points: []Sample{{TS: t0, V: 4}, {TS: now, V: 5}}},
		{Name: "PM10", Unit: "µg/m³", Class: "series-2", Points: []Sample{{TS: t0, V: 6}, {TS: now, V: 7}}},
		{Name: "PM1", Unit: "µg/m³", Class: "series-3", Points: []Sample{{TS: t0, V: 1}, {TS: now.Add(time.Minute), V: 2, Gap: true}}},
	}
	chart := must(t, particles)
	if strings.Count(chart, `data-fill-id="`) != 1 || strings.Contains(chart, "track-half") {
		t.Fatalf("particles tracks svg %d", strings.Count(chart, `data-fill-id="`))
	}
	if !strings.Contains(chart, `class="area"`) || !strings.Contains(chart, " Z") || strings.Contains(chart, "style=") {
		t.Fatal("particles area")
	}
	climate := base
	climate.Metric = "climate"
	climate.Series = []Series{
		{Name: "Temperature", Unit: "°C", Class: "series-a", Fill: true, Points: []Sample{{TS: t0, V: 22}, {TS: now, V: 23}}},
		{Name: "Humidity", Unit: "%", Class: "series-2", Points: []Sample{{TS: t0, V: 40}, {TS: now, V: 41}}},
	}
	half := must(t, climate)
	if strings.Count(half, "track-half") != 2 || strings.Count(half, `data-fill-id="`) != 2 {
		t.Fatal("climate tracks")
	}
	if !strings.Contains(device, `<span><span translate="no">HEPA</span> filter</span>`) {
		t.Fatal("hepa-head grouping on device")
	}
	if !strings.Contains(device, `class="note fan-note"`) {
		t.Fatal("fan-note class on device")
	}
	if !strings.Contains(device, `<dl class="kv">`) || strings.Contains(device, `<dl class="totals">`) {
		t.Fatal("device kv list")
	}
	multi := base
	multi.OpenAIConfigured = true
	multi.OpenAIHint = "sk-••••abcd"
	multi.OpenAIModel = "gpt-4o-mini"
	multi.DailyAI = &ai.Report{
		Scope:           "daily",
		Span:            "24h",
		Model:           "gpt-4.1",
		GeneratedAt:     now.Format(time.RFC3339),
		Status:          "attention",
		Headline:        "Particles clean; evening HCHO spike cleared",
		Summary:         "PM2.5 stayed under 3 µg/m³ all day while HCHO briefly peaked at 0.132 mg/m³ at 21:00.",
		Findings:        []string{"HCHO peaked at 0.132 mg/m³ around 21:00 and dropped to 0.037 mg/m³."},
		Metrics:         []ai.MetricNote{{ID: "hcho", Label: "HCHO", Status: "attention", Assessment: "Back in safe zone."}},
		Recommendations: []string{"Ventilate briefly if HCHO rises again."},
	}
	multi.DailyAINext = "in 52 m"
	multi.Charts = []ChartGroup{
		{ID: "pm25", Title: "PM2.5", Series: []Series{{Name: "PM2.5", Unit: "µg/m³", Class: "series-a", Fill: true, Points: []Sample{{TS: t0, V: 4}, {TS: now, V: 5}}}}},
		{ID: "pm10", Title: "PM10", Series: []Series{{Name: "PM10", Unit: "µg/m³", Class: "series-2", Fill: true, Points: []Sample{{TS: t0, V: 6}, {TS: now, V: 7}}}}},
		{ID: "pm1", Title: "PM1", Series: []Series{{Name: "PM1", Unit: "µg/m³", Class: "series-3", Fill: true, Points: []Sample{{TS: t0, V: 1}, {TS: now, V: 2}}}}},
		{ID: "aq", Title: "Air quality", Series: []Series{{Name: "Air quality", Unit: "%", Class: "series-a", Fill: true, Points: []Sample{{TS: t0, V: 90}, {TS: now, V: 95}}}}},
		{ID: "t", Title: "Temperature", Series: []Series{{Name: "Temperature", Unit: "°C", Class: "series-a", Fill: true, Points: []Sample{{TS: t0, V: 22}, {TS: now, V: 23}}}}},
		{ID: "rh", Title: "Humidity", Series: []Series{{Name: "Humidity", Unit: "%", Class: "series-2", Fill: true, Points: []Sample{{TS: t0, V: 40}, {TS: now, V: 41}}}}},
		{ID: "hcho", Title: "HCHO", Series: []Series{{Name: "HCHO", Unit: "mg/m³", Class: "series-a", Fill: true, Points: []Sample{{TS: t0, V: 0.035}, {TS: now, V: 0.095}}}}},
		{ID: "tvoc", Title: "TVOC", Series: []Series{{Name: "TVOC", Unit: "µg/m³", Class: "series-2", Fill: true, Points: []Sample{{TS: t0, V: 120}, {TS: now, V: 280}}}}},
	}
	multiHTML := must(t, multi)
	if strings.Count(multiHTML, `data-chart="`) != 8 || strings.Count(multiHTML, `class="chart-svg"`) != 9 {
		t.Fatalf("multi cards=%d svgs=%d", strings.Count(multiHTML, `data-chart="`), strings.Count(multiHTML, `class="chart-svg"`))
	}
	if strings.Contains(multiHTML, "style=") {
		t.Fatal("multiHTML contains inline style=")
	}
	for _, want := range []string{
		`id="fill-pm25-0"`, `id="fill-pm10-0"`, `id="fill-tvoc-0"`,
		`id="stroke-hcho-0"`, `stroke="url(#stroke-hcho-0)"`, `class="series series-band"`,
		`class="threshold-line threshold-warn"`, `class="threshold-line threshold-danger"`,
		`chart-zone-dot`, `val-danger`, `val-warn`, `val-ok`,
		`id="chart-zoom"`, `id="stroke-zoom"`, `id="zoom-bands"`, `id="zoom-desc"`,
		`id="ai-daily-text"`, `PM2.5 stayed under 3 µg/m³ all day`,
		`class="chart-desc"`, `Fine dust and smoke small enough to reach deep into lungs`,
		`class="icon-btn chart-zoom-btn"`,
	} {
		if !strings.Contains(multiHTML, want) {
			t.Fatalf("missing %s", want)
		}
	}
	for _, unwanted := range []string{`class="panel air-hero"`, `id="ai-daily-panel"`, `id="ai-daily-refresh"`, `class="ai-details"`} {
		if strings.Contains(multiHTML, unwanted) {
			t.Fatalf("unexpected %s in overview", unwanted)
		}
	}
	if strings.Contains(multiHTML, `id="fill-0"`) {
		t.Fatal("hardcoded fill-0 gradient id still present")
	}
	devAI := must(t, withPage(multi, "device"))
	if !strings.Contains(devAI, `action="/admin/openai"`) || !strings.Contains(devAI, `type="password"`) || !strings.Contains(devAI, `sk-••••abcd`) {
		t.Fatal("device OpenAI panel missing")
	}
	histJSON := string(HistoryJSON(multi))
	for _, want := range []string{`"charts":[`, `"y_ticks"`, `"x_ticks"`, `"readouts"`, `"stats"`, `"aria_label"`, `"bands":{`, `"zone":"danger"`} {
		if !strings.Contains(histJSON, want) {
			t.Fatalf("HistoryJSON missing %s: %s", want, histJSON)
		}
	}
}

func must(t *testing.T, s Snapshot) string {
	t.Helper()
	b, err := RenderHTML(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func withPage(s Snapshot, page string) Snapshot {
	s.Page = page
	return s
}

var ariaPressedOnLink = regexp.MustCompile(`<a\b[^>]*aria-pressed`)
