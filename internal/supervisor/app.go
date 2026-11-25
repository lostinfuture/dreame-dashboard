package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dreame-dashboard/internal/ai"
	"dreame-dashboard/internal/config"
	"dreame-dashboard/internal/dreame"
	"dreame-dashboard/internal/store"
	"dreame-dashboard/internal/web"
)

// App is the running dashboard: poller, controls, and the HTTP backend.
type App struct {
	Cfg   config.Config
	Store *store.Store
	Auth  *dreame.Manager
	Cloud Cloud

	Poller *Poller
	Worker *Worker
	Hub    *Hub
	AIKeys *ai.KeyStore
	AI     *ai.Client

	prof           *dreame.Profile
	profMu         sync.RWMutex
	wake           chan struct{}
	aiWake         chan struct{}
	mu             sync.Mutex
	aiMu           sync.RWMutex
	aiCallMu       sync.Mutex
	aiCache        map[string]ai.Report
	aiBaseline     metricSnap
	aiTrigger      string
	aiChangeStreak int
	aiDailyErr     string
	aiDailyBusy    bool
	flash          string
	flashInfo      bool
	loginErr       string
	runCtx         context.Context
	trouble        time.Time
	lastRead       time.Time
	ctrlMu         sync.Mutex
	busy           sync.Mutex
	httpOK         atomic.Bool
	booted         atomic.Bool
	signedOut      atomic.Bool
	mqttRefresh    atomic.Bool
}

// NewApp wires a store, token manager, and cloud client.
func NewApp(cfg config.Config, st *store.Store, auth *dreame.Manager, cloud Cloud) *App {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 30 * time.Second
	}
	hub := NewHub()
	prof := &dreame.Profile{}
	if loaded, err := dreame.LoadProfile(cfg.DataDir); err == nil {
		*prof = loaded
	}
	app := &App{
		Cfg: cfg, Store: st, Auth: auth, Cloud: cloud, Hub: hub, prof: prof,
		AIKeys:  ai.NewKeyStore(cfg.DataDir, cfg.OpenAIKey, cfg.OpenAIModel),
		AI:      &ai.Client{},
		aiCache: map[string]ai.Report{},
		wake:    make(chan struct{}, 1),
		aiWake:  make(chan struct{}, 1),
	}
	app.loadDailyReport()
	app.Poller = &Poller{
		Cloud: cloud, Store: st, Profile: prof, ProfMu: &app.profMu, Hub: hub,
		Interval: cfg.PollInterval, MQTTInterval: cfg.MQTTPollInterval,
		SaveProfile: func(p *dreame.Profile) error {
			if p == nil {
				return nil
			}
			return dreame.SaveProfile(cfg.DataDir, *p)
		},
		MQTTConnected: func() bool {
			st.Lock()
			defer st.Unlock()
			return st.State().MQTT == "connected"
		},
		OnReading: app.NoteReading,
	}
	app.Worker = &Worker{
		Cloud: cloud, Store: st, Profile: prof, ProfMu: &app.profMu, Hub: hub,
		Interval: cfg.PollInterval, ReadOnly: app.readOnly,
	}
	return app
}

// Phase reports boot, auth_required, or ready.
func (a *App) Phase() string {
	if a.signedOut.Load() || (a.Auth != nil && a.Auth.State() == "auth_required") {
		return "auth_required"
	}
	if !a.booted.Load() {
		return "boot"
	}
	return "ready"
}

