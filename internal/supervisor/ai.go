package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dreame-dashboard/internal/ai"
	"dreame-dashboard/internal/filex"
	"dreame-dashboard/internal/logx"
	"dreame-dashboard/internal/store"
	"dreame-dashboard/internal/web"
)

const (
	dailySummaryInterval = time.Hour
	aiChangeCooldown     = 10 * time.Minute
	aiSpikeBurstCooldown = 15 * time.Minute
	dailyReportFile      = "ai_daily.json"
	dailyCacheKey        = "daily::24h"
)

var validAIMetrics = map[string]bool{
	"pm25": true, "pm10": true, "pm1": true, "aq": true,
	"t": true, "rh": true, "hcho": true, "tvoc": true,
}

var watchedMetrics = []string{"pm25", "pm10", "pm1", "aq", "t", "rh", "hcho", "tvoc"}

// metricSnap records canonical metric values (°C, mg/m³ for HCHO) at the time a daily summary was generated.
type metricSnap struct {
	Power  int                `json:"power,omitempty"`
	Values map[string]float64 `json:"values,omitempty"`
}

type persistedDaily struct {
	Report       ai.Report  `json:"report"`
	Baseline     metricSnap `json:"baseline"`
	ChangeStreak int        `json:"change_streak,omitempty"`
}

func changeCooldownFor(streak int) time.Duration {
	if streak >= 2 {
		return aiSpikeBurstCooldown
	}
	return aiChangeCooldown
}

// SaveOpenAI saves or removes the encrypted OpenAI API key and model choice.
func (a *App) SaveOpenAI(_ context.Context, form web.OpenAIForm) error {
	if a.AIKeys == nil {
		return errors.New("AI key store unavailable")
	}
	if form.Delete {
		if err := a.AIKeys.Delete(); err != nil {
			a.setFlash(err.Error(), false)
			return err
		}
		a.clearAICache(true)
		a.publishAI("daily")
		_ = a.Store.AppendEvent(store.EventRecord{Kind: "ai", Target: "openai", Detail: "api key removed"})
		a.setFlash("OpenAI API key removed.", true)
		logx.Info("ai", "openai key removed")
		return nil
	}
	prev := a.AIKeys.Status()
	if err := a.AIKeys.Save(form.APIKey, form.Model); err != nil {
		a.setFlash(err.Error(), false)
		return err
	}
	next := a.AIKeys.Status()
	if prev.Model != next.Model || !prev.Configured {
		a.clearAICache(prev.Model != next.Model)
	}
	a.wakeAI()
	detail := "model=" + next.Model
	if strings.TrimSpace(form.APIKey) != "" {
		detail = "key saved (" + next.Hint + ") " + detail
	}
	_ = a.Store.AppendEvent(store.EventRecord{Kind: "ai", Target: "openai", Detail: detail})
	a.setFlash("OpenAI settings saved.", true)
	logx.Info("ai", "openai settings saved", "model", next.Model, "source", next.Source)
	return nil
}

// CachedReport returns the last generated report for scope/metric/span if present.
func (a *App) CachedReport(scope, metric, span string) (ai.Report, bool) {
	scope = allow(scope, "general", "daily", "general", "metric")
	span = allow(span, "6h", "6h", "24h", "7d", "30d")
	switch scope {
	case "daily":
		metric = ""
		span = "24h"
	case "general":
		metric = ""
	}
	key := scope + ":" + metric + ":" + span
	a.aiMu.RLock()
	defer a.aiMu.RUnlock()
	rep, ok := a.aiCache[key]
	return rep, ok
}

