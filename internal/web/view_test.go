package web

import (
	"strings"
	"testing"
	"time"
)

func TestEPAHalfOpen(t *testing.T) {
	cases := []struct {
		v     float64
		label string
	}{
		{0, "Good"},
		{9.0, "Good"},
		{9.1, "Moderate"},
		{35.4, "Moderate"},
		{35.5, "Poor"},
		{55.4, "Poor"},
		{55.5, "Unhealthy"},
		{200, "Unhealthy"},
	}
	for _, tc := range cases {
		label, _ := AQBand(tc.v)
		if label != tc.label {
			t.Fatalf("%v -> %s", tc.v, label)
		}
	}
}

func TestAQLevelOverridesEPA(t *testing.T) {
	s := Snapshot{
		Phase: "ready", Now: time.Date(2026, 11, 22, 15, 0, 0, 0, time.UTC), Loc: time.UTC,
		PM25: 40, PM25Known: true, AQL: 1, AQLKnown: true, AQLMap: map[string]string{"1": "Good"},
		Power: 1, PowerKnown: true, WritesOK: true,
	}
	p := Build(s)
	if p.Pill != "Good" || p.PillClass != "ok" || p.AQEstimate {
		t.Fatalf("%+v", p.Pill)
	}
	s.AQLMap = nil
	s.AQLKnown = false
	p = Build(s)
	if p.Pill != "Poor" || !p.AQEstimate {
		t.Fatal(p.Pill, p.AQEstimate)
	}
	if !p.PowerOff {
		t.Fatal("power toggle should stay disabled when PowerActionOK is false")
	}
	s.PowerActionOK = true
	if Build(s).PowerOff {
		t.Fatal("power toggle should enable when WritesOK, PowerKnown, and PowerActionOK are true")
	}
}

func TestSettingsPrecedenceLabels(t *testing.T) {
	s := Snapshot{
		Phase: "ready", Page: "device", Now: time.Now(), Loc: time.UTC,
		ReadOnly: true, ReadOnlyLocked: true, ReadOnlySource: "env",
		PollSeconds: 45, PollSource: "saved", TempUnit: "F", TempSource: "saved",
		HasOverrides: true,
	}
	b, err := RenderHTML(s)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	if !strings.Contains(body, "Locked to read-only by DMD_READ_ONLY=true.") {
		t.Fatal("lock note")
	}
	if !strings.Contains(body, "Source saved") {
		t.Fatal("source")
	}
	if strings.Contains(body, "style=") {
		t.Fatal("style")
	}
}

func TestLocalTimeAndDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	ts := time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC)
	if FormatTick(ts, loc) != "03:30" {
		t.Fatal(FormatTick(ts, loc))
	}
	if FormatWhen(ts, now, loc) != "Today 03:30:00" {
		t.Fatal(FormatWhen(ts, now, loc))
	}
	y := time.Date(2026, 3, 7, 7, 30, 0, 0, time.UTC)
	if FormatWhen(y, now, loc) != "Yesterday 02:30" {
		t.Fatal(FormatWhen(y, now, loc))
	}
}

func TestStandbySentence(t *testing.T) {
	now := time.Date(2026, 11, 22, 15, 4, 5, 0, time.UTC)
	s := Snapshot{Now: now, Loc: time.UTC, Power: 2, PowerKnown: true, PM25: 4, PM25Known: true, StatusAt: now.Add(-18 * time.Second), Status: "online"}
	p := Build(s)
	if p.Pill != "Standby" || !strings.Contains(p.Sentence, "In standby") {
		t.Fatal(p.Pill, p.Sentence)
	}
}

func TestBootStatusConnecting(t *testing.T) {
	now := time.Date(2026, 11, 22, 15, 0, 0, 0, time.UTC)
	p := Build(Snapshot{Now: now, Loc: time.UTC, Status: "boot"})
	if p.Age != "Connecting" || p.Dot != "muted" {
		t.Fatalf("boot status rendered as age=%q dot=%q", p.Age, p.Dot)
	}
}

func TestHCHOAndTVOCUnitsAndPrecision(t *testing.T) {
	now := time.Date(2026, 11, 22, 15, 0, 0, 0, time.UTC)
	p := Build(Snapshot{
		Now: now, Loc: time.UTC, Status: "online",
		HCHO: 0.041, HCHOKnown: true, TVOC: 176, TVOCKnown: true,
	})
	var hcho, tvoc string
	for _, s := range p.Sensors {
		if s.Label == "HCHO" {
			hcho = string(s.Value)
		}
		if s.Label == "TVOC" {
			tvoc = string(s.Value)
		}
	}
	if hcho != "0.041\u00a0mg/m³" || tvoc != "176\u00a0µg/m³" {
		t.Fatalf("hcho=%q tvoc=%q", hcho, tvoc)
	}
	if got := trim(0.037); got != "0.037" {
		t.Fatalf("trim(0.037) = %q", got)
	}
}

