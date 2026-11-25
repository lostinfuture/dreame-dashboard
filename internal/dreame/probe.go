package dreame

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ProbeAPI is the slice of the gateway client probe-writes needs.
type ProbeAPI interface {
	GetProperties(context.Context, string, []PropRef) ([]PropResult, error)
	SetProperties(context.Context, string, []PropWrite) error
	Action(context.Context, string, int, int, []any) (int, error)
}

// ProbeResult is the write-probe outcome merged onto a profile.
type ProbeResult struct {
	Profile Profile
	Log     string
}

// ProbeWrites snapshots writable properties, tries candidate values, and always restores.
func ProbeWrites(ctx context.Context, api ProbeAPI, did string, base Profile, sleep func(time.Duration)) (ProbeResult, error) {
	if sleep == nil {
		sleep = time.Sleep
	}
	snap, err := api.GetProperties(ctx, did, siid2Refs())
	if err != nil {
		return ProbeResult{}, err
	}
	orig := map[int]any{}
	for _, row := range snap {
		if row.Code == 0 || row.Value != nil {
			orig[row.PIID] = row.Value
		}
	}
	orig6 := map[int]any{}
	if snap6, err6 := api.GetProperties(ctx, did, pm20SIID6Refs()); err6 == nil {
		for _, row := range snap6 {
			if row.Code == 0 && row.Value != nil {
				orig6[row.PIID] = row.Value
			}
		}
	}
	isPM20 := isPM20Profile(base, orig6)

	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		rctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		// Ensure power is restored first if we need to restore siid 2 settings.
		if wantPwr, ok := asInt(orig[1]); ok && (wantPwr == 1 || wantPwr == 2) {
			if cur, rerr := readOne(rctx, api, did, 2, 1); rerr == nil {
				if curPwr, is := asInt(cur); is && curPwr != wantPwr {
					inVal := 1
					if wantPwr == 2 {
						inVal = 0
					}
					_, _ = api.Action(rctx, did, 2, 1, []any{map[string]any{"piid": 1, "value": inVal}})
					sleep(time.Second)
				}
			}
		}
		// Mode is last. A fan write while mode is automatic forces custom mode.
		piids := []int{4, 5, 6, 7, 3}
		if isPM20 {
			piids = []int{4, 6, 7, 3}
		}
		for _, piid := range piids {
			if v, ok := orig[piid]; ok {
				_ = api.SetProperties(rctx, did, []PropWrite{{SIID: 2, PIID: piid, Value: v}})
			}
		}
		for _, piid := range []int{15, 19, 20} {
			if v, ok := orig6[piid]; ok {
				_ = api.SetProperties(rctx, did, []PropWrite{{SIID: 6, PIID: piid, Value: v}})
			}
		}
	}
	defer restore()

	var logb []string
	note := func(s string) { logb = append(logb, s) }

	var modeOK []int
	if isPM20 {
		// On dreame.airp.u2402 fw >= 1069: 0=AI, 5=Comfort, 3=Custom, 4=Pet. Never probe 1.
		modeOK = tryIntsSIID(ctx, api, did, 2, 3, []int{0, 5, 3, 4}, []int{2, -1, 99}, sleep, note)
	} else {
		modeOK = tryIntsSIID(ctx, api, did, 2, 3, []int{0, 1, 2, 3, 4}, []int{5, -1, 99}, sleep, note)
	}
	if err := ctx.Err(); err != nil {
		return ProbeResult{Log: joinLines(logb)}, err
	}
	fanOK, coupled, auto := tryFan(ctx, api, did, sleep, note)
	if err := ctx.Err(); err != nil {
		return ProbeResult{Log: joinLines(logb)}, err
	}

	var volOK, lightOK, toneOK, oscOK []int
	var heaterMin, heaterMax int
	var cmOK, nearOK, followOK bool
	if isPM20 {
		// 2.5 is unused humidity_target on PM20 (u2403 only).
		// 2.6 is the PTC heater target (-1=Off, 18..40 C). Do not energize the heater during probe.
		if v, ok := asInt(orig[6]); ok {
			if err := api.SetProperties(ctx, did, []PropWrite{{SIID: 2, PIID: 6, Value: v}}); err == nil {
				heaterMin, heaterMax = 18, 40
				note("2.6 heater verified safely (18..40 C, -1=off)")
			}
		}
		if err := ctx.Err(); err != nil {
			return ProbeResult{Log: joinLines(logb)}, err
		}
		oscOK = tryIntsSIID(ctx, api, did, 2, 7, []int{0, 45, 90, 180}, []int{30, 360}, sleep, note)
		if err := ctx.Err(); err != nil {
			return ProbeResult{Log: joinLines(logb)}, err
		}
		if _, has := orig6[15]; has {
			cmOK = len(tryIntsSIID(ctx, api, did, 6, 15, []int{0, 1}, []int{2}, sleep, note)) == 2
		}
		if _, has := orig6[19]; has {
			nearOK = len(tryIntsSIID(ctx, api, did, 6, 19, []int{0, 1}, []int{2}, sleep, note)) == 2
		}
		if _, has := orig6[20]; has {
			followOK = len(tryIntsSIID(ctx, api, did, 6, 20, []int{0, 1}, []int{2}, sleep, note)) == 2
		}
		if err := ctx.Err(); err != nil {
			return ProbeResult{Log: joinLines(logb)}, err
		}
	} else {
		volOK = tryIntsSIID(ctx, api, did, 2, 5, []int{0, 80, 90, 100}, []int{-1, 101}, sleep, note)
		if err := ctx.Err(); err != nil {
			return ProbeResult{Log: joinLines(logb)}, err
		}
		lightOK = tryIntsSIID(ctx, api, did, 2, 6, []int{-1, 0, 1, 2}, []int{3}, sleep, note)
		if err := ctx.Err(); err != nil {
			return ProbeResult{Log: joinLines(logb)}, err
		}
		if _, isBool := orig[7].(bool); isBool {
			toneOK = tryBools(ctx, api, did, sleep, note)
		} else {
			toneOK = tryIntsSIID(ctx, api, did, 2, 7, []int{0, 1}, []int{2}, sleep, note)
		}
		if err := ctx.Err(); err != nil {
			return ProbeResult{Log: joinLines(logb)}, err
		}
	}

	// Official onOffAirp action is Action(siid=2, aiid=1, in=[{"piid":1,"value":0|1}]).
	// Never invoke Action(2, 3), which is resetMessage and factory-resets PM20.
	powerOK := tryPowerAction(ctx, api, did, orig[1], sleep, note)

	if err := ctx.Err(); err != nil {
		restore()
		return ProbeResult{Log: joinLines(logb)}, err
	}
	if base.DID == "" {
		base.DID = did
	}
	base.VerifiedBy = "write-probe"
	base.UpdatedAt = time.Now().UTC()
	base.Controls.Mode = modeOK
	if len(fanOK) > 0 {
		base.Controls.FanMin = fanOK[0]
		base.Controls.FanMax = fanOK[len(fanOK)-1]
	}
	base.Controls.FanForcesCustom = coupled
	if auto {
		base.Controls.FanAutoModes = []int{0}
	}
	base.Controls.PowerAction = powerOK
	if isPM20 {
		base.Controls.Volume = nil
		base.Controls.Light = nil
		base.Controls.LightKind = ""
		base.Controls.Tone = nil
		base.Controls.HeaterMin = heaterMin
		base.Controls.HeaterMax = heaterMax
		base.Controls.Oscillation = oscOK
		base.Controls.ContinuousMonitoring = cmOK
		base.Controls.NearbyPowerOn = nearOK
		base.Controls.FollowMotion = followOK
	} else {
		base.Controls.Volume = volOK
		base.Controls.Light = lightOK
		base.Controls.LightKind = lightKind(lightOK)
		base.Controls.Tone = toneOK
		base.Controls.HeaterMin = 0
		base.Controls.HeaterMax = 0
		base.Controls.Oscillation = nil
		base.Controls.ContinuousMonitoring = false
		base.Controls.NearbyPowerOn = false
		base.Controls.FollowMotion = false
	}
	markRW(&base, orig, orig6)
	restore()
	return ProbeResult{Profile: base, Log: joinLines(logb)}, ctx.Err()
}

