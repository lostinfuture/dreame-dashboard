package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dreame-dashboard/internal/ai"
	"dreame-dashboard/internal/config"
	"dreame-dashboard/internal/dreame"
	"dreame-dashboard/internal/store"
	"dreame-dashboard/internal/web"
)

type recordedAction struct {
	SIID int
	AIID int
	In   []any
}

type fakeCloud struct {
	gets       [][]dreame.PropRef
	sets       []dreame.PropWrite
	acts       int
	actCalls   []recordedAction
	actCode    int
	actMutate  bool
	props      map[string]dreame.PropResult
	failN      int
	listN      int
	otaN       int
	otaVer     dreame.OTAVersion
	otaVerN    int
	otaUpdates []string
	otcN       int
}

func (f *fakeCloud) ListDevices(context.Context) ([]dreame.Device, error) {
	f.listN++
	return []dreame.Device{{DID: "-1", Model: "dreame.airp.u2402", BindDomain: "10000.mt.us.iot.dreame.tech:19973", Name: "Air Purifier PM20", Online: true}}, nil
}
func (f *fakeCloud) GetProperties(_ context.Context, _ string, refs []dreame.PropRef) ([]dreame.PropResult, error) {
	f.gets = append(f.gets, refs)
	if f.failN > 0 {
		f.failN--
		return nil, &dreame.APIError{Code: 80001, Msg: "slow"}
	}
	var out []dreame.PropResult
	for _, r := range refs {
		if row, ok := f.props[r.Key()]; ok {
			out = append(out, row)
			continue
		}
		out = append(out, dreame.PropResult{SIID: r.SIID, PIID: r.PIID, Code: 0, Has: true, Value: float64(1)})
	}
	return out, nil
}
func (f *fakeCloud) SetProperties(_ context.Context, _ string, writes []dreame.PropWrite) error {
	f.sets = append(f.sets, writes...)
	return nil
}
func (f *fakeCloud) Action(_ context.Context, _ string, siid, aiid int, in []any) (int, error) {
	f.acts++
	f.actCalls = append(f.actCalls, recordedAction{SIID: siid, AIID: aiid, In: in})
	if f.actCode == 0 && f.actMutate && siid == 2 && aiid == 1 && len(in) > 0 {
		if m, ok := in[0].(map[string]any); ok {
			if v, is := asInt(m["value"]); is {
				if f.props == nil {
					f.props = map[string]dreame.PropResult{}
				}
				if v == 1 {
					f.props["2.1"] = dreame.PropResult{SIID: 2, PIID: 1, Code: 0, Has: true, Value: float64(1)}
				} else {
					f.props["2.1"] = dreame.PropResult{SIID: 2, PIID: 1, Code: 0, Has: true, Value: float64(2)}
				}
			}
		}
	}
	return f.actCode, nil
}
func (f *fakeCloud) DevOTCInfo(context.Context, string) (dreame.OTC, error) {
	f.otcN++
	return dreame.OTC{IP: "192.0.2.10", RSSI: -40, HasRSSI: true, SSID: "home"}, nil
}
func (f *fakeCloud) GetOTAState(context.Context, string) (string, error) {
	f.otaN++
	return "idle", nil
}
func (f *fakeCloud) GetOTAProgress(context.Context, string) (float64, string, error) {
	f.otaN++
	return 0, "0", nil
}
func (f *fakeCloud) CheckDeviceVersion(context.Context, string) (dreame.OTAVersion, error) {
	f.otaVerN++
	if f.otaVer.CurVersion == "" && !f.otaVer.HasNew {
		return dreame.OTAVersion{CurVersion: "1.8.17_1070"}, nil
	}
	return f.otaVer, nil
}
func (f *fakeCloud) StartFirmwareUpdate(_ context.Context, _ string, target string) error {
	f.otaUpdates = append(f.otaUpdates, target)
	return nil
}
func (f *fakeCloud) SetPrefix(string) {}

func testProfile() *dreame.Profile {
	p := &dreame.Profile{DID: "-1", VerifiedBy: "write-probe"}
	for _, sp := range [][2]int{{2, 1}, {2, 3}, {3, 5}, {3, 2}, {4, 1}, {1, 4}} {
		access := "ro"
		if sp[0] == 2 && sp[1] == 3 {
			access = "rw"
		}
		p.Properties = append(p.Properties, dreame.PropSpec{SIID: sp[0], PIID: sp[1], Key: "x", Realized: true, Access: access})
	}
	p.Controls.PowerAction = true
	p.Controls.Mode = []int{0, 1, 2, 3, 4}
	p.Controls.FanMin = 1
	p.Controls.FanMax = 5
	return p
}

