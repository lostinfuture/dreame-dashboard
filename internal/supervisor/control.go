package supervisor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dreame-dashboard/internal/dreame"
	"dreame-dashboard/internal/logx"
	"dreame-dashboard/internal/store"
)

// StatusError is an HTTP-shaped failure from the control worker.
type StatusError struct {
	Code int
	Msg  string
}

func (e *StatusError) Error() string {
	if e == nil {
		return ""
	}
	return e.Msg
}

// StatusCode is the HTTP status for this failure.
func (e *StatusError) StatusCode() int {
	if e == nil || e.Code == 0 {
		return 500
	}
	return e.Code
}

// Request is one device write.
type Request struct {
	Action   string
	SIID     int
	PIID     int
	Value    any
	Expected any
}

// Worker serializes device writes.
type Worker struct {
	Cloud      Cloud
	Store      *store.Store
	Profile    *dreame.Profile
	ProfMu     *sync.RWMutex
	Hub        *Hub
	Interval   time.Duration
	intervalNS atomic.Int64
	ReadOnly   func() bool
	Now        func() time.Time
	Sleep      func(time.Duration)
	last       time.Time
}

func (w *Worker) setInterval(d time.Duration) { w.intervalNS.Store(int64(d)) }

func (w *Worker) interval() time.Duration {
	if n := w.intervalNS.Load(); n > 0 {
		return time.Duration(n)
	}
	return w.Interval
}

func (w *Worker) profile() dreame.Profile {
	if w.ProfMu != nil {
		w.ProfMu.RLock()
		defer w.ProfMu.RUnlock()
	}
	return copyProfile(w.Profile)
}

func (w *Worker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Worker) sleep(d time.Duration) {
	if w.Sleep != nil {
		w.Sleep(d)
		return
	}
	time.Sleep(d)
}

// Do runs one command or returns 403 or 409.
func (w *Worker) Do(ctx context.Context, req Request) error {
	if w.ReadOnly != nil && w.ReadOnly() {
		return &StatusError{Code: 403, Msg: "read-only"}
	}
	prof := w.profile()
	if !prof.WriteVerified() {
		return &StatusError{Code: 403, Msg: "run dmd probe-writes to enable controls"}
	}
	w.Store.Lock()
	st := w.Store.State()
	offline := st.Status == "offline"
	powerSnap, hasPower := st.Props["2.1"]
	w.Store.Unlock()
	if offline {
		return &StatusError{Code: 403, Msg: "device offline"}
	}
	if err := allowWrite(prof, req); err != nil {
		return err
	}
	if requiresWorking(prof, req) && hasPower && sameValue(powerSnap.Value, 2) {
		return &StatusError{Code: 409, Msg: standbyGuardMsg(prof, req)}
	}
	if !w.last.IsZero() {
		wait := time.Second - w.now().Sub(w.last)
		if wait > 0 {
			w.sleep(wait)
		}
	}
	w.last = w.now()
	start := w.now()
	var err error
	if req.Action == "toggle_power" {
		err = w.toggle(ctx, prof, req)
	} else {
		err = w.set(ctx, prof, req)
	}
	ms := w.now().Sub(start).Milliseconds()
	code := 0
	result := "OK (0)"
	if err != nil {
		result = err.Error()
		var se *StatusError
		if errors.As(err, &se) {
			code = se.Code
		} else if c, ok := dreame.CodeOf(err); ok {
			code = c
		} else {
			code = -1
		}
	}
	target := targetName(prof, req)
	_ = w.Store.AppendCommand(store.CommandRecord{
		TS: w.now().Unix(), Target: target, Detail: detail(prof, req), Result: result, Code: code, MS: ms,
	})
	if err == nil || code == 409 {
		w.Store.Lock()
		_ = w.Store.SaveState()
		w.Store.Unlock()
		if w.Hub != nil {
			w.Hub.Publish(Event{Name: "state", Data: `{"ok":true}`})
		}
	}
	if err == nil {
		logx.Info("control", "control ok", "target", target, "ms", fmt.Sprint(ms))
	}
	return err
}