func isPM20Profile(base Profile, orig6 map[int]any) bool {
	if strings.Contains(strings.ToLower(base.Model), "u2402") || base.Controls.IsPM20() {
		return true
	}
	if len(orig6) > 0 {
		return true
	}
	for _, s := range base.Properties {
		if s.SIID == 6 && (s.PIID == 15 || s.PIID == 19 || s.PIID == 20) && s.Realized && !s.Pruned {
			return true
		}
	}
	return false
}

func pm20SIID6Refs() []PropRef {
	return []PropRef{{SIID: 6, PIID: 15}, {SIID: 6, PIID: 19}, {SIID: 6, PIID: 20}}
}

func tryPowerAction(ctx context.Context, api ProbeAPI, did string, origPower any, sleep func(time.Duration), note func(string)) bool {
	if ctx.Err() != nil {
		return false
	}
	cur, ok := asInt(origPower)
	if !ok || (cur != 1 && cur != 2) {
		note("power action skipped (unknown 2.1)")
		return false
	}
	flipIn, expectFlip := 0, 2
	restoreIn, expectRestore := 1, 1
	if cur == 2 {
		flipIn, expectFlip = 1, 1
		restoreIn, expectRestore = 0, 2
	}
	code, err := api.Action(ctx, did, 2, 1, []any{map[string]any{"piid": 1, "value": flipIn}})
	if err != nil || code != 0 {
		note(fmt.Sprintf("action 2.1 in=%d failed (code=%d err=%v)", flipIn, code, err))
		return false
	}
	sleep(time.Second)
	got, rerr := readOne(ctx, api, did, 2, 1)
	gotN, is := asInt(got)
	if rerr != nil || !is || gotN != expectFlip {
		note(fmt.Sprintf("action 2.1 in=%d readback=%v want=%d", flipIn, got, expectFlip))
		return false
	}
	code2, err2 := api.Action(ctx, did, 2, 1, []any{map[string]any{"piid": 1, "value": restoreIn}})
	if err2 != nil || code2 != 0 {
		note(fmt.Sprintf("action 2.1 restore in=%d failed (code=%d err=%v)", restoreIn, code2, err2))
		return false
	}
	sleep(time.Second)
	got2, rerr2 := readOne(ctx, api, did, 2, 1)
	got2N, is2 := asInt(got2)
	if rerr2 != nil || !is2 || got2N != expectRestore {
		note(fmt.Sprintf("action 2.1 restore readback=%v want=%d", got2, expectRestore))
		return false
	}
	note("action 2.1 power toggle verified")
	return true
}