func TestPollSplitsHotAndSlow(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	cloud := &fakeCloud{props: map[string]dreame.PropResult{}}
	p := &Poller{Cloud: cloud, Store: st, Profile: testProfile(), Interval: 30 * time.Second, Now: func() time.Time { return now }}
	if err := p.PollOnce(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if cloud.listN != 1 || cloud.otcN != 1 || cloud.otaN != 2 {
		t.Fatalf("slow list %d otc %d ota %d", cloud.listN, cloud.otcN, cloud.otaN)
	}
	if len(cloud.gets) != 1 {
		t.Fatal(len(cloud.gets))
	}
	hot, slow := 0, 0
	for _, r := range cloud.gets[0] {
		if r.SIID == 2 || r.SIID == 3 {
			hot++
		} else {
			slow++
		}
	}
	if hot == 0 || slow == 0 {
		t.Fatalf("hot %d slow %d", hot, slow)
	}
	cloud.gets = nil
	now = now.Add(30 * time.Second)
	if err := p.PollOnce(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if cloud.listN != 1 {
		t.Fatal("slow repeated")
	}
	for _, r := range cloud.gets[0] {
		if r.SIID != 2 && r.SIID != 3 {
			t.Fatal("slow prop in hot poll", r)
		}
	}
	if st.CallsPerMin(now) < 1 {
		t.Fatal(st.CallsPerMin(now))
	}
}

func TestDegradedThenOffline(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	st.Lock()
	st.State().Props["3.5"] = store.PropSnap{Value: float64(4), At: time.Now()}
	st.Unlock()
	cloud := &fakeCloud{failN: 5}
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	p := &Poller{Cloud: cloud, Store: st, Profile: testProfile(), Now: func() time.Time { return now }}
	if err := p.PollOnce(context.Background(), false); err == nil {
		t.Fatal("expected degraded")
	}
	if st.State().Status != "degraded" || st.State().Props["3.5"].Value != float64(4) {
		t.Fatal(st.State().Status, st.State().Props["3.5"])
	}
	for i := 0; i < 4; i++ {
		now = now.Add(time.Minute)
		_ = p.PollOnce(context.Background(), false)
	}
	if st.State().Status != "offline" || st.State().Streak < 5 {
		t.Fatal(st.State().Status, st.State().Streak)
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	prof := testProfile()
	cloud := &fakeCloud{props: map[string]dreame.PropResult{
		"3.5": {SIID: 3, PIID: 5, Code: -4003, Has: true},
	}}
	var saved int
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	p := &Poller{Cloud: cloud, Store: st, Profile: prof, Now: func() time.Time { return now }, SaveProfile: func(*dreame.Profile) error { saved++; return nil }}
	for i := 0; i < 3; i++ {
		now = now.Add(time.Minute)
		if err := p.PollOnce(context.Background(), false); err != nil {
			t.Fatal(err)
		}
	}
	spec, ok := prof.Prop("3.5")
	if !ok || !spec.Pruned || saved == 0 {
		t.Fatalf("%+v saved %d", spec, saved)
	}
}

func TestControlConflictAndAudit(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	st.Lock()
	st.State().Status = "online"
	st.State().Props["2.1"] = store.PropSnap{Value: 1, At: now.Add(-2 * time.Minute)}
	st.Unlock()
	cloud := &fakeCloud{props: map[string]dreame.PropResult{
		"2.1": {SIID: 2, PIID: 1, Code: 0, Has: true, Value: 2},
		"2.3": {SIID: 2, PIID: 3, Code: 0, Has: true, Value: 2},
	}}
	prof := testProfile()
	w := &Worker{Cloud: cloud, Store: st, Profile: prof, Interval: 30 * time.Second, Now: func() time.Time { return now }, Sleep: func(time.Duration) {}}
	err := w.Do(context.Background(), Request{Action: "toggle_power", Expected: 1})
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 409 || cloud.acts != 0 {
		t.Fatal(err, cloud.acts)
	}
	ro := &Worker{Cloud: cloud, Store: st, Profile: prof, ReadOnly: func() bool { return true }, Now: func() time.Time { return now }, Sleep: func(time.Duration) {}}
	err = ro.Do(context.Background(), Request{Action: "set", SIID: 2, PIID: 3, Value: 2, Expected: 0})
	if !errors.As(err, &se) || se.Code != 403 {
		t.Fatal(err)
	}
	open := testProfile()
	open.VerifiedBy = "read-sweep"
	locked := &Worker{Cloud: cloud, Store: st, Profile: open, Now: func() time.Time { return now }, Sleep: func(time.Duration) {}}
	if err := locked.Do(context.Background(), Request{SIID: 2, PIID: 3, Value: 1}); !errors.As(err, &se) || se.Code != 403 {
		t.Fatal(err)
	}
	okw := &Worker{Cloud: cloud, Store: st, Profile: prof, Now: func() time.Time { return now }, Sleep: func(time.Duration) {}}
	if err := okw.Do(context.Background(), Request{Action: "explode", SIID: 2, PIID: 3, Value: 2}); !errors.As(err, &se) || se.Code != 400 {
		t.Fatal(err)
	}
	if err := okw.Do(context.Background(), Request{Action: "set", SIID: 2, PIID: 3, Value: 9}); !errors.As(err, &se) || se.Code != 400 {
		t.Fatal(err)
	}
	st.Lock()
	st.State().Props["2.1"] = store.PropSnap{Value: 1, At: now}
	st.Unlock()
	if err := okw.Do(context.Background(), Request{Action: "set", SIID: 2, PIID: 3, Value: 2, Expected: 99}); err != nil {
		t.Fatal(err)
	}
	if len(cloud.sets) != 1 {
		t.Fatal(cloud.sets)
	}
	rows := st.ActivitySince(now.Add(-time.Hour))
	if len(rows) < 2 {
		t.Fatal(len(rows))
	}
}

func TestStandbyGuardAndReadBackMismatch(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 24, 18, 55, 0, 0, time.UTC)
	prof := testProfile()
	prof.Properties = append(prof.Properties,
		dreame.PropSpec{SIID: 2, PIID: 4, Key: "fan_speed", JSONType: "number", Access: "rw", Realized: true},
		dreame.PropSpec{SIID: 2, PIID: 6, Key: "light", JSONType: "number", Access: "rw", Realized: true},
	)
	prof.Controls.FanMax = 10
	prof.Controls.Light = []int{-1, 0, 1, 2}

	// 1. In standby, Mode (2.3) and Fan (2.4) are rejected with 409 before hitting the cloud.
	st.Lock()
	st.State().Status = "online"
	st.State().Props["2.1"] = store.PropSnap{Value: float64(2), At: now}
	st.Unlock()
	cloud := &fakeCloud{props: map[string]dreame.PropResult{
		"2.1": {SIID: 2, PIID: 1, Code: 0, Has: true, Value: float64(2)},
		"2.4": {SIID: 2, PIID: 4, Code: 0, Has: true, Value: float64(1)},
		"2.6": {SIID: 2, PIID: 6, Code: 0, Has: true, Value: float64(0)},
	}}
	w := &Worker{Cloud: cloud, Store: st, Profile: prof, Interval: 30 * time.Second, Now: func() time.Time { return now }, Sleep: func(time.Duration) {}}
	var se *StatusError
	if err := w.Do(context.Background(), Request{Action: "set", SIID: 2, PIID: 4, Value: 9, Expected: 1}); !errors.As(err, &se) || se.Code != 409 || len(cloud.sets) != 0 {
		t.Fatalf("standby fan err=%v sets=%d", err, len(cloud.sets))
	}
	if err := w.Do(context.Background(), Request{Action: "set", SIID: 2, PIID: 3, Value: 3, Expected: 0}); !errors.As(err, &se) || se.Code != 409 || len(cloud.sets) != 0 {
		t.Fatalf("standby mode err=%v sets=%d", err, len(cloud.sets))
	}
	// Indicator (2.6) is still allowed in standby when readback matches.
	if err := w.Do(context.Background(), Request{Action: "set", SIID: 2, PIID: 6, Value: 0, Expected: -1}); err != nil {
		t.Fatalf("standby indicator err=%v", err)
	}

	// 2. Cached state says Working (1), but device entered Standby and ignores Fan=9 (reads back 1, 2.1=2).
	st.Lock()
	st.State().Props["2.1"] = store.PropSnap{Value: float64(1), At: now}
	st.Unlock()
	err = w.Do(context.Background(), Request{Action: "set", SIID: 2, PIID: 4, Value: 9, Expected: 1})
	if !errors.As(err, &se) || se.Code != 409 || !strings.Contains(err.Error(), "standby") {
		t.Fatalf("expected standby readback mismatch 409, got %v", err)
	}
	st.Lock()
	gotPwr := st.State().Props["2.1"].Value
	st.Unlock()
	if !sameValue(gotPwr, 2) {
		t.Fatalf("expected 2.1 refreshed to 2, got %v", gotPwr)
	}
	rows := st.ActivitySince(now.Add(-time.Hour))
	foundStandby := false
	for _, r := range rows {
		if r.Target == "Fan (2.4)" && r.Code == 409 && strings.Contains(r.Result, "standby") {
			foundStandby = true
		}
	}
	if !foundStandby {
		t.Fatalf("expected standby mismatch in activity log, got %+v", rows)
	}

	// 3. Device is Working (1), cloud returns code=0, but readback stays at 1.
	st.Lock()
	st.State().Props["2.1"] = store.PropSnap{Value: float64(1), At: now}
	st.Unlock()
	cloud.props["2.1"] = dreame.PropResult{SIID: 2, PIID: 1, Code: 0, Has: true, Value: float64(1)}
	err = w.Do(context.Background(), Request{Action: "set", SIID: 2, PIID: 4, Value: 9, Expected: 1})
	if !errors.As(err, &se) || se.Code != 409 || !strings.Contains(err.Error(), "read back 1") {
		t.Fatalf("expected readback mismatch 409, got %v", err)
	}
}

func TestBoolKeyToneRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	st.Lock()
	st.State().Status = "online"
	st.State().Props["2.7"] = store.PropSnap{Value: true, At: now}
	st.Unlock()
	prof := testProfile()
	prof.Properties = append(prof.Properties, dreame.PropSpec{SIID: 2, PIID: 7, Key: "key_tone", JSONType: "bool", Access: "rw", Realized: true})
	prof.Controls.Tone = []int{0, 1}
	cloud := &fakeCloud{props: map[string]dreame.PropResult{
		"2.7": {SIID: 2, PIID: 7, Code: 0, Has: true, Value: false},
	}}
	w := &Worker{Cloud: cloud, Store: st, Profile: prof, Interval: 30 * time.Second, Now: func() time.Time { return now }, Sleep: func(time.Duration) {}}
	if err := w.Do(context.Background(), Request{Action: "set", SIID: 2, PIID: 7, Value: 0, Expected: 1}); err != nil {
		t.Fatal(err)
	}
	if len(cloud.sets) != 1 {
		t.Fatalf("sets %+v", cloud.sets)
	}
	b, ok := cloud.sets[0].Value.(bool)
	if !ok || b {
		t.Fatalf("expected bool false write, got %T %v", cloud.sets[0].Value, cloud.sets[0].Value)
	}
	if got := propInt(map[string]store.PropSnap{"2.7": {Value: true}}, "2.7"); got != 1 {
		t.Fatalf("propInt(bool true) = %d", got)
	}
	if propF(map[string]store.PropSnap{"2.7": {Value: false}}, "2.7") != 0 {
		t.Fatal("propF false")
	}
	st.Lock()
	st.State().Props["2.7"] = store.PropSnap{Value: true, At: now}
	st.Unlock()
	app := NewApp(config.Config{DataDir: dir, Location: time.UTC, PollInterval: 30 * time.Second}, st, dreame.NewManager(""), cloud)
	app.booted.Store(true)
	app.setProfile(*prof)
	snap := app.Page("overview", nil)
	if !snap.ToneKnown || snap.Tone != 1 {
		t.Fatalf("tone %d known %v", snap.Tone, snap.ToneKnown)
	}
}

func TestVerifyKeepsProbeMetadata(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	cloud := &fakeCloud{props: map[string]dreame.PropResult{
		"2.7": {SIID: 2, PIID: 7, Code: -4003, Has: true},
	}}
	app := NewApp(config.Config{DataDir: dir, Location: time.UTC, PollInterval: 30 * time.Second}, st, nil, cloud)
	app.setProfile(dreame.Profile{
		DID: "-1", VerifiedBy: "read-sweep",
		AQLevel:   map[string]string{"1": "Good"},
		HCHOScale: "ppb", Filter2: "carbon",
		Properties: []dreame.PropSpec{{SIID: 2, PIID: 3, Access: "ro", JSONType: "bool"}},
	})
	if _, err := app.verifyLocked(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, err := dreame.LoadProfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.VerifiedBy != "read-sweep" || p.AQLevel["1"] != "Good" || p.HCHOScale != "ppb" || p.Filter2 != "carbon" {
		t.Fatalf("metadata %+v", p)
	}
	if p.Model != "dreame.airp.u2402" || p.DID != "-1" {
		t.Fatalf("model %s did %s", p.Model, p.DID)
	}
	mode, ok := p.Prop("2.3")
	if !ok || mode.Access != "rw" {
		t.Fatalf("sweep access %+v", mode)
	}
	app.setProfile(dreame.Profile{
		DID: "-1", VerifiedBy: "write-probe",
		AQLevel: p.AQLevel, HCHOScale: p.HCHOScale, Filter2: p.Filter2,
		Controls: dreame.Controls{PowerAction: true, FanMin: 1, FanMax: 5, Tone: []int{0, 1}, ContinuousMonitoring: true},
		Properties: []dreame.PropSpec{
			{SIID: 2, PIID: 4, Access: "ro", JSONType: "bool"},
			{SIID: 2, PIID: 7, Key: "key_tone", Access: "ro", JSONType: "bool", Realized: true},
			{SIID: 6, PIID: 15, Key: "continuous_monitoring", Access: "rw", JSONType: "number", Realized: true},
		},
	})
	if _, err := app.verifyLocked(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, err = dreame.LoadProfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.VerifiedBy != "write-probe" || !p.Controls.PowerAction || p.Controls.FanMax != 5 {
		t.Fatalf("controls %+v %s", p.Controls, p.VerifiedBy)
	}
	if p.HCHOScale != "ppb" || p.Filter2 != "carbon" || p.AQLevel["1"] != "Good" {
		t.Fatal("metadata dropped")
	}
	fan, ok := p.Prop("2.4")
	if !ok || fan.Access != "ro" || fan.JSONType != "bool" {
		t.Fatalf("fan %+v", fan)
	}
	tone, ok := p.Prop("2.7")
	if !ok || tone.JSONType != "bool" || tone.Access != "ro" {
		t.Fatalf("tone %+v ok %v", tone, ok)
	}
	cm, ok := p.Prop("6.15")
	if !ok || cm.Access != "rw" {
		t.Fatalf("siid=6 rw prop dropped: %+v ok %v", cm, ok)
	}
}

func TestToggleActionRejectsNonZeroCode(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	st.Lock()
	st.State().Status = "online"
	st.State().Props["2.1"] = store.PropSnap{Value: 1, At: now}
	st.Unlock()
	cloud := &fakeCloud{actCode: -4004}
	w := &Worker{Cloud: cloud, Store: st, Profile: testProfile(), Interval: 30 * time.Second, Now: func() time.Time { return now }, Sleep: func(time.Duration) {}}
	err := w.Do(context.Background(), Request{Action: "toggle_power", Expected: 1})
	if err == nil {
		t.Fatal("expected error for non-zero action code")
	}
	if code, ok := dreame.CodeOf(err); !ok || code != -4004 {
		t.Fatalf("err=%v code=%d ok=%v", err, code, ok)
	}
}

func TestPhaseNilAuthIdentityQuoteAndZeroPollBanner(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	app := NewApp(config.Config{DataDir: dir, Location: time.UTC, PollInterval: 30 * time.Second}, st, nil, nil)
	if got := app.Phase(); got != "boot" {
		t.Fatalf("Phase with nil auth = %s", got)
	}
	rows := identityRows(store.DeviceSnap{Ver: "1.8.17_1070"}, map[string]store.PropSnap{"1.4": {Value: `1070"`}})
	foundBuild := ""
	for _, pair := range rows {
		if pair[0] == "Build" {
			foundBuild = pair[1]
		}
	}
	if foundBuild != "1070" {
		t.Fatalf("Build = %q", foundBuild)
	}
	msg := banner(store.State{Status: "degraded"}, "PM20", time.Now(), time.UTC)
	if msg != "The Dreame cloud reached the device slowly (code 80001). No readings have arrived yet." {
		t.Fatalf("banner = %q", msg)
	}
}

func TestAppEightChartsAndOpenAIAnalyze(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	v4 := 4.0
	v22 := 22.5
	v45 := 45.0
	v180 := 180.0
	for i := 0; i < 3; i++ {
		_ = st.AppendReading(store.Reading{
			TS:   now.Add(-time.Duration(3-i) * time.Minute).Unix(),
			PM25: &v4, PM10: &v4, PM1: &v4, AQ: &v45, T: &v22, RH: &v45, HCHO: &v4, TVOC: &v180,
		})
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "gpt-4o-mini",
			"choices": []any{map[string]any{
				"message": map[string]any{
					"content": `{"status":"good","headline":"Clean air","summary":"All readings look normal.","findings":["Stable PM2.5"],"recommendations":["Keep running"]}`,
				},
			}},
		})
	}))
	defer srv.Close()

	app := NewApp(config.Config{DataDir: dir, Location: time.UTC, PollInterval: 30 * time.Second}, st, nil, nil)
	app.AI = &ai.Client{Endpoint: srv.URL, HTTP: srv.Client()}
	app.booted.Store(true)

	snap := app.Page("overview", nil)
	if len(snap.Charts) != 8 {
		t.Fatalf("expected 8 individual charts, got %d", len(snap.Charts))
	}
	if snap.OpenAIConfigured {
		t.Fatal("should not be configured initially")
	}
	// Analyze without key returns 412.
	if _, err := app.Analyze(context.Background(), web.AIRequest{Scope: "metric", Metric: "pm25", Span: "6h"}); err == nil {
		t.Fatal("expected error when key missing")
	}
	const secret = "sk-proj-secretkey999988887777"
	if err := app.SaveOpenAI(context.Background(), web.OpenAIForm{APIKey: secret, Model: "gpt-4o-mini"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "openai_key.json"))
	if strings.Contains(string(raw), secret) {
		t.Fatal("plaintext OpenAI key stored on disk")
	}
	rep, err := app.Analyze(context.Background(), web.AIRequest{Scope: "metric", Metric: "pm25", Span: "6h"})
	if err != nil || rep.Headline != "Clean air" || calls.Load() != 1 {
		t.Fatalf("rep=%+v err=%v calls=%d", rep, err, calls.Load())
	}
	// Second call hits cache without extra OpenAI request.
	if _, err := app.Analyze(context.Background(), web.AIRequest{Scope: "metric", Metric: "pm25", Span: "6h"}); err != nil || calls.Load() != 1 {
		t.Fatalf("expected cached call, err=%v calls=%d", err, calls.Load())
	}
	// General report works too.
	if _, err := app.Analyze(context.Background(), web.AIRequest{Scope: "general", Span: "6h"}); err != nil || calls.Load() != 2 {
		t.Fatalf("general err=%v calls=%d", err, calls.Load())
	}
	// Daily hourly summary uses 1h buckets, persists to ai_daily.json, and reloads in a fresh App.
	dailyRep, err := app.Analyze(context.Background(), web.AIRequest{Scope: "daily", Span: "24h"})
	if err != nil || calls.Load() != 3 || dailyRep.Scope != "daily" || dailyRep.Span != "24h" {
		t.Fatalf("daily rep=%+v err=%v calls=%d", dailyRep, err, calls.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, "ai_daily.json")); err != nil {
		t.Fatalf("expected persisted ai_daily.json: %v", err)
	}
	app2 := NewApp(config.Config{DataDir: dir, Location: time.UTC, PollInterval: 30 * time.Second}, st, nil, nil)
	app2.AI = &ai.Client{Endpoint: srv.URL, HTTP: srv.Client()}
	app2.booted.Store(true)
	if rep2, ok := app2.CachedReport("daily", "", "24h"); !ok || rep2.Headline != "Clean air" {
		t.Fatalf("expected reloaded daily report, ok=%v rep=%+v", ok, rep2)
	}
	if snap2 := app2.Page("overview", nil); snap2.DailyAI == nil || snap2.DailyAI.Headline != "Clean air" {
		t.Fatalf("expected DailyAI in overview snapshot: %+v", snap2.DailyAI)
	}
	// Delete clears key and daily report file.
	if err := app.SaveOpenAI(context.Background(), web.OpenAIForm{Delete: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ai_daily.json")); !os.IsNotExist(err) {
		t.Fatalf("expected ai_daily.json removed on key delete, err=%v", err)
	}
	if app.Page("device", nil).OpenAIConfigured {
		t.Fatal("key still configured after delete")
	}
}