func requiresWorking(prof dreame.Profile, req Request) bool {
	if req.Action != "set" {
		return false
	}
	if req.SIID == 2 && (req.PIID == 3 || req.PIID == 4) {
		return true
	}
	if prof.Controls.IsPM20() {
		if req.SIID == 2 && (req.PIID == 6 || req.PIID == 7) {
			return true
		}
		if req.SIID == 6 && req.PIID == 20 {
			return true
		}
	}
	return false
}

func standbyGuardMsg(prof dreame.Profile, req Request) string {
	if req.SIID == 2 && (req.PIID == 3 || req.PIID == 4) {
		return "Purifier is in standby. Turn it on before changing mode or fan speed."
	}
	name, _ := dreameKey(prof.Controls, req.SIID, req.PIID)
	return fmt.Sprintf("Purifier is in standby. Turn it on before changing %s.", strings.ToLower(name))
}

func allowWrite(prof dreame.Profile, req Request) error {
	switch req.Action {
	case "toggle_power":
		if !prof.Controls.PowerAction {
			return &StatusError{Code: 400, Msg: "power action is not verified"}
		}
		return nil
	case "set":
		spec, ok := propSpec(prof, req.SIID, req.PIID)
		if !ok || spec.Access != "rw" || spec.Pruned {
			return &StatusError{Code: 400, Msg: "property is not writable"}
		}
		if !valueAllowed(prof.Controls, req) {
			return &StatusError{Code: 400, Msg: "value is outside the verified range"}
		}
		return nil
	default:
		return &StatusError{Code: 400, Msg: "unknown action"}
	}
}

func propSpec(prof dreame.Profile, siid, piid int) (dreame.PropSpec, bool) {
	for _, spec := range prof.Properties {
		if spec.SIID == siid && spec.PIID == piid {
			return spec, true
		}
	}
	return dreame.PropSpec{}, false
}

func valueAllowed(c dreame.Controls, req Request) bool {
	n, ok := asInt(req.Value)
	if !ok {
		return false
	}
	switch fmt.Sprintf("%d.%d", req.SIID, req.PIID) {
	case "2.3":
		return containsInt(c.Mode, n)
	case "2.4":
		return c.FanMax >= c.FanMin && n >= c.FanMin && n <= c.FanMax && c.FanMax > 0
	case "2.5":
		return !c.IsPM20() && containsInt(c.Volume, n)
	case "2.6":
		if c.IsPM20() {
			return c.HeaterMax > 0 && (n == -1 || (n >= c.HeaterMin && n <= c.HeaterMax))
		}
		return containsInt(c.Light, n)
	case "2.7":
		if c.IsPM20() {
			return containsInt(c.Oscillation, n)
		}
		return containsInt(c.Tone, n)
	case "6.15":
		return c.ContinuousMonitoring && (n == 0 || n == 1)
	case "6.19":
		return c.NearbyPowerOn && (n == 0 || n == 1)
	case "6.20":
		return c.FollowMotion && (n == 0 || n == 1)
	default:
		return false
	}
}

func containsInt(vals []int, n int) bool {
	for _, v := range vals {
		if v == n {
			return true
		}
	}
	return false
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	}
	return 0, false
}