func siid2Refs() []PropRef {
	refs := make([]PropRef, 30)
	for i := 1; i <= 30; i++ {
		refs[i-1] = PropRef{SIID: 2, PIID: i}
	}
	return refs
}

func tryIntsSIID(ctx context.Context, api ProbeAPI, did string, siid, piid int, in, out []int, sleep func(time.Duration), note func(string)) []int {
	var ok []int
	try := func(v int, expect bool) {
		if ctx.Err() != nil {
			return
		}
		if err := api.SetProperties(ctx, did, []PropWrite{{SIID: siid, PIID: piid, Value: v}}); err != nil {
			note(fmt.Sprintf("%d.%d=%d rejected", siid, piid, v))
			return
		}
		sleep(time.Second)
		got, err := readOne(ctx, api, did, siid, piid)
		if err != nil {
			note(fmt.Sprintf("%d.%d read %v", siid, piid, err))
			return
		}
		n, is := asInt(got)
		if is && n == v {
			if expect {
				ok = append(ok, v)
			}
			note(fmt.Sprintf("%d.%d=%d accepted", siid, piid, v))
			return
		}
		note(fmt.Sprintf("%d.%d=%d readback %v", siid, piid, v, got))
	}
	for _, v := range in {
		try(v, true)
	}
	for _, v := range out {
		try(v, false)
	}
	return ok
}

func tryFan(ctx context.Context, api ProbeAPI, did string, sleep func(time.Duration), note func(string)) ([]int, bool, bool) {
	if ctx.Err() != nil {
		return nil, false, false
	}
	_ = api.SetProperties(ctx, did, []PropWrite{{SIID: 2, PIID: 3, Value: 3}})
	sleep(time.Second)
	ok := tryIntsSIID(ctx, api, did, 2, 4, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, []int{0, 99}, sleep, note)
	_ = api.SetProperties(ctx, did, []PropWrite{{SIID: 2, PIID: 3, Value: 0}})
	sleep(time.Second)
	coupled := false
	auto := false
	curFan, _ := readOne(ctx, api, did, 2, 4)
	curN, _ := asInt(curFan)
	probe := 0
	for _, v := range ok {
		if v != curN {
			probe = v
			break
		}
	}
	if probe != 0 {
		if err := api.SetProperties(ctx, did, []PropWrite{{SIID: 2, PIID: 4, Value: probe}}); err == nil {
			sleep(time.Second)
			mode, _ := readOne(ctx, api, did, 2, 3)
			fan, _ := readOne(ctx, api, did, 2, 4)
			if n, is := asInt(mode); is && n == 3 {
				coupled = true
				note("fan while mode 0 forced custom")
			}
			if n, is := asInt(fan); is && n != probe {
				auto = true
				note("fan while mode 0 ignored")
			}
		}
	}
	return ok, coupled, auto
}