// Page builds the view snapshot.
func (a *App) Page(name string, q url.Values) web.Snapshot {
	if q == nil {
		q = url.Values{}
	}
	now := time.Now()
	loc := a.Cfg.Location
	if loc == nil {
		loc = time.UTC
	}
	a.Store.Lock()
	st := *a.Store.State()
	props := map[string]store.PropSnap{}
	for k, v := range a.Store.State().Props {
		props[k] = v
	}
	calls := a.Store.CallsPerMin(now)
	a.Store.Unlock()
	a.mu.Lock()
	flash := a.flash
	flashInfo := a.flashInfo
	loginErr := a.loginErr
	a.mu.Unlock()
	if loginErr == "" && a.Auth != nil {
		loginErr = a.Auth.LastError()
	}
	metric := allow(q.Get("metric"), "pm25", "pm25", "pm10", "pm1", "aq", "t", "rh", "hcho", "tvoc", "particles", "climate", "gas")
	switch metric {
	case "particles":
		metric = "pm25"
	case "climate":
		metric = "t"
	case "gas":
		metric = "hcho"
	}
	span := allow(q.Get("span"), "6h", "6h", "24h", "7d", "30d")
	kind := allow(q.Get("kind"), "all", "all", "commands", "events")
	days := 1
	if n, err := strconv.Atoi(q.Get("days")); err == nil && n >= 1 && n <= 180 {
		days = n
	}
	ro, locked, roSrc := a.readOnlyMeta()
	temp, tempSrc := a.tempMeta()
	poll, pollSrc := a.pollMeta()
	var aiStatus ai.KeyStatus
	if a.AIKeys != nil {
		aiStatus = a.AIKeys.Status()
	}
	prof := a.profileCopy()
	writes := !ro && prof.WriteVerified() && st.Status != "offline"
	s := web.Snapshot{
		Now: now, Loc: loc, Phase: a.Phase(), Page: name, Query: q,
		LoginError: loginErr, Email: a.Cfg.Email, Region: a.Cfg.Region,
		Name: nameOr(st.Device.Name), Status: st.Status, StatusAt: st.LastPoll,
		MQTT: st.MQTT, ReadOnly: ro, ReadOnlyLocked: locked, ReadOnlySource: roSrc,
		WritesOK: writes, Power: propInt(props, "2.1"), PowerKnown: propHas(props, "2.1"), PowerActionOK: prof.Controls.PowerAction,
		Mode: propInt(props, "2.3"), ModeKnown: propHas(props, "2.3"),
		Fan: propInt(props, "2.4"), FanKnown: propHas(props, "2.4"),
		FanMin: prof.Controls.FanMin, FanMax: prof.Controls.FanMax,
		FanForcesCustom:           prof.Controls.FanForcesCustom,
		ModeVals:                  prof.Controls.Mode,
		PM20Controls:              prof.Controls.IsPM20(),
		Heater:                    propInt(props, "2.6"),
		HeaterKnown:               propHas(props, "2.6"),
		HeaterMin:                 prof.Controls.HeaterMin,
		HeaterMax:                 prof.Controls.HeaterMax,
		Oscillation:               propInt(props, "2.7"),
		OscillationKnown:          propHas(props, "2.7"),
		OscillationVals:           prof.Controls.Oscillation,
		ContinuousMonitoring:      propInt(props, "6.15"),
		ContinuousMonitoringKnown: propHas(props, "6.15"),
		ContinuousMonitoringOK:    prof.Controls.ContinuousMonitoring,
		NearbyPowerOn:             propInt(props, "6.19"),
		NearbyPowerOnKnown:        propHas(props, "6.19"),
		NearbyPowerOnOK:           prof.Controls.NearbyPowerOn,
		FollowMotion:              propInt(props, "6.20"),
		FollowMotionKnown:         propHas(props, "6.20"),
		FollowMotionOK:            prof.Controls.FollowMotion,
		Light:                     propInt(props, "2.6"), LightKnown: propHas(props, "2.6"),
		LightKind: prof.Controls.LightKind, LightVals: prof.Controls.Light,
		Volume: propInt(props, "2.5"), VolumeKnown: propHas(props, "2.5"), VolumeVals: prof.Controls.Volume,
		Tone: propInt(props, "2.7"), ToneKnown: propHas(props, "2.7"), ToneVals: prof.Controls.Tone,
		AQ: propF(props, "3.12"), AQKnown: propHas(props, "3.12"),
		AQL: propInt(props, "3.4"), AQLKnown: propHas(props, "3.4"), AQLMap: prof.AQLevel,
		PM25: propF(props, "3.5"), PM25Known: propHas(props, "3.5"),
		PM10: propF(props, "3.6"), PM10Known: propHas(props, "3.6"),
		PM1: propF(props, "3.10"), PM1Known: propHas(props, "3.10"),
		TempC: propF(props, "3.3"), TempKnown: propHas(props, "3.3"),
		TempUnit: temp, TempSource: tempSrc,
		RH: propF(props, "3.2"), RHKnown: propHas(props, "3.2"),
		HCHO: propF(props, "3.7") / 1000, HCHOKnown: propHas(props, "3.7"),
		TVOC: propF(props, "3.8"), TVOCKnown: propHas(props, "3.8"),
		HEPA: propF(props, "4.1"), HEPAKnown: propHas(props, "4.1"),
		HEPADays: propF(props, "4.2"), HEPADaysKnown: propHas(props, "4.2"),
		Carbon: propF(props, "4.3"), CarbonKnown: propHas(props, "4.3"),
		CarbonDays: propF(props, "4.4"), CarbonDaysKnown: propHas(props, "4.4"),
		FilterHours: propF(props, "4.3"), FilterHoursKnown: propHas(props, "4.3"),
		Metric: metric, Span: span, Verified: prof.VerifiedBy,
		AQEstimate:     len(prof.AQLevel) == 0,
		OTAState:       st.OTA.State,
		OTAProgress:    progText(st.OTA),
		OTAWhen:        whenText(st.OTA.Changed, now, loc),
		OTAVersion:     otaVersionText(st),
		OTANewVersion:  st.OTA.NewVersion,
		OTAHasUpdate:   st.OTA.HasNew,
		OTATarget:      st.OTA.Target,
		OTADescription: st.OTA.Description,
		OTAChecked:     whenText(st.OTA.CheckedAt, now, loc),
		PollSeconds:    int(poll / time.Second), PollSource: pollSrc, HasOverrides: st.Overrides.HasAny(),
		OpenAIConfigured: aiStatus.Configured, OpenAISaved: aiStatus.Saved,
		OpenAIEnvLocked: aiStatus.EnvLocked, OpenAIHint: aiStatus.Hint,
		OpenAIModel: aiStatus.Model, OpenAISource: aiStatus.Source,
		Kind: kind, Days: days, Flash: flash, FlashInfo: flashInfo,
		Identity: identityRows(st.Device, props), Network: networkRows(a, st, calls),
	}
	applyPruned(&s, prof)
	if s.PowerKnown && s.Power == 2 && (!s.TempKnown || s.TempC == 0) && (!s.RHKnown || s.RH == 0) {
		s.PM25Known = false
		s.PM10Known = false
		s.PM1Known = false
		s.TempKnown = false
		s.RHKnown = false
		s.HCHOKnown = false
	}
	for _, auto := range prof.Controls.FanAutoModes {
		if s.ModeKnown && s.Mode == auto {
			s.FanAuto = true
		}
	}
	s.Banner = banner(st, s.Name, now, loc)
	if name == "activity" {
		rows := a.Store.ActivitySince(now.AddDate(0, 0, -days))
		for _, row := range rows {
			s.Rows = append(s.Rows, web.Act{TS: row.TS, Kind: row.Kind, Target: row.Target, Detail: row.Detail, Result: row.Result, MS: row.MS, Code: row.Code})
		}
	}
	if name == "device" || name == "" {
		s.Props = propRows(prof, props, now)
	}
	if name == "overview" || name == "" {
		s.Charts = a.allCharts(span, temp)
		for _, ch := range s.Charts {
			if ch.ID == metric {
				s.Series = ch.Series
				break
			}
		}
		if len(s.Series) == 0 && len(s.Charts) > 0 {
			s.Series = s.Charts[0].Series
		}
		if aiStatus.Configured {
			rep, ok, errText, busy, nextIn := a.dailyStatus(now)
			if ok {
				cp := rep
				s.DailyAI = &cp
			}
			s.DailyAIError = errText
			s.DailyAIBusy = busy
			s.DailyAINext = nextIn
		}
	}
	return s
}