func (w *Worker) toggle(ctx context.Context, prof dreame.Profile, req Request) error {
	did := prof.DID
	w.Store.Lock()
	snap := w.Store.State().Props["2.1"]
	w.Store.Unlock()
	live := snap.Value
	if w.interval() > 0 && (snap.At.IsZero() || w.now().Sub(snap.At) > 2*w.interval()) {
		rows, err := w.Cloud.GetProperties(ctx, did, []dreame.PropRef{{SIID: 2, PIID: 1}})
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			live = rows[0].Value
			w.Store.Lock()
			w.Store.State().Props["2.1"] = store.PropSnap{Value: live, At: w.now(), Code: rows[0].Code}
			w.Store.Unlock()
		}
	}
	if !sameValue(live, req.Expected) {
		logx.Info("control", "control rejected", "reason", "stale_state")
		return &StatusError{Code: 409, Msg: "Power state changed before your command ran. No toggle was sent."}
	}
	// Official onOffAirp action: Action(siid=2, aiid=1, in=[{"piid":1,"value":1|0}]).
	// value=1 switches to Working (2.1=1), value=0 switches to Standby (2.1=2).
	wantIn := 0
	expectPower := 2
	if sameValue(live, 2) {
		wantIn = 1
		expectPower = 1
	}
	code, err := w.Cloud.Action(ctx, did, 2, 1, []any{map[string]any{"piid": 1, "value": wantIn}})
	if err != nil {
		return err
	}
	if code != 0 {
		return &dreame.APIError{Op: "action", Code: code, Msg: "action rejected"}
	}
	w.sleep(time.Second)
	got, rerr := w.readBack(ctx, did, 2, 1)
	if rerr == nil && got != nil && !sameValue(got, expectPower) {
		w.sleep(time.Second)
		if got2, rerr2 := w.readBack(ctx, did, 2, 1); rerr2 == nil && got2 != nil {
			got = got2
		}
	}
	if rerr == nil && got != nil && !sameValue(got, expectPower) {
		return &StatusError{
			Code: 409,
			Msg:  fmt.Sprintf("Purifier did not change power state (still %s).", powerLabel(got)),
		}
	}
	return nil
}

func (w *Worker) set(ctx context.Context, prof dreame.Profile, req Request) error {
	did := prof.DID
	val := req.Value
	if spec, ok := propSpec(prof, req.SIID, req.PIID); ok && spec.JSONType == "bool" {
		if n, is := asInt(val); is {
			val = n != 0
		}
	}
	clearFollow := false
	if req.SIID == 2 && req.PIID == 7 && prof.Controls.IsPM20() && prof.Controls.FollowMotion {
		w.Store.Lock()
		fSnap, hasF := w.Store.State().Props["6.20"]
		w.Store.Unlock()
		if hasF && sameValue(fSnap.Value, 1) {
			clearFollow = true
			_ = w.Cloud.SetProperties(ctx, did, []dreame.PropWrite{{SIID: 6, PIID: 20, Value: 0}})
		}
	}
	if err := w.Cloud.SetProperties(ctx, did, []dreame.PropWrite{{SIID: req.SIID, PIID: req.PIID, Value: val}}); err != nil {
		return err
	}
	w.sleep(500 * time.Millisecond)
	got, rerr := w.readBack(ctx, did, req.SIID, req.PIID)
	if rerr == nil && got != nil && !sameValue(got, val) {
		w.sleep(600 * time.Millisecond)
		if got2, rerr2 := w.readBack(ctx, did, req.SIID, req.PIID); rerr2 == nil && got2 != nil {
			got = got2
		}
	}
	if req.SIID == 2 && req.PIID == 4 && prof.Controls.FanForcesCustom {
		_, _ = w.readBack(ctx, did, 2, 3)
	}
	if clearFollow {
		_, _ = w.readBack(ctx, did, 6, 20)
	}
	if req.SIID == 6 && req.PIID == 20 && prof.Controls.IsPM20() && len(prof.Controls.Oscillation) > 0 && sameValue(val, 1) {
		_, _ = w.readBack(ctx, did, 2, 7)
	}
	if rerr == nil && got != nil && !sameValue(got, val) {
		if requiresWorking(prof, req) {
			if pwr, perr := w.readBack(ctx, did, 2, 1); perr == nil && sameValue(pwr, 2) {
				return &StatusError{
					Code: 409,
					Msg:  fmt.Sprintf("Purifier is in standby — %s stayed at %s.", targetName(prof, req), formatVal(got)),
				}
			}
		}
		return &StatusError{
			Code: 409,
			Msg:  fmt.Sprintf("Device ignored %s = %s (read back %s).", targetName(prof, req), formatVal(req.Value), formatVal(got)),
		}
	}
	return nil
}