// Analyze generates (or returns cached) ChatGPT analysis for one metric, general, or daily hourly summary.
func (a *App) Analyze(ctx context.Context, req web.AIRequest) (ai.Report, error) {
	scope := allow(req.Scope, "", "daily", "general", "metric")
	if scope == "" {
		return ai.Report{}, &StatusError{Code: 400, Msg: "invalid scope"}
	}
	span := allow(req.Span, "6h", "6h", "24h", "7d", "30d")
	metric := strings.TrimSpace(req.Metric)
	switch scope {
	case "metric":
		if !validAIMetrics[metric] {
			return ai.Report{}, &StatusError{Code: 400, Msg: "unknown metric"}
		}
	case "daily":
		metric = ""
		span = "24h"
	default:
		metric = ""
	}

	cacheKey := scope + ":" + metric + ":" + span
	if !req.Force {
		if rep, ok := a.CachedReport(scope, metric, span); ok {
			return rep, nil
		}
	}
	if a.AIKeys == nil || a.AI == nil {
		return ai.Report{}, &StatusError{Code: 503, Msg: "AI analyzer unavailable"}
	}
	apiKey, model, err := a.AIKeys.Resolve()
	if err != nil {
		if errors.Is(err, ai.ErrNotConfigured) {
			return ai.Report{}, &StatusError{Code: 412, Msg: "Configure an OpenAI API key in Device settings first."}
		}
		return ai.Report{}, &StatusError{Code: 500, Msg: err.Error()}
	}

	a.aiCallMu.Lock()
	defer a.aiCallMu.Unlock()
	if !req.Force {
		if rep, ok := a.CachedReport(scope, metric, span); ok {
			return rep, nil
		}
	}

	var input ai.AnalyzeInput
	var snap metricSnap
	var trigger string
	if scope == "daily" {
		a.setDailyBusy(true)
		defer a.setDailyBusy(false)
		trigger = a.dailyTrigger()
		snap = a.captureMetricSnap()
		input = a.buildDailyAnalyzeInput()
		if trigger != "" {
			input.Trigger = "live change: " + trigger
		} else {
			input.Trigger = "hourly"
		}
	} else {
		input = a.buildAnalyzeInput(scope, metric, span)
	}
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	rep, err := a.AI.Analyze(callCtx, apiKey, model, input)
	if err != nil {
		if scope == "daily" {
			a.setDailyError(err.Error())
			a.publishAI("daily")
		}
		logx.Warn("ai", "openai analysis failed", "scope", scope, "metric", metric, "err", err.Error())
		return ai.Report{}, &StatusError{Code: 502, Msg: err.Error()}
	}

	streak := 0
	a.aiMu.Lock()
	if a.aiCache == nil {
		a.aiCache = map[string]ai.Report{}
	}
	a.aiCache[cacheKey] = rep
	if scope == "daily" {
		a.aiDailyErr = ""
		a.aiBaseline = snap
		a.aiTrigger = ""
		if trigger != "" {
			a.aiChangeStreak++
		} else {
			a.aiChangeStreak = 0
		}
		streak = a.aiChangeStreak
	}
	a.aiMu.Unlock()

	if scope == "daily" {
		a.saveDailyReport(rep, snap, streak)
		a.publishAI("daily")
		a.wakeAI()
	}

	target := scope
	if metric != "" {
		target = metric
	}
	detail := span + " " + rep.Model + " (" + rep.Status + ")"
	if scope == "daily" && trigger != "" {
		detail += " trigger=" + trigger
	}
	_ = a.Store.AppendEvent(store.EventRecord{Kind: "ai", Target: target, Detail: detail})
	logx.Info("ai", "openai analysis complete", "scope", scope, "target", target, "span", span, "model", rep.Model, "trigger", trigger)
	return rep, nil
}

// NoteReading inspects a freshly polled/pushed reading and schedules an immediate
// daily summary refresh if any metric crossed a threshold band or shifted materially.
func (a *App) NoteReading(r store.Reading) {
	if a.AIKeys == nil || !a.AIKeys.Status().Configured {
		return
	}
	cur := metricSnapFromReading(r)
	if len(cur.Values) == 0 && cur.Power == 0 {
		return
	}
	wake := false
	a.aiMu.Lock()
	if len(a.aiBaseline.Values) == 0 && a.aiBaseline.Power == 0 {
		a.aiBaseline = cur
		a.aiMu.Unlock()
		return
	}
	changed, reason := detectMetricChanges(a.aiBaseline, cur)
	if len(changed) == 0 {
		a.aiTrigger = ""
		a.aiMu.Unlock()
		return
	}
	for _, id := range changed {
		for k := range a.aiCache {
			if strings.HasPrefix(k, "metric:"+id+":") {
				delete(a.aiCache, k)
			}
		}
	}
	prevReason := a.aiTrigger
	a.aiTrigger = reason
	wake = prevReason == ""
	a.aiMu.Unlock()
	if wake {
		logx.Info("ai", "metric change queued summary refresh", "reason", reason)
		a.wakeAI()
	}
}