func TestDeviceStandbyStateAndControls(t *testing.T) {
	now := time.Date(2026, 11, 24, 18, 55, 0, 0, time.UTC)
	base := Snapshot{
		Phase: "ready", Page: "device", Now: now, Loc: time.UTC, Status: "online",
		WritesOK: true, PowerKnown: true, Power: 1,
		ModeKnown: true, Mode: 0, ModeVals: []int{0, 3, 4},
		FanKnown: true, Fan: 5, FanMin: 1, FanMax: 10,
	}
	working := Build(base)
	if working.PowerState != "Working" || working.PowerStateClass != "ok" || working.Standby {
		t.Fatalf("working state = %+v", working)
	}
	for _, g := range working.Groups {
		if (g.Label == "Mode" || g.Label == "Fan speed") && g.Off {
			t.Fatalf("%s should be enabled when working", g.Label)
		}
	}
	workHTML, err := RenderHTML(base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workHTML), `id="power-state-pill" class="pill pill-ok">Working</span>`) {
		t.Fatalf("missing Working pill in device HTML")
	}

	standbySnap := base
	standbySnap.Power = 2
	standbySnap.Fan = 1
	standby := Build(standbySnap)
	if standby.PowerState != "Standby" || standby.PowerStateClass != "muted" || !standby.Standby {
		t.Fatalf("standby state = %+v", standby)
	}
	if !strings.Contains(standby.WriteReason, "In standby") {
		t.Fatalf("expected standby write reason, got %q", standby.WriteReason)
	}
	for _, g := range standby.Groups {
		if (g.Label == "Mode" || g.Label == "Fan speed") && !g.Off {
			t.Fatalf("%s should be disabled in standby", g.Label)
		}
		if g.Label == "Indicator" && g.Off {
			t.Fatalf("Indicator should stay enabled in standby")
		}
	}
	if stageAllowed(standbySnap, 2, 3, 3) || stageAllowed(standbySnap, 2, 4, 9) {
		t.Fatal("mode/fan staging should be disallowed in standby")
	}
	raw := string(StateJSON(standbySnap))
	if !strings.Contains(raw, `"power_state":"Standby"`) || !strings.Contains(raw, `"standby":true`) {
		t.Fatalf("StateJSON missing standby fields: %s", raw)
	}
}

func TestDevicePM20ControlsAndStandby(t *testing.T) {
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	base := Snapshot{
		Phase: "ready", Page: "device", Now: now, Loc: time.UTC, Status: "online",
		WritesOK: true, PowerKnown: true, Power: 1, PowerActionOK: true,
		ModeKnown: true, Mode: 5, ModeVals: []int{0, 5, 3, 4},
		FanKnown: true, Fan: 3, FanMin: 1, FanMax: 10, FanForcesCustom: true,
		PM20Controls: true,
		HEPAKnown:    true, HEPA: 100, HEPADaysKnown: true, HEPADays: 718,
		CarbonKnown: true, Carbon: 100, CarbonDaysKnown: true, CarbonDays: 358,
		HeaterKnown: true, Heater: 22, HeaterMin: 18, HeaterMax: 40, TempUnit: "F",
		OscillationKnown: true, Oscillation: 90, OscillationVals: []int{0, 45, 90, 180},
		ContinuousMonitoringKnown: true, ContinuousMonitoring: 0, ContinuousMonitoringOK: true,
		NearbyPowerOnKnown: true, NearbyPowerOn: 1, NearbyPowerOnOK: true,
		FollowMotionKnown: true, FollowMotion: 0, FollowMotionOK: true,
	}
	working := Build(base)
	if working.PowerLabel != "Standby" || working.PowerVerb != "Confirm standby" || working.PowerOff {
		t.Fatalf("working power btn = %q / %q off=%v", working.PowerLabel, working.PowerVerb, working.PowerOff)
	}
	htmlBytes, err := RenderHTML(base)
	if err != nil {
		t.Fatal(err)
	}
	body := string(htmlBytes)
	if strings.Contains(body, "style=") {
		t.Fatal("device PM20 HTML contains inline style=")
	}
	for _, want := range []string{
		`id="power-btn"`, `>Standby</button>`, `>Comfort</button>`,
		`>Oscillation</div>`, `>90°</button>`, `data-stepper="heater"`, `72 °F`,
		`>Follow motion</div>`, `>Nearby power-on</div>`, `>Continuous monitoring</div>`,
		`718 days left`, `Carbon filter`, `358 days left`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("PM20 device page missing %q", want)
		}
	}
	for _, unwanted := range []string{`>Indicator</div>`, `>Voice volume</div>`, `>Key tone</div>`, `h used`} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("PM20 device page should hide legacy %q", unwanted)
		}
	}

	// Standby: Power ("Turn on"), Nearby power-on, Continuous monitoring stay enabled; airflow & heater disabled.
	standbySnap := base
	standbySnap.Power = 2
	standby := Build(standbySnap)
	if standby.PowerLabel != "Turn on" || standby.PowerVerb != "Confirm turn on" || standby.PowerOff {
		t.Fatalf("standby power btn = %q / %q off=%v", standby.PowerLabel, standby.PowerVerb, standby.PowerOff)
	}
	for _, g := range standby.Groups {
		switch g.Label {
		case "Mode", "Fan speed", "Oscillation", "Heater", "Follow motion":
			if !g.Off {
				t.Fatalf("%s should be disabled in standby", g.Label)
			}
		case "Nearby power-on", "Continuous monitoring":
			if g.Off {
				t.Fatalf("%s should stay enabled in standby", g.Label)
			}
		}
	}
	if stageAllowed(standbySnap, 2, 6, 22) || stageAllowed(standbySnap, 2, 7, 90) || stageAllowed(standbySnap, 6, 20, 1) {
		t.Fatal("heater/oscillation/follow-motion staging should be blocked in standby")
	}
	if !stageAllowed(standbySnap, 6, 15, 1) || !stageAllowed(standbySnap, 6, 19, 0) {
		t.Fatal("continuous monitoring and nearby power-on staging should be allowed in standby")
	}
	if stageAllowed(base, 2, 5, 80) {
		t.Fatal("unused 2.5 should be blocked on PM20")
	}
	raw := string(StateJSON(standbySnap))
	if !strings.Contains(raw, `"power_verb":"Confirm turn on"`) || !strings.Contains(raw, `"heater":{`) {
		t.Fatalf("StateJSON missing PM20 heater/power fields: %s", raw)
	}
}

