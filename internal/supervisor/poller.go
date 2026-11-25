package supervisor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"dreame-dashboard/internal/dreame"
	"dreame-dashboard/internal/logx"
	"dreame-dashboard/internal/store"
)

// Cloud is the Dreame gateway surface the poller and control worker use.
type Cloud interface {
	ListDevices(context.Context) ([]dreame.Device, error)
	GetProperties(context.Context, string, []dreame.PropRef) ([]dreame.PropResult, error)
	SetProperties(context.Context, string, []dreame.PropWrite) error
	Action(context.Context, string, int, int, []any) (int, error)
	DevOTCInfo(context.Context, string) (dreame.OTC, error)
	GetOTAState(context.Context, string) (string, error)
	GetOTAProgress(context.Context, string) (float64, string, error)
	CheckDeviceVersion(context.Context, string) (dreame.OTAVersion, error)
	StartFirmwareUpdate(context.Context, string, string) error
	SetPrefix(string)
}

// Poller reads the device on two cadences.
type Poller struct {
	Cloud         Cloud
	Store         *store.Store
	Profile       *dreame.Profile
	ProfMu        *sync.RWMutex
	SaveProfile   func(*dreame.Profile) error
	Hub           *Hub
	Interval      time.Duration
	intervalNS    atomic.Int64
	MQTTInterval  time.Duration
	MQTTConnected func() bool
	OnReading     func(store.Reading)
	Now           func() time.Time
	ExternalCount bool
	lastSlow      time.Time
	lastOTA       time.Time
	lastVerCheck  time.Time
	otaActive     bool
}

func (p *Poller) setInterval(d time.Duration) { p.intervalNS.Store(int64(d)) }

func (p *Poller) interval() time.Duration {
	if n := p.intervalNS.Load(); n > 0 {
		return time.Duration(n)
	}
	return p.Interval
}

func (p *Poller) profile() (dreame.Profile, bool) {
	if p.ProfMu != nil {
		p.ProfMu.RLock()
		defer p.ProfMu.RUnlock()
	}
	if p.Profile == nil || p.Profile.DID == "" {
		return dreame.Profile{}, false
	}
	return copyProfile(p.Profile), true
}

func (p *Poller) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Poller) mqttOn() bool { return p.MQTTConnected != nil && p.MQTTConnected() }

// PollOnce runs one read cycle. full forces the slow set.
func (p *Poller) PollOnce(ctx context.Context, full bool) error {
	prof, ok := p.profile()
	if !ok {
		return errors.New("no device profile")
	}
	did := prof.DID
	now := p.now()
	hot := prof.ActiveRefs(true)
	slowDue := full || p.lastSlow.IsZero() || now.Sub(p.lastSlow) >= 5*time.Minute
	if p.mqttOn() {
		var sensors []dreame.PropRef
		for _, r := range hot {
			if r.SIID == 3 {
				sensors = append(sensors, r)
			}
		}
		hot = sensors
		if !full && !p.lastSlow.IsZero() && now.Sub(p.lastSlow) < p.mqttSlow() {
			slowDue = false
		}
	}
	var refs []dreame.PropRef
	refs = append(refs, hot...)
	var slow []dreame.PropRef
	if slowDue {
		slow = prof.ActiveRefs(false)
		if p.mqttOn() {
			for _, r := range prof.ActiveRefs(true) {
				if r.SIID == 2 {
					slow = append(slow, r)
				}
			}
		}
		refs = append(refs, slow...)
	}
	rows, err := p.Cloud.GetProperties(ctx, did, refs)
	p.noteCalls(batches(len(refs), 10), now)
	if err != nil {
		p.fail(now, err)
		return err
	}
	if slowDue {
		p.lastSlow = now
		p.reconcile(ctx, did, now)
	}
	p.apply(rows, now)
	if p.otaDue(now) {
		p.pollOTA(ctx, did, now)
	}
	p.Store.Lock()
	st := p.Store.State()
	st.Status = "online"
	st.Streak = 0
	st.LastPoll = now
	reading := readingFrom(st, now)
	p.Store.Unlock()
	if err := p.Store.AppendReading(reading); err != nil {
		logx.Warn("poll", "reading", "err", err.Error())
	}
	if p.OnReading != nil {
		p.OnReading(reading)
	}
	p.Store.Lock()
	err = p.Store.SaveState()
	p.Store.Unlock()
	if err != nil {
		return err
	}
	if p.Hub != nil {
		p.Hub.Publish(Event{Name: "state", Data: `{"ok":true}`})
	}
	logx.Info("poll", "poll ok", "props", fmt.Sprint(len(rows)))
	return nil
}