func TestDailyHourlySummaryGranularity(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	loc := time.FixedZone("EST", -5*3600)
	now := time.Date(2026, 11, 23, 10, 20, 0, 0, loc)
	hchoRaw := 40.0 // 0.04 mg/m3
	hchoOld := 130.0
	pm25a, pm25b := 2.0, 6.0
	tC, rh, tvoc, aq := 24.0, 42.0, 120.0, 15.0
	// Yesterday evening spike (must be excluded once past 01:00 today).
	yest := time.Date(2026, 11, 22, 21, 15, 0, 0, loc)
	_ = st.AppendReading(store.Reading{TS: yest.Unix(), PM25: &pm25b, HCHO: &hchoOld, T: &tC, RH: &rh, TVOC: &tvoc, AQ: &aq})
	for h := 3; h >= 0; h-- {
		base := now.Add(-time.Duration(h) * time.Hour)
		_ = st.AppendReading(store.Reading{TS: base.Add(-10 * time.Minute).Unix(), PM25: &pm25a, HCHO: &hchoRaw, T: &tC, RH: &rh, TVOC: &tvoc, AQ: &aq})
		_ = st.AppendReading(store.Reading{TS: base.Add(-5 * time.Minute).Unix(), PM25: &pm25b, HCHO: &hchoRaw, T: &tC, RH: &rh, TVOC: &tvoc, AQ: &aq})
	}
	st.Lock()
	st.State().Props["3.5"] = store.PropSnap{Value: 2.0, At: now}
	st.State().Props["3.7"] = store.PropSnap{Value: 37.0, At: now}
	st.Unlock()

	app := NewApp(config.Config{DataDir: dir, Location: loc, PollInterval: 30 * time.Second}, st, nil, nil)
	in := app.buildDailyAnalyzeInputAt(now)
	if in.Scope != "daily" || in.Span != "24h" || in.Granularity != "1h" {
		t.Fatalf("unexpected input header: %+v", in)
	}
	if len(in.Metrics) != 8 {
		t.Fatalf("expected 8 metrics, got %d", len(in.Metrics))
	}
	var pm, hcho ai.MetricInput
	for _, m := range in.Metrics {
		if m.ID == "pm25" {
			pm = m
		}
		if m.ID == "hcho" {
			hcho = m
		}
	}
	if pm.Current != "2 µg/m³" || len(pm.Points) < 3 {
		t.Fatalf("unexpected pm25 hourly input: %+v", pm)
	}
	if hcho.Current != "0.037 mg/m³" || hcho.Peak != "0.04 mg/m³" {
		t.Fatalf("expected yesterday 0.13 spike excluded and today peak 0.04, got: %+v", hcho)
	}
	// Right after midnight (< 1h into new day), falls back to last 2 hours.
	early := time.Date(2026, 11, 23, 0, 15, 0, 0, loc)
	_ = st.AppendReading(store.Reading{TS: time.Date(2026, 11, 22, 23, 30, 0, 0, loc).Unix(), PM25: &pm25a})
	inEarly := app.buildDailyAnalyzeInputAt(early)
	var pmEarly ai.MetricInput
	for _, m := range inEarly.Metrics {
		if m.ID == "pm25" {
			pmEarly = m
		}
	}
	if len(pmEarly.Points) == 0 {
		t.Fatalf("expected midnight fallback to include recent overnight bucket: %+v", pmEarly)
	}
}