func TestOverviewStandbyChartsAndPill(t *testing.T) {
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	snap := Snapshot{
		Phase: "ready", Page: "overview", Now: now, Loc: time.UTC, Status: "online",
		PowerKnown: true, Power: 2,
		Charts: []ChartGroup{
			{ID: "pm25", Title: "PM2.5", Series: []Series{{Name: "PM2.5", Unit: "µg/m³", Class: "series-a", Fill: true}}},
			{ID: "tvoc", Title: "TVOC", Series: []Series{{Name: "TVOC", Unit: "µg/m³", Class: "series-2", Fill: true}}},
		},
	}
	b, err := RenderHTML(snap)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	if !strings.Contains(body, `id="top-standby" href="/device#controls">Standby</a>`) {
		t.Fatalf("expected visible topbar standby pill in standby overview")
	}
	if !strings.Contains(body, `>Paused in standby</p>`) || !strings.Contains(body, `>Collecting readings…</p>`) {
		t.Fatalf("expected Paused in standby for pm25 and Collecting readings for tvoc")
	}
	hist := string(HistoryJSON(snap))
	if !strings.Contains(hist, `"empty_text":"Paused in standby"`) {
		t.Fatalf("HistoryJSON missing empty_text: %s", hist)
	}

	// Continuous monitoring active in standby uses Collecting readings...
	cmSnap := snap
	cmSnap.ContinuousMonitoringKnown = true
	cmSnap.ContinuousMonitoring = 1
	cmHTML, err := RenderHTML(cmSnap)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cmHTML), "Paused in standby") {
		t.Fatal("continuous monitoring should not label empty charts as paused in standby")
	}

	// Working state hides the overview Standby pill.
	workSnap := snap
	workSnap.Power = 1
	workHTML, err := RenderHTML(workSnap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workHTML), `id="top-standby" href="/device#controls" hidden>Standby</a>`) {
		t.Fatal("topbar standby pill should be hidden when working")
	}
}

func TestDeviceOTAPanelAndConfirm(t *testing.T) {
	now := time.Date(2026, 11, 25, 10, 30, 0, 0, time.UTC)
	base := Snapshot{
		Phase: "ready", Page: "device", Now: now, Loc: time.UTC, Status: "online",
		Token: "tok", OTAState: "idle", OTAVersion: "1.8.17_1070", OTAChecked: "Today 10:25:00",
	}
	b, err := RenderHTML(base)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	if !strings.Contains(body, `id="ota"`) || !strings.Contains(body, `Up to date`) || !strings.Contains(body, `Check for updates`) {
		t.Fatalf("missing up-to-date OTA panel elements")
	}
	if strings.Contains(body, `Install update`) || strings.Contains(body, "style=") {
		t.Fatalf("unexpected Install update or inline style when up to date")
	}

	avail := base
	avail.OTAHasUpdate = true
	avail.OTANewVersion = "1.8.17_1075"
	avail.OTATarget = "MCU"
	avail.OTADescription = "Stability improvements"
	b2, err := RenderHTML(avail)
	if err != nil {
		t.Fatal(err)
	}
	body2 := string(b2)
	for _, want := range []string{`Update available`, `1.8.17_1075`, `(MCU)`, `Stability improvements`, `value="ota_update"`} {
		if !strings.Contains(body2, want) {
			t.Fatalf("update-available OTA panel missing %q", want)
		}
	}

	staged := avail
	staged.Query = map[string][]string{"stage": {"ota_update"}}
	b3, err := RenderHTML(staged)
	if err != nil {
		t.Fatal(err)
	}
	body3 := string(b3)
	if !strings.Contains(body3, `action="/ota/update"`) || !strings.Contains(body3, `Confirm firmware update`) {
		t.Fatalf("staged OTA update missing confirm form")
	}

	roStaged := staged
	roStaged.ReadOnly = true
	b4, err := RenderHTML(roStaged)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b4), `Confirm firmware update`) {
		t.Fatal("read-only mode should suppress OTA update confirmation")
	}
}