func (p *Poller) mqttSlow() time.Duration {
	if p.MQTTInterval <= 0 {
		return 5 * time.Minute
	}
	return p.MQTTInterval
}

func (p *Poller) noteCalls(n int, now time.Time) {
	if p.ExternalCount || n < 1 {
		return
	}
	p.Store.Lock()
	for i := 0; i < n; i++ {
		p.Store.NoteCall(now)
	}
	p.Store.Unlock()
}

func batches(n, size int) int {
	if n <= 0 {
		return 0
	}
	if size < 1 {
		size = 10
	}
	return (n + size - 1) / size
}

func (p *Poller) fail(_ time.Time, err error) {
	p.Store.Lock()
	st := p.Store.State()
	st.Streak++
	code, _ := dreame.CodeOf(err)
	if st.Streak >= 5 {
		st.Status = "offline"
		logx.Warn("poll", "poll offline", "streak", fmt.Sprint(st.Streak))
	} else if code == 80001 {
		st.Status = "degraded"
		logx.Warn("poll", "poll degraded", "code", "80001")
	} else {
		st.Status = "degraded"
		logx.Warn("poll", "poll degraded", "err", err.Error())
	}
	_ = p.Store.SaveState()
	p.Store.Unlock()
	if p.Hub != nil {
		p.Hub.Publish(Event{Name: "state", Data: `{"ok":true}`})
	}
}

func (p *Poller) apply(rows []dreame.PropResult, now time.Time) {
	type pair struct{ siid, piid int }
	var prune []pair
	p.Store.Lock()
	for _, row := range rows {
		key := fmt.Sprintf("%d.%d", row.SIID, row.PIID)
		if row.Code == -4003 {
			prune = append(prune, pair{row.SIID, row.PIID})
			continue
		}
		if row.Code != 0 && row.Has && row.Value == nil {
			continue
		}
		p.Store.State().Props[key] = store.PropSnap{Value: row.Value, At: now, Code: row.Code}
	}
	p.Store.Unlock()
	changed := false
	for _, item := range prune {
		if p.notePrune(item.siid, item.piid) {
			changed = true
		}
	}
	if changed {
		p.saveProf()
	}
}

func (p *Poller) notePrune(siid, piid int) bool {
	if p.ProfMu != nil {
		p.ProfMu.Lock()
		defer p.ProfMu.Unlock()
	}
	if p.Profile == nil {
		return false
	}
	for i := range p.Profile.Properties {
		s := &p.Profile.Properties[i]
		if s.SIID == siid && s.PIID == piid {
			s.Failures++
			if s.Failures >= 3 && !s.Pruned {
				s.Pruned = true
				logx.Info("poll", "prop pruned", "key", fmt.Sprintf("%d.%d", siid, piid))
				return true
			}
		}
	}
	return false
}

func (p *Poller) saveProf() {
	if p.SaveProfile == nil {
		return
	}
	if p.ProfMu != nil {
		p.ProfMu.RLock()
		defer p.ProfMu.RUnlock()
	}
	if p.Profile == nil {
		return
	}
	cp := copyProfile(p.Profile)
	_ = p.SaveProfile(&cp)
}