func (w *Worker) readBack(ctx context.Context, did string, siid, piid int) (any, error) {
	rows, err := w.Cloud.GetProperties(ctx, did, []dreame.PropRef{{SIID: siid, PIID: piid}})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	w.Store.Lock()
	w.Store.State().Props[fmt.Sprintf("%d.%d", siid, piid)] = store.PropSnap{Value: rows[0].Value, At: w.now(), Code: rows[0].Code}
	w.Store.Unlock()
	return rows[0].Value, nil
}

func sameValue(a, b any) bool {
	af, aok := asFloat(a)
	bf, bok := asFloat(b)
	if aok && bok {
		return af == bf
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

func formatVal(v any) string {
	switch n := v.(type) {
	case float64:
		if n == float64(int64(n)) {
			return fmt.Sprintf("%d", int64(n))
		}
		return fmt.Sprintf("%g", n)
	case float32:
		if n == float32(int64(n)) {
			return fmt.Sprintf("%d", int64(n))
		}
		return fmt.Sprintf("%g", n)
	default:
		return fmt.Sprint(v)
	}
}

func powerLabel(v any) string {
	if sameValue(v, 2) {
		return "Standby"
	}
	if sameValue(v, 1) {
		return "Working"
	}
	return formatVal(v)
}

func targetName(prof dreame.Profile, req Request) string {
	if req.Action == "toggle_power" {
		return "Power (2.1)"
	}
	name, _ := dreameKey(prof.Controls, req.SIID, req.PIID)
	return fmt.Sprintf("%s (%d.%d)", name, req.SIID, req.PIID)
}

func dreameKey(c dreame.Controls, siid, piid int) (string, bool) {
	switch fmt.Sprintf("%d.%d", siid, piid) {
	case "2.3":
		return "Mode", true
	case "2.4":
		return "Fan", true
	case "2.5":
		return "Voice", true
	case "2.6":
		if c.IsPM20() {
			return "Heater", true
		}
		return "Light", true
	case "2.7":
		if c.IsPM20() {
			return "Oscillation", true
		}
		return "Key tone", true
	case "6.15":
		return "Continuous monitoring", true
	case "6.19":
		return "Nearby power-on", true
	case "6.20":
		return "Follow motion", true
	default:
		return "Property", false
	}
}

func detail(prof dreame.Profile, req Request) string {
	if req.Action == "toggle_power" {
		if sameValue(req.Expected, 2) {
			return "Standby -> Working"
		}
		if sameValue(req.Expected, 1) {
			return "Working -> Standby"
		}
		return fmt.Sprintf("%v -> toggle", req.Expected)
	}
	if prof.Controls.IsPM20() {
		switch fmt.Sprintf("%d.%d", req.SIID, req.PIID) {
		case "2.6":
			return fmt.Sprintf("%s -> %s", heaterDetailVal(req.Expected), heaterDetailVal(req.Value))
		case "2.7":
			return fmt.Sprintf("%s -> %s", oscDetailVal(req.Expected), oscDetailVal(req.Value))
		case "6.15", "6.19", "6.20":
			return fmt.Sprintf("%s -> %s", onOffDetailVal(req.Expected), onOffDetailVal(req.Value))
		}
	}
	return fmt.Sprintf("%v -> %v", req.Expected, req.Value)
}

func heaterDetailVal(v any) string {
	n, ok := asInt(v)
	if !ok {
		return formatVal(v)
	}
	if n < 0 {
		return "Off"
	}
	return fmt.Sprintf("%d °C", n)
}

func oscDetailVal(v any) string {
	n, ok := asInt(v)
	if !ok {
		return formatVal(v)
	}
	if n == 0 {
		return "Off"
	}
	return fmt.Sprintf("%d°", n)
}

func onOffDetailVal(v any) string {
	n, ok := asInt(v)
	if !ok {
		return formatVal(v)
	}
	if n == 0 {
		return "Off"
	}
	return "On"
}