func applyPruned(s *web.Snapshot, prof dreame.Profile) {
	s.Pruned = map[string]bool{}
	for _, spec := range prof.Properties {
		if !spec.Pruned {
			continue
		}
		id := fmt.Sprintf("%d.%d", spec.SIID, spec.PIID)
		s.Pruned[id] = true
		switch id {
		case "2.1":
			s.PowerKnown = false
		case "2.3":
			s.ModeKnown = false
		case "2.4":
			s.FanKnown = false
		case "2.5":
			s.VolumeKnown = false
		case "2.6":
			s.LightKnown = false
			s.HeaterKnown = false
		case "2.7":
			s.ToneKnown = false
			s.OscillationKnown = false
		case "6.15":
			s.ContinuousMonitoringKnown = false
		case "6.19":
			s.NearbyPowerOnKnown = false
		case "6.20":
			s.FollowMotionKnown = false
		case "3.12":
			s.AQKnown = false
		case "3.4":
			s.AQLKnown = false
		case "3.5":
			s.PM25Known = false
		case "3.6":
			s.PM10Known = false
		case "3.10":
			s.PM1Known = false
		case "3.3":
			s.TempKnown = false
		case "3.2":
			s.RHKnown = false
		case "3.7":
			s.HCHOKnown = false
		case "3.8":
			s.TVOCKnown = false
		case "4.1":
			s.HEPAKnown = false
		case "4.2":
			s.HEPADaysKnown = false
		case "4.3":
			s.FilterHoursKnown = false
			s.CarbonKnown = false
		case "4.4":
			s.CarbonDaysKnown = false
		}
	}
}

// Login starts an interactive Dreame session.
func (a *App) Login(ctx context.Context, email, password, region string) error {
	if region == "" {
		region = a.Cfg.Region
	}
	country := countries[region]
	if country == "" {
		country = a.Cfg.Country
	}
	err := a.Auth.Login(ctx, email, password, region, country)
	a.mu.Lock()
	if err != nil {
		a.loginErr = err.Error()
	} else {
		a.loginErr = ""
		a.signedOut.Store(false)
	}
	a.mu.Unlock()
	if err == nil {
		if loc, ok := a.Cloud.(interface{ SetLocale(string, string) }); ok {
			loc.SetLocale(region, country)
		}
		_ = a.Store.AppendEvent(store.EventRecord{Kind: "auth", Target: "login", Detail: "signed in"})
		a.ping()
	}
	return err
}