func (p *Poller) reconcile(ctx context.Context, did string, now time.Time) {
	if devs, err := p.Cloud.ListDevices(ctx); err == nil {
		p.noteCalls(1, now)
		for _, d := range devs {
			if d.DID == did {
				if d.BindDomain != "" {
					p.Cloud.SetPrefix(d.BindDomain)
				}
				var onlineEv *store.EventRecord
				p.Store.Lock()
				st := p.Store.State()
				prevOnline := st.Device.Online
				st.Device.DID = d.DID
				st.Device.Model = d.Model
				st.Device.UID = d.UID
				st.Device.SN = d.SN
				st.Device.MAC = d.MAC
				st.Device.Ver = d.Ver
				st.Device.Name = d.Name
				st.Device.Codename = d.Codename
				st.Device.BindDomain = d.BindDomain
				st.Device.Category = d.Category
				if prevOnline != d.Online && !st.CloudSince.IsZero() {
					onlineEv = &store.EventRecord{TS: now.Unix(), Kind: "presence", Target: "online", Detail: fmt.Sprintf("%v -> %v", prevOnline, d.Online)}
				}
				st.Device.Online = d.Online
				if st.CloudSince.IsZero() {
					st.CloudSince = now
				}
				p.Store.Unlock()
				if onlineEv != nil {
					_ = p.Store.AppendEvent(*onlineEv)
				}
				break
			}
		}
	}
	otc, err := p.Cloud.DevOTCInfo(ctx, did)
	if err != nil {
		return
	}
	p.noteCalls(1, now)
	var otcEv *store.EventRecord
	p.Store.Lock()
	prev := p.Store.State().OTC
	band := p.Store.State().RSSIBand
	next := band
	changed := false
	if otc.HasRSSI {
		next = rssiBand(otc.RSSI, band)
		if band != "" && next != band {
			changed = true
		}
	}
	if prev.IP != "" && otc.IP != "" && prev.IP != otc.IP {
		changed = true
	}
	if prev.BSSID != "" && otc.BSSID != "" && prev.BSSID != otc.BSSID {
		changed = true
	}
	if changed {
		otcEv = &store.EventRecord{TS: now.Unix(), Kind: "presence", Target: "otc", Detail: fmt.Sprintf("ip=%s bssid=%s rssi=%d", otc.IP, otc.BSSID, otc.RSSI)}
	}
	p.Store.State().OTC = store.OTCSnap{SSID: otc.SSID, BSSID: otc.BSSID, RSSI: otc.RSSI, HasRSSI: otc.HasRSSI, IP: otc.IP, Mask: otc.Mask, GW: otc.GW, At: now}
	p.Store.State().RSSIBand = next
	if otc.HWVer != "" {
		p.Store.State().Device.HW = otc.HWVer
	}
	if otc.FWVer != "" && p.Store.State().Device.Ver == "" {
		p.Store.State().Device.Ver = otc.FWVer
	}
	p.Store.Unlock()
	if otcEv != nil {
		_ = p.Store.AppendEvent(*otcEv)
		if p.Hub != nil {
			p.Hub.Publish(Event{Name: "presence", Data: `{"ok":true}`})
		}
	}
}

func rssiBand(rssi int, prev string) string {
	switch prev {
	case "high":
		if rssi <= -83 {
			return "low"
		}
		if rssi <= -73 {
			return "mid"
		}
		return "high"
	case "low":
		if rssi >= -67 {
			return "high"
		}
		if rssi >= -77 {
			return "mid"
		}
		return "low"
	case "mid":
		if rssi >= -67 {
			return "high"
		}
		if rssi <= -83 {
			return "low"
		}
		return "mid"
	default:
		if rssi > -70 {
			return "high"
		}
		if rssi <= -80 {
			return "low"
		}
		return "mid"
	}
}

func (p *Poller) otaDue(now time.Time) bool {
	if p.lastOTA.IsZero() {
		return true
	}
	if p.otaActive {
		return now.Sub(p.lastOTA) >= time.Minute
	}
	return now.Sub(p.lastOTA) >= 15*time.Minute
}