// aiDailyLoop refreshes the daily summary hourly and immediately on meaningful metric changes.
func (a *App) aiDailyLoop(ctx context.Context) {
	backoff := time.Duration(0)
	for ctx.Err() == nil {
		if a.Phase() != "ready" {
			a.waitAI(ctx, 2*time.Second)
			continue
		}
		if a.AIKeys == nil || !a.AIKeys.Status().Configured {
			backoff = 0
			a.waitAI(ctx, time.Minute)
			continue
		}
		curModel := a.AIKeys.Status().Model
		if rep, ok := a.CachedReport("daily", "", "24h"); ok {
			if genAt, err := time.Parse(time.RFC3339, rep.GeneratedAt); err == nil && rep.Version >= ai.DailyPromptVersion && modelMatches(rep.Model, curModel) {
				age := time.Since(genAt)
				trigger := a.dailyTrigger()
				if trigger != "" {
					// Re-verify against current live props so reverted blips cancel cleanly.
					cur := a.captureMetricSnap()
					a.aiMu.Lock()
					changed, reason := detectMetricChanges(a.aiBaseline, cur)
					if len(changed) == 0 {
						a.aiTrigger = ""
						trigger = ""
					} else {
						a.aiTrigger = reason
						trigger = reason
					}
					a.aiMu.Unlock()
				}
				if trigger != "" {
					cooldown := changeCooldownFor(a.dailyChangeStreak())
					if age < cooldown {
						backoff = 0
						a.waitAI(ctx, cooldown-age)
						continue
					}
				} else if age < dailySummaryInterval {
					backoff = 0
					a.waitAI(ctx, dailySummaryInterval-age)
					continue
				}
			}
		}
		in := a.buildDailyAnalyzeInput()
		if !hasDailyTelemetry(in) {
			a.waitAI(ctx, 15*time.Second)
			continue
		}
		_, err := a.Analyze(ctx, web.AIRequest{Scope: "daily", Span: "24h", Force: true})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if backoff < 5*time.Minute {
				backoff = 5 * time.Minute
			} else if backoff < 15*time.Minute {
				backoff *= 2
				if backoff > 15*time.Minute {
					backoff = 15 * time.Minute
				}
			}
			a.waitAI(ctx, backoff)
			continue
		}
		backoff = 0
		a.waitAI(ctx, dailySummaryInterval)
	}
}

func (a *App) dailyTrigger() string {
	a.aiMu.RLock()
	defer a.aiMu.RUnlock()
	return a.aiTrigger
}

func (a *App) dailyChangeStreak() int {
	a.aiMu.RLock()
	defer a.aiMu.RUnlock()
	return a.aiChangeStreak
}

func (a *App) dailyStatus(now time.Time) (rep ai.Report, ok bool, errText string, busy bool, nextIn string) {
	a.aiMu.RLock()
	rep, ok = a.aiCache[dailyCacheKey]
	errText = a.aiDailyErr
	busy = a.aiDailyBusy
	a.aiMu.RUnlock()
	if ok {
		if genAt, err := time.Parse(time.RFC3339, rep.GeneratedAt); err == nil {
			rem := genAt.Add(dailySummaryInterval).Sub(now)
			if rem > time.Minute {
				nextIn = "in " + humanDuration(rem)
			} else {
				nextIn = "soon"
			}
		}
	}
	return rep, ok, errText, busy, nextIn
}

func (a *App) dailyReportPath() string {
	if a.Cfg.DataDir == "" {
		return ""
	}
	return filepath.Join(a.Cfg.DataDir, dailyReportFile)
}

func (a *App) loadDailyReport() {
	path := a.dailyReportPath()
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var env persistedDaily
	var rep ai.Report
	var base metricSnap
	var streak int
	if json.Unmarshal(b, &env) == nil && env.Report.GeneratedAt != "" {
		rep = env.Report
		base = env.Baseline
		streak = env.ChangeStreak
	} else {
		if json.Unmarshal(b, &rep) != nil || rep.GeneratedAt == "" {
			return
		}
	}
	if rep.Scope == "" {
		rep.Scope = "daily"
	}
	if rep.Span == "" {
		rep.Span = "24h"
	}
	a.aiMu.Lock()
	if a.aiCache == nil {
		a.aiCache = map[string]ai.Report{}
	}
	a.aiCache[dailyCacheKey] = rep
	if len(base.Values) > 0 || base.Power != 0 {
		a.aiBaseline = base
	}
	a.aiChangeStreak = streak
	a.aiMu.Unlock()
}