// Logout deletes the saved session and stays at the gate until the next sign-in.
func (a *App) Logout(context.Context) error {
	err := a.Auth.Logout()
	a.signedOut.Store(true)
	a.booted.Store(true)
	_ = a.Store.AppendEvent(store.EventRecord{Kind: "auth", Target: "logout", Detail: "signed out"})
	a.ping()
	return err
}

// Control runs one confirmed write.
func (a *App) Control(ctx context.Context, in web.ControlInput) error {
	a.ctrlMu.Lock()
	defer a.ctrlMu.Unlock()
	a.Worker.setInterval(a.pollInterval())
	err := a.Worker.Do(ctx, Request{
		Action: in.Action, SIID: in.SIID, PIID: in.PIID,
		Value: coerce(in.Value), Expected: coerce(in.Expected),
	})
	if err != nil {
		a.setFlash(err.Error(), false)
	}
	return err
}

// Poll runs one read cycle.
func (a *App) Poll(ctx context.Context) error {
	a.busy.Lock()
	defer a.busy.Unlock()
	a.Poller.setInterval(a.pollInterval())
	return a.Poller.PollOnce(ctx, true)
}

// CheckOTA checks cloud firmware availability (checkDeviceVersionV2) and live device OTA status.
func (a *App) CheckOTA(ctx context.Context) error {
	a.profMu.RLock()
	did := ""
	if a.prof != nil {
		did = a.prof.DID
	}
	a.profMu.RUnlock()
	if did == "" || a.Cloud == nil {
		return errors.New("no device")
	}
	ver, verr := a.Cloud.CheckDeviceVersion(ctx, did)
	state, serr := a.Cloud.GetOTAState(ctx, did)
	if verr != nil && serr != nil {
		a.setFlash(verr.Error(), false)
		return verr
	}
	var prog float64
	var raw string
	if serr == nil {
		prog, raw, _ = a.Cloud.GetOTAProgress(ctx, did)
	}
	now := time.Now()
	a.Store.Lock()
	ota := &a.Store.State().OTA
	prev := ota.State
	transition := false
	if serr == nil {
		if prev != "" && prev != state {
			transition = true
			ota.Changed = now
		}
		ota.State = state
		ota.Progress = prog
		ota.Raw = raw
		ota.At = now
	}
	if verr == nil {
		if ver.CurVersion != "" {
			ota.CurVersion = ver.CurVersion
		}
		ota.NewVersion = ver.NewVersion
		ota.HasNew = ver.HasNew
		ota.Target = ver.Target
		ota.Description = ver.Description
		ota.CheckedAt = now
	}
	curVer := otaVersionText(*a.Store.State())
	saveErr := a.Store.SaveState()
	a.Store.Unlock()

	if transition {
		_ = a.Store.AppendEvent(store.EventRecord{Kind: "ota", Target: "ota", Detail: prev + " -> " + state})
	}
	if a.Hub != nil {
		a.Hub.Publish(Event{Name: "ota", Data: `{"ok":true}`})
	}
	switch {
	case verr == nil && ver.HasNew:
		msg := "Firmware update available: " + ver.NewVersion
		if ver.Target != "" {
			msg += " (" + ver.Target + ")"
		}
		a.setFlash(msg+".", true)
	case serr == nil && state != "" && state != "idle" && state != "unknown":
		a.setFlash("OTA status: "+state+".", true)
	case curVer != "" && curVer != "—":
		a.setFlash("Firmware "+curVer+" is up to date.", true)
	default:
		a.setFlash("Firmware is up to date.", true)
	}
	return saveErr
}

// StartOTAUpdate triggers an official cloud firmware update after re-verifying update availability.
func (a *App) StartOTAUpdate(ctx context.Context) error {
	if a.readOnly() {
		err := &StatusError{Code: 403, Msg: "read-only mode is on"}
		a.setFlash(err.Error(), false)
		return err
	}
	a.profMu.RLock()
	did := ""
	if a.prof != nil {
		did = a.prof.DID
	}
	a.profMu.RUnlock()
	if did == "" || a.Cloud == nil {
		err := errors.New("no device")
		a.setFlash(err.Error(), false)
		return err
	}
	a.Store.Lock()
	offline := a.Store.State().Status == "offline"
	a.Store.Unlock()
	if offline {
		err := &StatusError{Code: 403, Msg: "device offline"}
		a.setFlash(err.Error(), false)
		return err
	}
	a.ctrlMu.Lock()
	defer a.ctrlMu.Unlock()
	ver, err := a.Cloud.CheckDeviceVersion(ctx, did)
	if err != nil {
		a.setFlash(err.Error(), false)
		return err
	}
	now := time.Now()
	a.Store.Lock()
	ota := &a.Store.State().OTA
	if ver.CurVersion != "" {
		ota.CurVersion = ver.CurVersion
	}
	ota.NewVersion = ver.NewVersion
	ota.HasNew = ver.HasNew
	ota.Target = ver.Target
	ota.Description = ver.Description
	ota.CheckedAt = now
	_ = a.Store.SaveState()
	a.Store.Unlock()

	if !ver.HasNew || (ver.Target != "COMM" && ver.Target != "MCU") {
		err := &StatusError{Code: 409, Msg: "No pending firmware update is available."}
		a.setFlash(err.Error(), false)
		return err
	}
	if ver.Status != 0 && ver.Status != 1 && ver.Status != 6 {
		err := &StatusError{Code: 409, Msg: "A firmware update is already in progress."}
		a.setFlash(err.Error(), false)
		return err
	}
	if err := a.Cloud.StartFirmwareUpdate(ctx, did, ver.Target); err != nil {
		a.setFlash(err.Error(), false)
		return err
	}
	a.Store.Lock()
	a.Store.State().OTA.State = "downloading"
	a.Store.State().OTA.Progress = 0
	a.Store.State().OTA.Raw = "0"
	a.Store.State().OTA.At = now
	a.Store.State().OTA.Changed = now
	saveErr := a.Store.SaveState()
	a.Store.Unlock()
	detail := fmt.Sprintf("update triggered (%s -> %s)", ver.Target, ver.NewVersion)
	_ = a.Store.AppendEvent(store.EventRecord{Kind: "ota", Target: "ota", Detail: detail})
	if a.Hub != nil {
		a.Hub.Publish(Event{Name: "ota", Data: `{"ok":true}`})
	}
	a.setFlash(fmt.Sprintf("Firmware update started (%s %s).", ver.Target, ver.NewVersion), true)
	return saveErr
}

