package dreame

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

type memDev struct {
	vals   map[string]any
	sets   []PropWrite
	failAt int
	failed bool
	cancel context.CancelFunc
	acts   int
}

func newMem() *memDev {
	m := &memDev{vals: map[string]any{}}
	for i := 1; i <= 30; i++ {
		m.vals[fmt.Sprintf("2.%d", i)] = 0
	}
	m.vals["2.1"] = 1
	m.vals["2.3"] = 0
	m.vals["2.4"] = 2
	m.vals["2.5"] = 80
	m.vals["2.6"] = 0
	m.vals["2.7"] = 1
	return m
}

func (m *memDev) GetProperties(_ context.Context, _ string, refs []PropRef) ([]PropResult, error) {
	var out []PropResult
	for _, r := range refs {
		k := r.Key()
		v, ok := m.vals[k]
		if !ok {
			out = append(out, PropResult{SIID: r.SIID, PIID: r.PIID, Code: -4003, Has: true})
			continue
		}
		out = append(out, PropResult{SIID: r.SIID, PIID: r.PIID, Code: 0, Has: true, Value: v})
	}
	return out, nil
}

func (m *memDev) SetProperties(ctx context.Context, _ string, writes []PropWrite) error {
	m.sets = append(m.sets, writes...)
	if m.failAt > 0 && !m.failed && len(m.sets) >= m.failAt {
		m.failed = true
		if m.cancel != nil {
			m.cancel()
		}
		return fmt.Errorf("injected")
	}
	for _, w := range writes {
		if w.SIID == 2 && w.PIID == 4 {
			if mode, _ := m.vals["2.3"].(int); mode == 0 {
				m.vals["2.3"] = 3
			}
		}
		m.vals[fmt.Sprintf("%d.%d", w.SIID, w.PIID)] = w.Value
	}
	return nil
}

func (m *memDev) Action(_ context.Context, _ string, siid, aiid int, in []any) (int, error) {
	m.acts++
	if siid == 2 && aiid == 3 {
		return -4004, fmt.Errorf("forbidden resetMessage Action(2, 3)")
	}
	if siid == 2 && aiid == 1 && len(in) > 0 {
		if arg, ok := in[0].(map[string]any); ok {
			if v, is := asInt(arg["value"]); is {
				if v == 1 {
					m.vals["2.1"] = 1
				} else {
					m.vals["2.1"] = 2
				}
			}
		}
	}
	return 0, nil
}

func TestProbeRestoresOnCancel(t *testing.T) {
	m := newMem()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.failAt = 3
	_, err := ProbeWrites(ctx, m, "-1", Profile{DID: "-1"}, func(time.Duration) {})
	if err == nil {
		t.Fatal("expected cancel")
	}
	if m.vals["2.3"] != 0 || m.vals["2.4"] != 2 || m.vals["2.5"] != 80 || m.vals["2.1"] != 1 {
		t.Fatalf("not restored %+v", m.vals)
	}
}