func (a *App) saveDailyReport(rep ai.Report, base metricSnap, streak int) {
	path := a.dailyReportPath()
	if path == "" {
		return
	}
	b, err := json.MarshalIndent(persistedDaily{Report: rep, Baseline: base, ChangeStreak: streak}, "", "  ")
	if err != nil {
		return
	}
	if err := filex.WriteAtomic(path, append(b, '\n')); err != nil {
		logx.Warn("ai", "daily report save failed", "err", err.Error())
	}
}

func (a *App) clearAICache(removeDailyFile bool) {
	a.aiMu.Lock()
	a.aiCache = map[string]ai.Report{}
	a.aiBaseline = metricSnap{}
	a.aiTrigger = ""
	a.aiChangeStreak = 0
	a.aiDailyErr = ""
	a.aiMu.Unlock()
	if removeDailyFile {
		if p := a.dailyReportPath(); p != "" {
			_ = os.Remove(p)
		}
	}
}

func (a *App) setDailyBusy(busy bool) {
	a.aiMu.Lock()
	a.aiDailyBusy = busy
	a.aiMu.Unlock()
}

func (a *App) setDailyError(msg string) {
	a.aiMu.Lock()
	a.aiDailyErr = msg
	a.aiMu.Unlock()
}

func (a *App) publishAI(scope string) {
	if a.Hub != nil {
		a.Hub.Publish(Event{Name: "ai", Data: `{"scope":"` + scope + `"}`})
	}
}

func (a *App) wakeAI() {
	if a.aiWake == nil {
		return
	}
	select {
	case a.aiWake <- struct{}{}:
	default:
	}
}

func (a *App) waitAI(ctx context.Context, d time.Duration) {
	if d < 0 {
		d = 0
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	case <-a.aiWake:
	}
}

func modelMatches(got, want string) bool {
	got = strings.TrimSpace(got)
	want = strings.TrimSpace(want)
	if got == "" || want == "" {
		return true
	}
	return got == want || strings.HasPrefix(got, want+"-20")
}

func hasDailyTelemetry(in ai.AnalyzeInput) bool {
	for _, m := range in.Metrics {
		if m.Current != "" && m.Current != "—" {
			return true
		}
		if m.Count > 0 {
			return true
		}
	}
	return false
}

func (a *App) captureMetricSnap() metricSnap {
	a.Store.Lock()
	props := map[string]store.PropSnap{}
	for k, v := range a.Store.State().Props {
		props[k] = v
	}
	a.Store.Unlock()

	snap := metricSnap{Values: map[string]float64{}}
	if propHas(props, "2.1") {
		snap.Power = propInt(props, "2.1")
	}
	standbyOff := (snap.Power == 2 && (!propHas(props, "3.3") || propF(props, "3.3") == 0) && (!propHas(props, "3.2") || propF(props, "3.2") == 0)) ||
		(propHas(props, "3.3") && propHas(props, "3.2") && propF(props, "3.3") == 0 && propF(props, "3.2") == 0)

	add := func(id, key string, scale float64, offInStandby bool) {
		if !propHas(props, key) || (standbyOff && offInStandby) {
			return
		}
		snap.Values[id] = propF(props, key) * scale
	}
	add("pm25", "3.5", 1, true)
	add("pm10", "3.6", 1, true)
	add("pm1", "3.10", 1, true)
	add("aq", "3.12", 1, false)
	add("t", "3.3", 1, true)
	add("rh", "3.2", 1, true)
	add("hcho", "3.7", 0.001, true)
	add("tvoc", "3.8", 1, false)
	return snap
}

func metricSnapFromReading(r store.Reading) metricSnap {
	snap := metricSnap{Values: map[string]float64{}}
	if r.Pwr != nil {
		snap.Power = *r.Pwr
	}
	standbyOff := (snap.Power == 2 && (r.T == nil || *r.T == 0) && (r.RH == nil || *r.RH == 0)) ||
		(r.T != nil && r.RH != nil && *r.T == 0 && *r.RH == 0)
	add := func(id string, p *float64, scale float64, offInStandby bool) {
		if p == nil || (standbyOff && offInStandby) {
			return
		}
		snap.Values[id] = *p * scale
	}
	add("pm25", r.PM25, 1, true)
	add("pm10", r.PM10, 1, true)
	add("pm1", r.PM1, 1, true)
	add("aq", r.AQ, 1, false)
	add("t", r.T, 1, true)
	add("rh", r.RH, 1, true)
	add("hcho", r.HCHO, 0.001, true)
	add("tvoc", r.TVOC, 1, false)
	return snap
}