func otaVersionText(st store.State) string {
	if st.OTA.CurVersion != "" {
		return st.OTA.CurVersion
	}
	if st.Device.Ver != "" {
		return st.Device.Ver
	}
	return "—"
}

// VerifyMap runs a read-only sweep and keeps write-probe bounds when they exist.
func (a *App) VerifyMap(ctx context.Context) error {
	a.busy.Lock()
	defer a.busy.Unlock()
	base := ctx
	if a.runCtx != nil {
		base = a.runCtx
	}
	vctx, cancel := context.WithTimeout(base, 3*time.Minute)
	defer cancel()
	text, err := a.verifyLocked(vctx)
	if err != nil {
		a.setFlash(err.Error(), false)
		return err
	}
	a.setFlash(text, true)
	return nil
}

// Dismiss clears the flash banner.
func (a *App) Dismiss() {
	a.mu.Lock()
	a.flash = ""
	a.flashInfo = false
	a.mu.Unlock()
}

// CloseHub unblocks server-sent event handlers during shutdown.
func (a *App) CloseHub() {
	if a.Hub != nil {
		a.Hub.Close()
	}
}

// Settings saves a runtime override.
func (a *App) Settings(_ context.Context, f web.SettingsForm) error {
	a.Store.Lock()
	defer a.Store.Unlock()
	st := a.Store.State()
	if f.Reset {
		st.Overrides = store.Overrides{}
		return a.Store.SaveState()
	}
	if f.Poll != "" {
		n, err := strconv.Atoi(f.Poll)
		if err != nil || n < 20 || n > 300 {
			return errors.New("poll interval")
		}
		st.Overrides.PollSeconds = &n
	}
	if f.TempUnit != "" {
		u := strings.ToUpper(f.TempUnit)
		if u != "C" && u != "F" {
			return errors.New("temperature unit")
		}
		st.Overrides.TempUnit = &u
	}
	if f.ReadOnly != "" && !a.Cfg.ReadOnly {
		on := f.ReadOnly == "true" || f.ReadOnly == "1"
		st.Overrides.ReadOnly = &on
	}
	return a.Store.SaveState()
}

// Subscribe forwards hub events to the HTTP server.
func (a *App) Subscribe() (<-chan web.SSE, func()) {
	in, cancel := a.Hub.Subscribe()
	out := make(chan web.SSE, 8)
	go func() {
		defer close(out)
		for ev := range in {
			select {
			case out <- web.SSE{Name: ev.Name, Data: ev.Data}:
			default:
			}
		}
	}()
	return out, cancel
}