func (p *Poller) pollOTA(ctx context.Context, did string, now time.Time) {
	state, err := p.Cloud.GetOTAState(ctx, did)
	if err != nil {
		return
	}
	prog, raw, err := p.Cloud.GetOTAProgress(ctx, did)
	if err != nil {
		return
	}
	p.noteCalls(2, now)
	p.lastOTA = now
	p.otaActive = state != "" && state != "idle"
	var otaEv *store.EventRecord
	p.Store.Lock()
	prev := p.Store.State().OTA.State
	if prev != "" && prev != state {
		otaEv = &store.EventRecord{TS: now.Unix(), Kind: "ota", Target: "ota", Detail: prev + " -> " + state}
		p.Store.State().OTA.Changed = now
	}
	p.Store.State().OTA.State = state
	p.Store.State().OTA.Progress = prog
	p.Store.State().OTA.Raw = raw
	p.Store.State().OTA.At = now
	p.Store.Unlock()
	if otaEv != nil {
		_ = p.Store.AppendEvent(*otaEv)
		if p.Hub != nil {
			p.Hub.Publish(Event{Name: "ota", Data: state})
		}
		logx.Info("poll", "ota transition", "from", prev, "to", state)
	}
	if p.lastVerCheck.IsZero() || now.Sub(p.lastVerCheck) >= 6*time.Hour {
		ver, verr := p.Cloud.CheckDeviceVersion(ctx, did)
		if verr == nil {
			p.noteCalls(1, now)
			p.lastVerCheck = now
			var verEv *store.EventRecord
			p.Store.Lock()
			wasNew := p.Store.State().OTA.HasNew
			if ver.CurVersion != "" {
				p.Store.State().OTA.CurVersion = ver.CurVersion
			}
			p.Store.State().OTA.NewVersion = ver.NewVersion
			p.Store.State().OTA.HasNew = ver.HasNew
			p.Store.State().OTA.Target = ver.Target
			p.Store.State().OTA.Description = ver.Description
			p.Store.State().OTA.CheckedAt = now
			p.Store.Unlock()
			if ver.HasNew && !wasNew {
				verEv = &store.EventRecord{TS: now.Unix(), Kind: "ota", Target: "ota", Detail: fmt.Sprintf("update available: %s (%s)", ver.NewVersion, ver.Target)}
				_ = p.Store.AppendEvent(*verEv)
				if p.Hub != nil {
					p.Hub.Publish(Event{Name: "ota", Data: `{"ok":true}`})
				}
			}
		}
	}
}

func readingFrom(st *store.State, now time.Time) store.Reading {
	r := store.Reading{TS: now.Unix()}
	if v, ok := intProp(st, "2.1"); ok {
		r.Pwr = &v
	}
	if v, ok := intProp(st, "2.3"); ok {
		r.Mode = &v
	}
	if v, ok := intProp(st, "2.4"); ok {
		r.Fan = &v
	}
	if v, ok := floatProp(st, "3.12"); ok {
		r.AQ = &v
	}
	if v, ok := intProp(st, "3.4"); ok {
		r.AQL = &v
	}
	if v, ok := floatProp(st, "3.10"); ok {
		r.PM1 = &v
	}
	if v, ok := floatProp(st, "3.5"); ok {
		r.PM25 = &v
	}
	if v, ok := floatProp(st, "3.6"); ok {
		r.PM10 = &v
	}
	if v, ok := floatProp(st, "3.3"); ok {
		r.T = &v
	}
	if v, ok := floatProp(st, "3.2"); ok {
		r.RH = &v
	}
	if v, ok := floatProp(st, "3.7"); ok {
		r.HCHO = &v
	}
	if v, ok := floatProp(st, "3.8"); ok {
		r.TVOC = &v
	}
	if v, ok := floatProp(st, "4.1"); ok {
		r.HEPA = &v
	}
	return r
}

func intProp(st *store.State, key string) (int, bool) {
	f, ok := floatProp(st, key)
	if !ok {
		return 0, false
	}
	return int(f), true
}

func floatProp(st *store.State, key string) (float64, bool) {
	snap, ok := st.Props[key]
	if !ok || snap.Value == nil {
		return 0, false
	}
	switch n := snap.Value.(type) {
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