func detectMetricChanges(base, cur metricSnap) ([]string, string) {
	if len(base.Values) == 0 && base.Power == 0 {
		return nil, ""
	}
	var changed []string
	var parts []string
	if base.Power != 0 && cur.Power != 0 && base.Power != cur.Power {
		changed = append(changed, "power")
		parts = append(parts, fmt.Sprintf("power %d->%d", base.Power, cur.Power))
	}
	for _, id := range watchedMetrics {
		bv, okB := base.Values[id]
		cv, okC := cur.Values[id]
		if !okB || !okC {
			continue
		}
		bands := web.BandsFor(id, "°C")
		zoneChanged := bands != nil && web.ZoneOf(bv, bands) != web.ZoneOf(cv, bands) &&
			math.Abs(cv-bv) >= zoneChangeMinDelta(id)
		if zoneChanged || metricDeltaExceeded(id, bv, cv) {
			changed = append(changed, id)
			parts = append(parts, fmt.Sprintf("%s %s->%s", id, fmtNum(bv), fmtNum(cv)))
		}
	}
	return changed, strings.Join(parts, ", ")
}

func zoneChangeMinDelta(id string) float64 {
	switch id {
	case "pm25", "pm1":
		return 2
	case "pm10":
		return 3
	case "aq":
		return 3
	case "t":
		return 0.5
	case "rh":
		return 2
	case "hcho":
		return 0.005
	case "tvoc":
		return 15
	default:
		return 0
	}
}

func metricDeltaExceeded(id string, base, cur float64) bool {
	diff := math.Abs(cur - base)
	switch id {
	case "pm25", "pm1":
		return diff >= math.Max(5, 0.35*math.Abs(base))
	case "pm10":
		return diff >= math.Max(10, 0.35*math.Abs(base))
	case "aq":
		return diff >= 12
	case "t":
		return diff >= 1.5
	case "rh":
		return diff >= 6
	case "hcho":
		return diff >= math.Max(0.015, 0.30*math.Abs(base))
	case "tvoc":
		return diff >= math.Max(45, 0.30*math.Abs(base))
	default:
		return false
	}
}

func (a *App) buildDailyAnalyzeInput() ai.AnalyzeInput {
	return a.buildDailyAnalyzeInputAt(time.Now())
}