func (a *App) verifyLocked(ctx context.Context) (string, error) {
	a.profMu.RLock()
	want := a.Cfg.DID
	if a.prof != nil && a.prof.DID != "" {
		want = a.prof.DID
	}
	prev := copyProfile(a.prof)
	a.profMu.RUnlock()
	devs, err := a.Cloud.ListDevices(ctx)
	if err != nil {
		return "", err
	}
	dev, err := pickDevice(devs, want)
	if err != nil {
		return "", err
	}
	if dev.BindDomain != "" {
		a.Cloud.SetPrefix(dev.BindDomain)
	}
	a.remember(dev)
	p, text, err := dreame.VerifyMap(ctx, a.Cloud, dev.DID)
	if err != nil {
		return "", err
	}
	if dev.Model != "" {
		p.Model = dev.Model
	}
	if dev.UID != "" {
		p.UID = dev.UID
	}
	if p.DID == "" {
		p.DID = dev.DID
	}
	if len(prev.AQLevel) > 0 {
		p.AQLevel = prev.AQLevel
	}
	if prev.HCHOScale != "" {
		p.HCHOScale = prev.HCHOScale
	}
	if prev.Filter2 != "" {
		p.Filter2 = prev.Filter2
	}
	if prev.WriteVerified() {
		p.VerifiedBy = prev.VerifiedBy
		p.Controls = prev.Controls
		type keptSpec struct {
			access string
			json   string
		}
		prevSpec := map[string]keptSpec{}
		for _, spec := range prev.Properties {
			prevSpec[fmt.Sprintf("%d.%d", spec.SIID, spec.PIID)] = keptSpec{spec.Access, spec.JSONType}
		}
		seen := map[string]bool{}
		for i := range p.Properties {
			key := fmt.Sprintf("%d.%d", p.Properties[i].SIID, p.Properties[i].PIID)
			seen[key] = true
			old, ok := prevSpec[key]
			if !ok {
				continue
			}
			if old.access != "" {
				p.Properties[i].Access = old.access
			}
			if old.json != "" {
				p.Properties[i].JSONType = old.json
			}
		}
		for _, spec := range prev.Properties {
			if spec.SIID != 2 && !(spec.SIID == 6 && spec.Access == "rw") {
				continue
			}
			key := fmt.Sprintf("%d.%d", spec.SIID, spec.PIID)
			if seen[key] {
				continue
			}
			p.Properties = append(p.Properties, spec)
			seen[key] = true
		}
	}
	if err := dreame.SaveProfile(a.Cfg.DataDir, p); err != nil {
		return text, err
	}
	a.setProfile(p)
	_ = a.Store.AppendEvent(store.EventRecord{Kind: "map", Target: "profile", Detail: "read-sweep"})
	if a.Hub != nil {
		a.Hub.Publish(Event{Name: "state", Data: `{"ok":true}`})
	}
	return text, nil
}

func (a *App) setProfile(p dreame.Profile) {
	a.profMu.Lock()
	defer a.profMu.Unlock()
	if a.prof == nil {
		a.prof = &dreame.Profile{}
	}
	cp := copyProfile(&p)
	*a.prof = cp
	a.Poller.Profile = a.prof
	a.Worker.Profile = a.prof
}

func (a *App) profileCopy() dreame.Profile {
	a.profMu.RLock()
	defer a.profMu.RUnlock()
	return copyProfile(a.prof)
}

func copyProfile(p *dreame.Profile) dreame.Profile {
	if p == nil {
		return dreame.Profile{}
	}
	cp := *p
	cp.Properties = append([]dreame.PropSpec(nil), p.Properties...)
	cp.Controls.Mode = append([]int(nil), p.Controls.Mode...)
	cp.Controls.FanAutoModes = append([]int(nil), p.Controls.FanAutoModes...)
	cp.Controls.Volume = append([]int(nil), p.Controls.Volume...)
	cp.Controls.Light = append([]int(nil), p.Controls.Light...)
	cp.Controls.Tone = append([]int(nil), p.Controls.Tone...)
	cp.Controls.Oscillation = append([]int(nil), p.Controls.Oscillation...)
	if p.AQLevel != nil {
		cp.AQLevel = make(map[string]string, len(p.AQLevel))
		for k, v := range p.AQLevel {
			cp.AQLevel[k] = v
		}
	}
	return cp
}

func (a *App) remember(d dreame.Device) {
	a.Store.Lock()
	defer a.Store.Unlock()
	st := a.Store.State()
	st.Device.DID = d.DID
	st.Device.Model = d.Model
	st.Device.UID = d.UID
	st.Device.Name = d.Name
	st.Device.SN = d.SN
	st.Device.MAC = d.MAC
	st.Device.Ver = d.Ver
	st.Device.BindDomain = d.BindDomain
	st.Device.Codename = d.Codename
	st.Device.Category = d.Category
	st.Device.Online = d.Online
}

func (a *App) readOnly() bool {
	on, _, _ := a.readOnlyMeta()
	return on
}

func (a *App) readOnlyMeta() (bool, bool, string) {
	if a.Cfg.ReadOnly {
		return true, true, "env"
	}
	a.Store.Lock()
	defer a.Store.Unlock()
	if a.Store.State().Overrides.ReadOnly != nil {
		return *a.Store.State().Overrides.ReadOnly, false, "saved"
	}
	return false, false, "env"
}

func (a *App) pollInterval() time.Duration {
	d, _ := a.pollMeta()
	return d
}

func (a *App) pollMeta() (time.Duration, string) {
	a.Store.Lock()
	defer a.Store.Unlock()
	if n := a.Store.State().Overrides.PollSeconds; n != nil && *n >= 20 && *n <= 300 {
		return time.Duration(*n) * time.Second, "saved"
	}
	d := a.Cfg.PollInterval
	if d <= 0 {
		d = 30 * time.Second
	}
	return d, "env"
}