func TestDailySummaryTriggersOnMetricChange(t *testing.T) {
	base := metricSnap{
		Power: 1,
		Values: map[string]float64{
			"pm25": 1, "pm10": 1, "pm1": 1, "aq": 11,
			"t": 25, "rh": 40, "hcho": 0.031, "tvoc": 29,
		},
	}
	// Normal small sensor jitter must NOT trigger.
	jitter := metricSnap{
		Power: 1,
		Values: map[string]float64{
			"pm25": 2, "pm10": 3, "pm1": 2, "aq": 13,
			"t": 25, "rh": 41, "hcho": 0.026, "tvoc": 32,
		},
	}
	if changed, reason := detectMetricChanges(base, jitter); len(changed) != 0 {
		t.Fatalf("expected jitter ignored, got changed=%v reason=%q", changed, reason)
	}
	// Threshold crossing (Temperature 25 -> 26 °C) MUST trigger.
	warm := metricSnap{Power: 1, Values: map[string]float64{"t": 26, "pm25": 1}}
	if changed, _ := detectMetricChanges(base, warm); len(changed) != 1 || changed[0] != "t" {
		t.Fatalf("expected temp band crossing trigger, got %v", changed)
	}
	// Large within-band surge (already high PM2.5 70 -> 548) MUST trigger.
	highBase := metricSnap{Power: 1, Values: map[string]float64{"pm25": 70}}
	highCur := metricSnap{Power: 1, Values: map[string]float64{"pm25": 548}}
	if changed, _ := detectMetricChanges(highBase, highCur); len(changed) != 1 || changed[0] != "pm25" {
		t.Fatalf("expected large pm25 surge trigger, got %v", changed)
	}
	// Standby powered-off zeros must NOT register as pm25/t/rh drops.
	pwr2 := 2
	z := 0.0
	aq15 := 15.0
	standbySnap := metricSnapFromReading(store.Reading{Pwr: &pwr2, PM25: &z, T: &z, RH: &z, HCHO: &z, AQ: &aq15})
	if _, hasPM := standbySnap.Values["pm25"]; hasPM {
		t.Fatalf("standby reading should omit powered-off pm25: %+v", standbySnap)
	}

	// End-to-end NoteReading queues trigger, invalidates changed per-metric cache, and persists baseline.
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.Lock()
	st.State().Props["2.1"] = store.PropSnap{Value: 1.0, At: time.Now()}
	st.State().Props["3.5"] = store.PropSnap{Value: 1.0, At: time.Now()}
	st.State().Props["3.8"] = store.PropSnap{Value: 29.0, At: time.Now()}
	st.Unlock()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "gpt-4.1",
			"choices": []any{map[string]any{
				"message": map[string]any{"content": `{"summary":"Particles surged quickly right now."}`},
			}},
		})
	}))
	defer srv.Close()

	app := NewApp(config.Config{DataDir: dir, Location: time.UTC, PollInterval: 30 * time.Second}, st, nil, nil)
	app.AI = &ai.Client{Endpoint: srv.URL, HTTP: srv.Client()}
	app.booted.Store(true)
	if err := app.SaveOpenAI(context.Background(), web.OpenAIForm{APIKey: "sk-proj-testkey1234567890abcd", Model: "gpt-4.1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Analyze(context.Background(), web.AIRequest{Scope: "daily", Span: "24h"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Analyze(context.Background(), web.AIRequest{Scope: "metric", Metric: "pm25", Span: "6h"}); err != nil {
		t.Fatal(err)
	}
	// Reload in a fresh App and verify baseline persisted in ai_daily.json.
	app2 := NewApp(config.Config{DataDir: dir, Location: time.UTC, PollInterval: 30 * time.Second}, st, nil, nil)
	if app2.aiBaseline.Values["pm25"] != 1 {
		t.Fatalf("expected persisted pm25 baseline=1, got %+v", app2.aiBaseline)
	}
	// Live PM2.5 spike queues trigger and clears stale pm25 metric cache.
	pwr1 := 1
	pmSpike := 42.0
	app.NoteReading(store.Reading{Pwr: &pwr1, PM25: &pmSpike})
	if trig := app.dailyTrigger(); !strings.Contains(trig, "pm25") {
		t.Fatalf("expected pm25 trigger queued, got %q", trig)
	}
	if _, cached := app.CachedReport("metric", "pm25", "6h"); cached {
		t.Fatal("expected stale pm25 metric report invalidated on spike")
	}

	// Cooldown escalates on consecutive spike refreshes and resets on hourly refresh.
	if changeCooldownFor(0) != 10*time.Minute || changeCooldownFor(1) != 10*time.Minute || changeCooldownFor(2) != 15*time.Minute {
		t.Fatalf("unexpected cooldowns: %v %v %v", changeCooldownFor(0), changeCooldownFor(1), changeCooldownFor(2))
	}
	// Triggered refresh increments streak and persists it across reload.
	if _, err := app.Analyze(context.Background(), web.AIRequest{Scope: "daily", Span: "24h", Force: true}); err != nil {
		t.Fatal(err)
	}
	if app.dailyChangeStreak() != 1 {
		t.Fatalf("expected change streak=1, got %d", app.dailyChangeStreak())
	}
	app3 := NewApp(config.Config{DataDir: dir, Location: time.UTC, PollInterval: 30 * time.Second}, st, nil, nil)
	if app3.dailyChangeStreak() != 1 {
		t.Fatalf("expected persisted change streak=1, got %d", app3.dailyChangeStreak())
	}
	// aiDailyLoop honors cooldown and does not re-call OpenAI immediately on another spike.
	var calls atomic.Int32
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "gpt-4.1",
			"choices": []any{map[string]any{"message": map[string]any{"content": `{"summary":"Updated."}`}}},
		})
	}))
	defer srv2.Close()
	app.AI = &ai.Client{Endpoint: srv2.URL, HTTP: srv2.Client()}
	pmSpike2 := 180.0
	st.Lock()
	st.State().Props["3.5"] = store.PropSnap{Value: pmSpike2, At: time.Now()}
	st.Unlock()
	app.NoteReading(store.Reading{Pwr: &pwr1, PM25: &pmSpike2})
	loopCtx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	app.aiDailyLoop(loopCtx)
	if calls.Load() != 0 {
		t.Fatalf("expected spike refresh held by cooldown, got %d calls", calls.Load())
	}
}