func TestProbeRecordsBounds(t *testing.T) {
	m := newMem()
	res, err := ProbeWrites(context.Background(), m, "-1", Profile{DID: "-1"}, func(time.Duration) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Profile.VerifiedBy != "write-probe" || !res.Profile.Controls.PowerAction || m.acts != 2 {
		t.Fatalf("controls=%+v acts=%d", res.Profile.Controls, m.acts)
	}
	if res.Profile.Controls.FanMax < 1 || !res.Profile.Controls.FanForcesCustom {
		t.Fatalf("%+v", res.Profile.Controls)
	}
	if m.vals["2.1"] != 1 || m.vals["2.3"] != 0 {
		t.Fatalf("left dirty %+v", m.vals)
	}
}

func TestProbePM20Controls(t *testing.T) {
	m := newMem()
	m.vals["2.5"] = -1
	m.vals["2.6"] = -1
	m.vals["2.7"] = 0
	m.vals["6.15"] = 0
	m.vals["6.19"] = 0
	m.vals["6.20"] = 0
	res, err := ProbeWrites(context.Background(), m, "-1", Profile{DID: "-1", Model: "dreame.airp.u2402"}, func(time.Duration) {})
	if err != nil {
		t.Fatal(err)
	}
	c := res.Profile.Controls
	if !c.IsPM20() || !c.PowerAction || c.HeaterMin != 18 || c.HeaterMax != 40 {
		t.Fatalf("unexpected pm20 controls: %+v", c)
	}
	if len(c.Oscillation) != 4 || !c.ContinuousMonitoring || !c.NearbyPowerOn || !c.FollowMotion {
		t.Fatalf("missing pm20 capabilities: %+v", c)
	}
	if len(c.Volume) != 0 || len(c.Light) != 0 || len(c.Tone) != 0 {
		t.Fatalf("legacy controls should be cleared on PM20: %+v", c)
	}
	for _, w := range m.sets {
		if w.SIID == 2 && w.PIID == 6 {
			if n, _ := asInt(w.Value); n != -1 {
				t.Fatalf("probe must not energize heater, wrote %v", w.Value)
			}
		}
		if w.SIID == 2 && w.PIID == 3 {
			if n, _ := asInt(w.Value); n == 1 {
				t.Fatalf("probe must not write mode=1 on PM20")
			}
		}
	}
	if m.vals["2.1"] != 1 || m.vals["2.3"] != 0 || m.vals["2.6"] != -1 || m.vals["2.7"] != 0 || m.vals["6.15"] != 0 {
		t.Fatalf("pm20 left dirty: %+v", m.vals)
	}
}

func TestProbeBoolTone(t *testing.T) {
	m := newMem()
	m.vals["2.7"] = true
	res, err := ProbeWrites(context.Background(), m, "-1", Profile{DID: "-1"}, func(time.Duration) {})
	if err != nil {
		t.Fatal(err)
	}
	var tone PropSpec
	found := false
	for _, spec := range res.Profile.Properties {
		if spec.SIID == 2 && spec.PIID == 7 {
			tone = spec
			found = true
		}
	}
	if !found || tone.JSONType != "bool" || tone.Access != "rw" {
		t.Fatalf("%+v", tone)
	}
	if len(res.Profile.Controls.Tone) != 2 {
		t.Fatalf("%v", res.Profile.Controls.Tone)
	}
	if m.vals["2.7"] != true || m.vals["2.1"] != 1 || m.vals["2.3"] != 0 || m.vals["2.4"] != 2 || m.vals["2.5"] != 80 {
		t.Fatalf("left dirty %+v", m.vals)
	}
}

func TestVerifyMap(t *testing.T) {
	m := newMem()
	m.vals["3.5"] = float64(4)
	m.vals["1.4"] = "1070"
	p, diff, err := VerifyMap(context.Background(), m, "-1")
	if err != nil {
		t.Fatal(err)
	}
	if p.VerifiedBy != "read-sweep" {
		t.Fatal(p.VerifiedBy)
	}
	found := false
	for _, s := range p.Properties {
		if s.Key == "pm25" && s.JSONType == "number" {
			found = true
		}
		if s.SIID == 3 && s.PIID == 1 {
			t.Fatal("unrealized included")
		}
	}
	if !found {
		t.Fatal(diff)
	}
	if !strings.Contains(diff, "missing=") {
		t.Fatal(diff)
	}
}

type flakySweepDev struct {
	vals    map[string]any
	timeout map[string]bool
	calls   int
}

func (f *flakySweepDev) GetProperties(_ context.Context, _ string, refs []PropRef) ([]PropResult, error) {
	f.calls++
	for _, r := range refs {
		if f.timeout[r.Key()] {
			return nil, &APIError{Op: "get_properties", Code: 80001, Msg: "timeout", Temporary: true}
		}
	}
	out := make([]PropResult, 0, len(refs))
	for _, r := range refs {
		if v, ok := f.vals[r.Key()]; ok {
			out = append(out, PropResult{SIID: r.SIID, PIID: r.PIID, Code: 0, Has: true, Value: v})
		} else {
			out = append(out, PropResult{SIID: r.SIID, PIID: r.PIID, Code: -4003, Has: true})
		}
	}
	return out, nil
}

func TestVerifyMapSkips80001AndProbesBaseline(t *testing.T) {
	dev := &flakySweepDev{
		vals: map[string]any{
			"1.4": "1070\"",
			"2.1": 1,
			"3.5": float64(4),
			"4.1": float64(100),
			"4.2": float64(718),
			"4.3": float64(100),
			"4.4": float64(358),
		},
		timeout: map[string]bool{"4.10": true, "6.14": true},
	}
	p, diff, err := VerifyMap(context.Background(), dev, "-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1.4", "2.1", "3.5", "4.1", "4.2", "4.3", "4.4"} {
		if _, ok := p.Prop(want); !ok {
			t.Fatalf("missing recovered prop %s in %s", want, diff)
		}
	}
	if _, ok := p.Prop("6.14"); ok {
		t.Fatal("timed-out 6.14 included")
	}
	if !strings.Contains(diff, "timeout=6.14") {
		t.Fatalf("expected timeout note in %s", diff)
	}
	allDown := &flakySweepDev{timeout: map[string]bool{}}
	for _, r := range SweepRefs() {
		allDown.timeout[r.Key()] = true
	}
	if _, _, err := VerifyMap(context.Background(), allDown, "-1"); err == nil {
		t.Fatal("expected error when zero properties respond")
	}
}