func (a *App) tempMeta() (string, string) {
	a.Store.Lock()
	defer a.Store.Unlock()
	if u := a.Store.State().Overrides.TempUnit; u != nil && (*u == "C" || *u == "F") {
		return *u, "saved"
	}
	if a.Cfg.TempUnit != "" {
		return strings.ToUpper(a.Cfg.TempUnit), "env"
	}
	return "C", "env"
}

func (a *App) allCharts(span, unit string) []web.ChartGroup {
	gap := 3 * a.pollInterval()
	now := time.Now()
	hist := a.Store.HistoryMulti(now, span, []string{"pm25", "pm10", "pm1", "aq", "t", "rh", "hcho", "tvoc"}, gap)
	one := func(id, title, class, u string) web.ChartGroup {
		pts := samples(hist[id], id == "t" && unit == "F")
		if id == "hcho" {
			pts = scaleSamples(pts, 0.001)
		}
		return web.ChartGroup{
			ID: id, Title: title, Overlay: false,
			Series: []web.Series{{
				Name: title, Unit: u, Class: class, Fill: true,
				Points: pts,
			}},
		}
	}
	tu := "°C"
	if unit == "F" {
		tu = "°F"
	}
	return []web.ChartGroup{
		one("pm25", "PM2.5", "series-a", "µg/m³"),
		one("pm10", "PM10", "series-2", "µg/m³"),
		one("pm1", "PM1", "series-3", "µg/m³"),
		one("aq", "Air quality", "series-a", "%"),
		one("t", "Temperature", "series-a", tu),
		one("rh", "Humidity", "series-2", "%"),
		one("hcho", "HCHO", "series-a", "mg/m³"),
		one("tvoc", "TVOC", "series-2", "µg/m³"),
	}
}

func scaleSamples(in []web.Sample, factor float64) []web.Sample {
	out := make([]web.Sample, len(in))
	for i, p := range in {
		p.V *= factor
		p.Min *= factor
		p.Max *= factor
		out[i] = p
	}
	return out
}

func (a *App) setFlash(s string, info bool) {
	a.mu.Lock()
	a.flash = s
	a.flashInfo = info
	a.mu.Unlock()
}

func (a *App) ping() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

func (a *App) wait(ctx context.Context, d time.Duration) {
	if d < 0 {
		d = 0
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	case <-a.wake:
	}
}

// Jitter scales a duration by ±10% using unit in [0,1].
func Jitter(base time.Duration, unit float64) time.Duration {
	if unit < 0 {
		unit = 0
	}
	if unit > 1 {
		unit = 1
	}
	return time.Duration(float64(base) * (0.9 + 0.2*unit))
}

// NextWait doubles the interval once the device is offline, capped at 5 minutes.
func NextWait(base time.Duration, streak int) time.Duration {
	if base <= 0 {
		base = 30 * time.Second
	}
	if streak >= 5 {
		base *= 2
		if base > 5*time.Minute {
			base = 5 * time.Minute
		}
	}
	return base
}

func pickDevice(devs []dreame.Device, want string) (dreame.Device, error) {
	if want != "" {
		for _, d := range devs {
			if d.DID == want {
				return d, nil
			}
		}
		return dreame.Device{}, fmt.Errorf("device %s is not on this account", want)
	}
	var air []dreame.Device
	for _, d := range devs {
		if strings.Contains(d.Model, "airp") || strings.Contains(strings.ToLower(d.Category), "air") {
			air = append(air, d)
		}
	}
	if len(air) == 1 {
		return air[0], nil
	}
	if len(devs) == 1 {
		return devs[0], nil
	}
	return dreame.Device{}, errors.New("set DMD_DID")
}

func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var countries = map[string]string{"us": "US", "eu": "DE", "cn": "CN", "sg": "SG", "kr": "KR", "ru": "RU"}

func allow(v, fallback string, ok ...string) string {
	for _, item := range ok {
		if v == item {
			return v
		}
	}
	return fallback
}

func nameOr(s string) string {
	if s == "" {
		return "Air Purifier PM20"
	}
	return s
}

func propHas(p map[string]store.PropSnap, key string) bool {
	s, ok := p[key]
	return ok && s.Value != nil
}

func propF(p map[string]store.PropSnap, key string) float64 {
	s, ok := p[key]
	if !ok {
		return 0
	}
	switch n := s.Value.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case bool:
		if n {
			return 1
		}
		return 0
	default:
		return 0
	}
}

func propInt(p map[string]store.PropSnap, key string) int {
	return int(math.Round(propF(p, key)))
}