func pm20Profile() *dreame.Profile {
	return &dreame.Profile{
		DID: "-1", Model: "dreame.airp.u2402", VerifiedBy: "write-probe",
		Properties: []dreame.PropSpec{
			{SIID: 2, PIID: 1, Key: "power", JSONType: "number", Access: "ro", Realized: true},
			{SIID: 2, PIID: 3, Key: "mode", JSONType: "number", Access: "rw", Realized: true},
			{SIID: 2, PIID: 4, Key: "fan_speed", JSONType: "number", Access: "rw", Realized: true},
			{SIID: 2, PIID: 5, Key: "humidity_target", JSONType: "number", Access: "ro", Realized: true},
			{SIID: 2, PIID: 6, Key: "heater_temp", JSONType: "number", Access: "rw", Realized: true},
			{SIID: 2, PIID: 7, Key: "oscillation", JSONType: "number", Access: "rw", Realized: true},
			{SIID: 6, PIID: 15, Key: "continuous_monitoring", JSONType: "number", Access: "rw", Realized: true},
			{SIID: 6, PIID: 19, Key: "nearby_power_on", JSONType: "number", Access: "rw", Realized: true},
			{SIID: 6, PIID: 20, Key: "follow_motion", JSONType: "number", Access: "rw", Realized: true},
		},
		Controls: dreame.Controls{
			Mode:                 []int{0, 5, 3, 4},
			FanMin:               1,
			FanMax:               10,
			FanForcesCustom:      true,
			HeaterMin:            18,
			HeaterMax:            40,
			Oscillation:          []int{0, 45, 90, 180},
			ContinuousMonitoring: true,
			NearbyPowerOn:        true,
			FollowMotion:         true,
			PowerAction:          true,
		},
	}
}