func (a *App) buildDailyAnalyzeInputAt(now time.Time) ai.AnalyzeInput {
	loc := a.Cfg.Location
	if loc == nil {
		loc = time.UTC
	}
	localNow := now.In(loc)
	startOfDay := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	from := startOfDay
	if now.Sub(startOfDay) < time.Hour {
		from = now.Add(-2 * time.Hour)
	}
	tempUnit, _ := a.tempMeta()
	hourly := a.Store.HourlySeries(from, now, []string{"pm25", "pm10", "pm1", "aq", "t", "rh", "hcho", "tvoc"})

	a.Store.Lock()
	st := *a.Store.State()
	props := map[string]store.PropSnap{}
	for k, v := range a.Store.State().Props {
		props[k] = v
	}
	a.Store.Unlock()

	powerState, mode, fan, hepa := deviceContext(props)
	standbyOff := propHas(props, "2.1") && propInt(props, "2.1") == 2 &&
		(!propHas(props, "3.3") || propF(props, "3.3") == 0) &&
		(!propHas(props, "3.2") || propF(props, "3.2") == 0)

	tu := "°C"
	if tempUnit == "F" {
		tu = "°F"
	}
	type spec struct {
		id, label, propKey, unit string
		scale                    float64
		temp                     bool
		offInStandby             bool
	}
	specs := []spec{
		{"pm25", "PM2.5", "3.5", "µg/m³", 1, false, true},
		{"pm10", "PM10", "3.6", "µg/m³", 1, false, true},
		{"pm1", "PM1", "3.10", "µg/m³", 1, false, true},
		{"aq", "Air quality", "3.12", "%", 1, false, false},
		{"t", "Temperature", "3.3", tu, 1, true, true},
		{"rh", "Humidity", "3.2", "%", 1, false, true},
		{"hcho", "HCHO", "3.7", "mg/m³", 0.001, false, true},
		{"tvoc", "TVOC", "3.8", "µg/m³", 1, false, false},
	}

	todayLocal := now.In(loc).Format("2006-01-02")
	metrics := make([]ai.MetricInput, 0, len(specs))
	for _, sp := range specs {
		u := ""
		if sp.unit != "" {
			u = " " + sp.unit
		}
		current := "—"
		if standbyOff && sp.offInStandby {
			current = "paused in standby"
		} else if propHas(props, sp.propKey) {
			val := propF(props, sp.propKey) * sp.scale
			if sp.temp && tempUnit == "F" {
				val = val*9/5 + 32
			}
			current = fmtNum(val) + u
		}
		pts := samples(hourly[sp.id], sp.temp && tempUnit == "F")
		if sp.scale != 1 {
			pts = scaleSamples(pts, sp.scale)
		}
		mi := ai.MetricInput{
			ID:      sp.id,
			Label:   sp.label,
			Unit:    sp.unit,
			Current: current,
			Min:     "—",
			Avg:     "—",
			Peak:    "—",
			Count:   len(pts),
		}
		if len(pts) > 0 {
			minV, maxV, sum := pts[0].V, pts[0].V, 0.0
			for _, pt := range pts {
				sum += pt.V
				lo, hi := pt.V, pt.V
				if pt.Range {
					lo, hi = pt.Min, pt.Max
				}
				if lo < minV {
					minV = lo
				}
				if hi > maxV {
					maxV = hi
				}
				tsLocal := pt.TS.In(loc)
				when := tsLocal.Format("15:04")
				if tsLocal.Format("2006-01-02") != todayLocal {
					when = tsLocal.Format("Jan 2 15:04")
				}
				p := ai.Point{When: when, V: round1(pt.V)}
				if pt.Range {
					p.Min = round1(pt.Min)
					p.Max = round1(pt.Max)
				}
				mi.Points = append(mi.Points, p)
			}
			if mi.Current == "—" {
				mi.Current = fmtNum(pts[len(pts)-1].V) + u
			}
			mi.Min = fmtNum(minV) + u
			mi.Avg = fmtNum(sum/float64(len(pts))) + u
			mi.Peak = fmtNum(maxV) + u
		}
		metrics = append(metrics, mi)
	}

	return ai.AnalyzeInput{
		Scope:       "daily",
		Span:        "24h",
		Granularity: "1h",
		CurrentTime: now.In(loc).Format("2006-01-02 15:04 MST"),
		DeviceName:  nameOr(st.Device.Name),
		PowerState:  powerState,
		Mode:        mode,
		Fan:         fan,
		HEPA:        hepa,
		TempUnit:    tempUnit,
		Metrics:     metrics,
		GeneratedAt: now,
	}
}

func deviceContext(props map[string]store.PropSnap) (powerState, mode, fan, hepa string) {
	powerState = "Unknown"
	if propHas(props, "2.1") {
		if propInt(props, "2.1") == 2 {
			powerState = "Standby"
		} else {
			powerState = "Working"
		}
	}
	mode = "Unknown"
	if propHas(props, "2.3") {
		mode = modeLabel(propInt(props, "2.3"))
	}
	fan = "Unknown"
	if propHas(props, "2.4") {
		fan = strconv.Itoa(propInt(props, "2.4"))
	}
	hepa = "Unknown"
	if propHas(props, "4.1") {
		hepa = fmt.Sprintf("%.0f%%", propF(props, "4.1"))
	}
	return powerState, mode, fan, hepa
}