func tryBools(ctx context.Context, api ProbeAPI, did string, sleep func(time.Duration), note func(string)) []int {
	var ok []int
	for _, v := range []bool{false, true} {
		if ctx.Err() != nil {
			return ok
		}
		if err := api.SetProperties(ctx, did, []PropWrite{{SIID: 2, PIID: 7, Value: v}}); err != nil {
			note(fmt.Sprintf("2.7=%t rejected", v))
			continue
		}
		sleep(time.Second)
		got, err := readOne(ctx, api, did, 2, 7)
		if err != nil {
			continue
		}
		b, is := got.(bool)
		if is && b == v {
			ok = append(ok, boolInt(v))
			note(fmt.Sprintf("2.7=%t accepted", v))
		}
	}
	return ok
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func readOne(ctx context.Context, api ProbeAPI, did string, siid, piid int) (any, error) {
	rows, err := api.GetProperties(ctx, did, []PropRef{{SIID: siid, PIID: piid}})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("empty")
	}
	if rows[0].Code == -4003 {
		return nil, fmt.Errorf("code -4003")
	}
	return rows[0].Value, nil
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	default:
		return 0, false
	}
}

func lightKind(vals []int) string {
	if len(vals) == 0 {
		return ""
	}
	set := map[int]bool{}
	for _, v := range vals {
		set[v] = true
	}
	if len(vals) == 2 && (set[-1] || set[0] || set[1]) && !set[2] {
		return "onoff"
	}
	if set[0] && set[1] && set[2] {
		return "color"
	}
	return "enum"
}

func markRW(p *Profile, orig, orig6 map[int]any) {
	rw2 := map[int]bool{
		3: len(p.Controls.Mode) > 0,
		4: p.Controls.FanMax > 0,
		5: len(p.Controls.Volume) > 0,
		6: len(p.Controls.Light) > 0 || p.Controls.HeaterMax > 0,
		7: len(p.Controls.Tone) > 0 || len(p.Controls.Oscillation) > 0,
	}
	rw6 := map[int]bool{
		15: p.Controls.ContinuousMonitoring,
		19: p.Controls.NearbyPowerOn,
		20: p.Controls.FollowMotion,
	}
	found2 := map[int]bool{}
	found6 := map[int]bool{}
	for i := range p.Properties {
		switch p.Properties[i].SIID {
		case 2:
			piid := p.Properties[i].PIID
			found2[piid] = true
			if v, has := orig[piid]; has && v != nil {
				p.Properties[i].JSONType = jsonType(v)
			}
			if _, watched := rw2[piid]; watched {
				if rw2[piid] {
					p.Properties[i].Access = "rw"
				} else {
					p.Properties[i].Access = "ro"
				}
			}
		case 6:
			piid := p.Properties[i].PIID
			found6[piid] = true
			if v, has := orig6[piid]; has && v != nil {
				p.Properties[i].JSONType = jsonType(v)
			}
			if _, watched := rw6[piid]; watched {
				if rw6[piid] {
					p.Properties[i].Access = "rw"
				} else {
					p.Properties[i].Access = "ro"
				}
			}
		}
	}
	for _, piid := range []int{3, 4, 5, 6, 7} {
		if rw2[piid] && !found2[piid] {
			name, _ := specFor(2, piid)
			jt := "number"
			if v, has := orig[piid]; has && v != nil {
				jt = jsonType(v)
			}
			p.Properties = append(p.Properties, PropSpec{SIID: 2, PIID: piid, Key: name, Access: "rw", Realized: true, JSONType: jt})
		}
	}
	for _, piid := range []int{15, 19, 20} {
		if rw6[piid] && !found6[piid] {
			name, _ := specFor(6, piid)
			jt := "number"
			if v, has := orig6[piid]; has && v != nil {
				jt = jsonType(v)
			}
			p.Properties = append(p.Properties, PropSpec{SIID: 6, PIID: piid, Key: name, Access: "rw", Realized: true, JSONType: jt})
		}
	}
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}