func TestPM20ControlsPowerActionAndStandbyGuard(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	st.Lock()
	st.State().Status = "online"
	st.State().Props["2.1"] = store.PropSnap{Value: float64(1), At: now}
	st.State().Props["6.20"] = store.PropSnap{Value: float64(1), At: now}
	st.Unlock()
	cloud := &fakeCloud{
		actMutate: true,
		props: map[string]dreame.PropResult{
			"2.1":  {SIID: 2, PIID: 1, Code: 0, Has: true, Value: float64(1)},
			"2.3":  {SIID: 2, PIID: 3, Code: 0, Has: true, Value: float64(5)},
			"2.6":  {SIID: 2, PIID: 6, Code: 0, Has: true, Value: float64(22)},
			"2.7":  {SIID: 2, PIID: 7, Code: 0, Has: true, Value: float64(90)},
			"6.15": {SIID: 6, PIID: 15, Code: 0, Has: true, Value: float64(1)},
			"6.19": {SIID: 6, PIID: 19, Code: 0, Has: true, Value: float64(1)},
			"6.20": {SIID: 6, PIID: 20, Code: 0, Has: true, Value: float64(0)},
		},
	}
	prof := pm20Profile()
	w := &Worker{Cloud: cloud, Store: st, Profile: prof, Interval: 30 * time.Second, Now: func() time.Time { return now }, Sleep: func(time.Duration) {}}

	// 1. Working -> Standby uses Action(2, 1, [{"piid": 1, "value": 0}]) and verifies 2.1 == 2.
	if err := w.Do(context.Background(), Request{Action: "toggle_power", Expected: 1}); err != nil {
		t.Fatalf("working->standby err=%v", err)
	}
	if len(cloud.actCalls) != 1 || cloud.actCalls[0].SIID != 2 || cloud.actCalls[0].AIID != 1 {
		t.Fatalf("unexpected action call: %+v", cloud.actCalls)
	}
	arg0, _ := cloud.actCalls[0].In[0].(map[string]any)
	if arg0["piid"] != 1 || arg0["value"] != 0 {
		t.Fatalf("expected piid=1 value=0 for standby, got %+v", arg0)
	}

	// 2. In Standby: Mode, Fan, Heater, Oscillation, Follow motion blocked with 409; 6.15 and 6.19 allowed.
	var se *StatusError
	for _, blocked := range []Request{
		{Action: "set", SIID: 2, PIID: 3, Value: 5},
		{Action: "set", SIID: 2, PIID: 4, Value: 4},
		{Action: "set", SIID: 2, PIID: 6, Value: 22},
		{Action: "set", SIID: 2, PIID: 7, Value: 90},
		{Action: "set", SIID: 6, PIID: 20, Value: 1},
	} {
		if err := w.Do(context.Background(), blocked); !errors.As(err, &se) || se.Code != 409 {
			t.Fatalf("expected 409 in standby for %d.%d, got %v", blocked.SIID, blocked.PIID, err)
		}
	}
	if err := w.Do(context.Background(), Request{Action: "set", SIID: 6, PIID: 15, Value: 1, Expected: 0}); err != nil {
		t.Fatalf("continuous monitoring should be allowed in standby: %v", err)
	}
	if err := w.Do(context.Background(), Request{Action: "set", SIID: 6, PIID: 19, Value: 1, Expected: 0}); err != nil {
		t.Fatalf("nearby power-on should be allowed in standby: %v", err)
	}

	// 3. Standby -> Working uses Action(2, 1, [{"piid": 1, "value": 1}]) and verifies 2.1 == 1.
	if err := w.Do(context.Background(), Request{Action: "toggle_power", Expected: 2}); err != nil {
		t.Fatalf("standby->working err=%v", err)
	}
	arg1, _ := cloud.actCalls[1].In[0].(map[string]any)
	if arg1["piid"] != 1 || arg1["value"] != 1 {
		t.Fatalf("expected piid=1 value=1 for turn on, got %+v", arg1)
	}

	// 4. Working PM20 writes: Comfort (5), Heater (22, rejects out-of-range 15), Oscillation clears Follow motion (6.20=0).
	if err := w.Do(context.Background(), Request{Action: "set", SIID: 2, PIID: 3, Value: 5, Expected: 0}); err != nil {
		t.Fatalf("comfort mode err=%v", err)
	}
	if err := w.Do(context.Background(), Request{Action: "set", SIID: 2, PIID: 6, Value: 22, Expected: -1}); err != nil {
		t.Fatalf("heater 22 err=%v", err)
	}
	if err := w.Do(context.Background(), Request{Action: "set", SIID: 2, PIID: 6, Value: 15, Expected: -1}); !errors.As(err, &se) || se.Code != 400 {
		t.Fatalf("expected 400 for heater=15, got %v", err)
	}
	if err := w.Do(context.Background(), Request{Action: "set", SIID: 2, PIID: 5, Value: 80, Expected: -1}); !errors.As(err, &se) || se.Code != 400 {
		t.Fatalf("expected 400 for unused 2.5 on PM20, got %v", err)
	}
	cloud.sets = nil
	if err := w.Do(context.Background(), Request{Action: "set", SIID: 2, PIID: 7, Value: 90, Expected: 0}); err != nil {
		t.Fatalf("oscillation err=%v", err)
	}
	if len(cloud.sets) != 2 || cloud.sets[0].SIID != 6 || cloud.sets[0].PIID != 20 || cloud.sets[0].Value != 0 || cloud.sets[1].SIID != 2 || cloud.sets[1].PIID != 7 {
		t.Fatalf("expected follow motion cleared before oscillation write, got %+v", cloud.sets)
	}

	// 5. Toggle mismatch when device refuses to change power state returns 409.
	cloud.actMutate = false
	if err := w.Do(context.Background(), Request{Action: "toggle_power", Expected: 1}); !errors.As(err, &se) || se.Code != 409 {
		t.Fatalf("expected 409 when power state did not flip, got %v", err)
	}
}