func (a *App) buildAnalyzeInput(scope, metric, span string) ai.AnalyzeInput {
	now := time.Now()
	loc := a.Cfg.Location
	if loc == nil {
		loc = time.UTC
	}
	tempUnit, _ := a.tempMeta()
	charts := a.allCharts(span, tempUnit)

	a.Store.Lock()
	st := *a.Store.State()
	props := map[string]store.PropSnap{}
	for k, v := range a.Store.State().Props {
		props[k] = v
	}
	a.Store.Unlock()

	powerState, mode, fan, hepa := deviceContext(props)

	maxPts := 12
	if scope == "metric" {
		maxPts = 48
	}
	var metrics []ai.MetricInput
	for _, ch := range charts {
		if scope == "metric" && ch.ID != metric {
			continue
		}
		if len(ch.Series) == 0 {
			continue
		}
		ser := ch.Series[0]
		mi := summarizeForAI(ch.ID, ch.Title, ser, span, loc, maxPts)
		metrics = append(metrics, mi)
	}
	return ai.AnalyzeInput{
		Scope:       scope,
		Metric:      metric,
		Span:        span,
		CurrentTime: now.In(loc).Format("2006-01-02 15:04 MST"),
		DeviceName:  nameOr(st.Device.Name),
		PowerState:  powerState,
		Mode:        mode,
		Fan:         fan,
		HEPA:        hepa,
		TempUnit:    tempUnit,
		Metrics:     metrics,
		GeneratedAt: now,
	}
}

func summarizeForAI(id, label string, ser web.Series, span string, loc *time.Location, maxPts int) ai.MetricInput {
	mi := ai.MetricInput{
		ID:      id,
		Label:   label,
		Unit:    ser.Unit,
		Current: "—",
		Min:     "—",
		Avg:     "—",
		Peak:    "—",
		Count:   len(ser.Points),
	}
	if len(ser.Points) == 0 {
		return mi
	}
	minV, maxV, sum := ser.Points[0].V, ser.Points[0].V, 0.0
	for _, pt := range ser.Points {
		sum += pt.V
		lo, hi := pt.V, pt.V
		if pt.Range {
			lo, hi = pt.Min, pt.Max
		}
		if lo < minV {
			minV = lo
		}
		if hi > maxV {
			maxV = hi
		}
	}
	u := ""
	if ser.Unit != "" {
		u = " " + ser.Unit
	}
	last := ser.Points[len(ser.Points)-1]
	mi.Current = fmtNum(last.V) + u
	mi.Min = fmtNum(minV) + u
	mi.Avg = fmtNum(sum/float64(len(ser.Points))) + u
	mi.Peak = fmtNum(maxV) + u

	layout := "15:04"
	if span == "7d" || span == "30d" {
		layout = "Jan 2 15:04"
	}
	pts := ser.Points
	step := 1
	if len(pts) > maxPts && maxPts > 1 {
		step = (len(pts) + maxPts - 1) / maxPts
	}
	for i := 0; i < len(pts); i += step {
		pt := pts[i]
		p := ai.Point{When: pt.TS.In(loc).Format(layout), V: round1(pt.V)}
		if pt.Range {
			p.Min = round1(pt.Min)
			p.Max = round1(pt.Max)
		}
		mi.Points = append(mi.Points, p)
	}
	if len(pts) > 1 && (len(pts)-1)%step != 0 {
		pt := pts[len(pts)-1]
		p := ai.Point{When: pt.TS.In(loc).Format(layout), V: round1(pt.V)}
		if pt.Range {
			p.Min = round1(pt.Min)
			p.Max = round1(pt.Max)
		}
		mi.Points = append(mi.Points, p)
	}
	return mi
}

func modeLabel(v int) string {
	switch v {
	case 0:
		return "AI"
	case 1:
		return "Strong"
	case 2:
		return "Sleep"
	case 3:
		return "Custom"
	case 4:
		return "Pet"
	case 5:
		return "Comfort"
	default:
		return strconv.Itoa(v)
	}
}

func fmtNum(v float64) string {
	av := v
	if av < 0 {
		av = -av
	}
	if av < 0.0005 {
		return "0"
	}
	if av < 1 {
		s := strconv.FormatFloat(v, 'f', 3, 64)
		return strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	r := round1(v)
	if r == float64(int64(r)) {
		return strconv.FormatInt(int64(r), 10)
	}
	return strconv.FormatFloat(r, 'f', 1, 64)
}

func round1(v float64) float64 {
	av := v
	if av < 0 {
		av = -av
	}
	if av > 0 && av < 1 {
		if v >= 0 {
			return float64(int64(v*1000+0.5)) / 1000
		}
		return float64(int64(v*1000-0.5)) / 1000
	}
	if v >= 0 {
		return float64(int64(v*10+0.5)) / 10
	}
	return float64(int64(v*10-0.5)) / 10
}