func coerce(s string) any {
	if s == "" {
		return ""
	}
	if i, err := strconv.Atoi(s); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

func samples(pts []store.Point, fahrenheit bool) []web.Sample {
	out := make([]web.Sample, len(pts))
	for i, p := range pts {
		v, lo, hi := p.V, p.Min, p.Max
		if fahrenheit {
			v = v*9/5 + 32
			lo = lo*9/5 + 32
			hi = hi*9/5 + 32
		}
		out[i] = web.Sample{TS: p.TS, V: v, Min: lo, Max: hi, Range: p.Range, Gap: p.Gap}
	}
	return out
}

func propRows(p dreame.Profile, props map[string]store.PropSnap, now time.Time) []web.PropRow {
	var rows []web.PropRow
	for _, spec := range p.Properties {
		id := fmt.Sprintf("%d.%d", spec.SIID, spec.PIID)
		snap := props[id]
		state := "Active"
		if spec.Access == "ro" && spec.SIID != 2 && spec.SIID != 3 && spec.SIID != 4 {
			state = "Read-only diagnostic"
		}
		if spec.Pruned {
			state = "Unsupported (-4003)"
		}
		val := "—"
		if snap.Value != nil && !spec.Pruned {
			val = fmt.Sprint(snap.Value)
		}
		rows = append(rows, web.PropRow{Key: spec.Key, ID: id, Value: val, Age: ageLabel(now, snap.At), State: state})
	}
	return rows
}

func identityRows(d store.DeviceSnap, props map[string]store.PropSnap) [][2]string {
	build := ""
	if i := strings.LastIndex(d.Ver, "_"); i >= 0 {
		build = d.Ver[i+1:]
	}
	if v, ok := props["1.4"]; ok && v.Value != nil {
		if clean := strings.Trim(strings.TrimSpace(fmt.Sprint(v.Value)), `"'`); clean != "" {
			build = clean
		}
	}
	hw := d.HW
	if hw == "" {
		hw = "Linux"
	}
	return [][2]string{
		{"Model", dash(d.Model)}, {"Codename", dash(d.Codename)}, {"DID", dash(d.DID)},
		{"Serial", dash(d.SN)}, {"MAC", dash(d.MAC)}, {"Firmware", dash(d.Ver)},
		{"Build", dash(build)}, {"Hardware", hw},
	}
}

func networkRows(a *App, st store.State, calls float64) [][2]string {
	mqtt := "Disabled"
	if a.Cfg.EnableMQTT {
		mqtt = st.MQTT
		if mqtt == "" {
			mqtt = "Reconnecting"
		}
	}
	online := "No"
	if st.Device.Online {
		online = "Yes"
	}
	rssi := "—"
	if st.OTC.HasRSSI {
		rssi = fmt.Sprintf("%d dBm", st.OTC.RSSI)
	}
	return [][2]string{
		{"LAN IP", dash(st.OTC.IP)}, {"Gateway", dash(st.OTC.GW)}, {"Wi-Fi", dash(st.OTC.SSID)},
		{"BSSID", dash(st.OTC.BSSID)}, {"RSSI", rssi}, {"Broker", dash(st.Device.BindDomain)},
		{"Cloud online", online}, {"Upstream", fmt.Sprintf("%.1f calls/min", calls)},
		{"MQTT", mqtt}, {"Session", tokenWhen(a.Auth, time.Now())},
	}
}

func banner(st store.State, name string, now time.Time, loc *time.Location) string {
	tail := "No readings have arrived yet."
	if !st.LastPoll.IsZero() {
		tail = "Showing readings from " + web.FormatWhen(st.LastPoll, now, loc) + "."
	}
	switch st.Status {
	case "degraded":
		return "The Dreame cloud reached the device slowly (code 80001). " + tail
	case "offline":
		return name + " is offline. " + tail
	default:
		return ""
	}
}

func tokenWhen(m *dreame.Manager, now time.Time) string {
	if m == nil {
		return "—"
	}
	exp := m.Current().ExpiresAt
	if exp == 0 {
		return "—"
	}
	d := time.Unix(exp, 0).Sub(now)
	if d < 0 {
		return "Expired"
	}
	return "Refreshes in " + humanDuration(d)
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(d.Seconds()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%d h %d m", h, m)
	}
	return fmt.Sprintf("%d m", int(d.Minutes()))
}

func ageLabel(now, at time.Time) string {
	if at.IsZero() {
		return "—"
	}
	d := now.Sub(at)
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%d s ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d m ago", int(d.Minutes()))
	}
	return fmt.Sprintf("%d h ago", int(d.Hours()))
}

func whenText(t, now time.Time, loc *time.Location) string {
	if t.IsZero() {
		return ""
	}
	return web.FormatWhen(t, now, loc)
}

func progText(o store.OTASnap) string {
	state := strings.ToLower(o.State)
	if state == "" || state == "idle" || state == "unknown" {
		return ""
	}
	if o.Raw != "" && o.Raw != "0" {
		return o.Raw
	}
	if o.Progress == 0 {
		return ""
	}
	return fmt.Sprintf("%.0f%%", o.Progress)
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