func TestAIHysteresisAndStandbySummaryInput(t *testing.T) {
	// 1. Boundary hysteresis suppresses 1-unit wobble across thresholds.
	base := metricSnap{Power: 1, Values: map[string]float64{"rh": 35, "tvoc": 199, "hcho": 0.049}}
	wobble := metricSnap{Power: 1, Values: map[string]float64{"rh": 34, "tvoc": 202, "hcho": 0.051}}
	if changed, reason := detectMetricChanges(base, wobble); len(changed) != 0 {
		t.Fatalf("expected boundary wobble ignored, got changed=%v reason=%q", changed, reason)
	}
	realDrop := metricSnap{Power: 1, Values: map[string]float64{"rh": 33, "tvoc": 215, "hcho": 0.055}}
	changed, _ := detectMetricChanges(base, realDrop)
	if len(changed) != 3 {
		t.Fatalf("expected real crossings to trigger for rh/tvoc/hcho, got %v", changed)
	}

	// 2. Standby input marks powered-off sensors as "paused in standby" rather than reusing stale history.
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	pmOld, tOld, rhOld, tvocLive, aqLive := 5.0, 23.0, 44.0, 145.0, 12.0
	_ = st.AppendReading(store.Reading{TS: now.Add(-2 * time.Hour).Unix(), PM25: &pmOld, T: &tOld, RH: &rhOld, TVOC: &tvocLive, AQ: &aqLive})
	st.Lock()
	st.State().Props["2.1"] = store.PropSnap{Value: 2.0, At: now}
	st.State().Props["2.3"] = store.PropSnap{Value: 5.0, At: now}
	st.State().Props["3.5"] = store.PropSnap{Value: 0.0, At: now}
	st.State().Props["3.3"] = store.PropSnap{Value: 0.0, At: now}
	st.State().Props["3.2"] = store.PropSnap{Value: 0.0, At: now}
	st.State().Props["3.8"] = store.PropSnap{Value: 145.0, At: now}
	st.State().Props["3.12"] = store.PropSnap{Value: 12.0, At: now}
	st.Unlock()
	app := NewApp(config.Config{DataDir: dir, Location: time.UTC, PollInterval: 30 * time.Second}, st, nil, nil)
	in := app.buildDailyAnalyzeInputAt(now)
	if in.PowerState != "Standby" || in.Mode != "Comfort" {
		t.Fatalf("unexpected device context: power=%q mode=%q", in.PowerState, in.Mode)
	}
	byID := map[string]ai.MetricInput{}
	for _, m := range in.Metrics {
		byID[m.ID] = m
	}
	if byID["pm25"].Current != "paused in standby" || byID["t"].Current != "paused in standby" || byID["rh"].Current != "paused in standby" {
		t.Fatalf("expected paused in standby for powered-off sensors, got pm25=%q t=%q rh=%q", byID["pm25"].Current, byID["t"].Current, byID["rh"].Current)
	}
	if byID["tvoc"].Current != "145 µg/m³" || byID["aq"].Current != "12 %" {
		t.Fatalf("expected live tvoc/aq in standby, got tvoc=%q aq=%q", byID["tvoc"].Current, byID["aq"].Current)
	}
}

func TestOTAVersionCheckAndConfirmedUpdate(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.Lock()
	st.State().Status = "online"
	st.State().Device.Ver = "1.8.17_1070"
	st.Unlock()
	cloud := &fakeCloud{
		otaVer: dreame.OTAVersion{CurVersion: "1.8.17_1070", HasNew: false},
	}
	app := NewApp(config.Config{DataDir: dir, Location: time.UTC, PollInterval: 30 * time.Second}, st, nil, cloud)
	app.booted.Store(true)
	app.setProfile(*pm20Profile())

	// 1. Up-to-date check records CheckedAt and flashes up-to-date status.
	if err := app.CheckOTA(context.Background()); err != nil {
		t.Fatal(err)
	}
	snap := app.Page("device", nil)
	if snap.OTAVersion != "1.8.17_1070" || snap.OTAHasUpdate || snap.OTAChecked == "" || !strings.Contains(snap.Flash, "up to date") {
		t.Fatalf("unexpected up-to-date OTA snap: ver=%q hasNew=%v checked=%q flash=%q", snap.OTAVersion, snap.OTAHasUpdate, snap.OTAChecked, snap.Flash)
	}
	// Starting an update when none is available returns 409.
	var se *StatusError
	if err := app.StartOTAUpdate(context.Background()); !errors.As(err, &se) || se.Code != 409 {
		t.Fatalf("expected 409 when no update available, got %v", err)
	}

	// 2. When a new MCU firmware is published, CheckOTA surfaces it and StartOTAUpdate triggers manualFirmwareUpdateV2.
	cloud.otaVer = dreame.OTAVersion{
		CurVersion: "1.8.17_1070", NewVersion: "1.8.17_1075", HasNew: true,
		Target: "MCU", Status: 1, Description: "Airflow calibration",
	}
	if err := app.CheckOTA(context.Background()); err != nil {
		t.Fatal(err)
	}
	snap2 := app.Page("device", nil)
	if !snap2.OTAHasUpdate || snap2.OTANewVersion != "1.8.17_1075" || snap2.OTATarget != "MCU" || snap2.OTADescription != "Airflow calibration" {
		t.Fatalf("unexpected update-available OTA snap: %+v", snap2)
	}
	// Read-only blocks firmware update with 403.
	ro := true
	st.Lock()
	st.State().Overrides.ReadOnly = &ro
	st.Unlock()
	if err := app.StartOTAUpdate(context.Background()); !errors.As(err, &se) || se.Code != 403 || len(cloud.otaUpdates) != 0 {
		t.Fatalf("expected 403 in read-only, got %v updates=%v", err, cloud.otaUpdates)
	}
	st.Lock()
	st.State().Overrides.ReadOnly = nil
	st.Unlock()

	if err := app.StartOTAUpdate(context.Background()); err != nil {
		t.Fatalf("StartOTAUpdate err=%v", err)
	}
	if len(cloud.otaUpdates) != 1 || cloud.otaUpdates[0] != "MCU" {
		t.Fatalf("expected MCU update triggered, got %v", cloud.otaUpdates)
	}
	if app.Page("device", nil).OTAState != "downloading" {
		t.Fatalf("expected OTAState=downloading, got %q", app.Page("device", nil).OTAState)
	}
}
