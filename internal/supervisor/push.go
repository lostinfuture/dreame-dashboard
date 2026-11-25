package supervisor

import (
	"context"
	"strings"
	"time"

	"dreame-dashboard/internal/logx"
	"dreame-dashboard/internal/mqtt"
	"dreame-dashboard/internal/store"
)

func (a *App) mqttLoop(ctx context.Context, c *mqtt.Client) {
	if c == nil {
		c = &mqtt.Client{
			CAFile: a.Cfg.MQTTCAFile, Insecure: a.Cfg.MQTTInsecureSkipVerify,
			ClientID: mqtt.ClientID(),
		}
		a.Auth.SetOnRefresh(func() {
			a.mqttRefresh.Store(true)
			c.Close()
		})
	}
	c.OnMessage = func(b []byte) {
		push, err := mqtt.Parse(b)
		if err != nil {
			logx.Debug("mqtt", "push ignored", "err", err.Error())
			return
		}
		a.applyPush(push, time.Now())
	}
	wait := 5 * time.Second
	for ctx.Err() == nil {
		a.Store.Lock()
		host := a.Store.State().Device.BindDomain
		did := a.Store.State().Device.DID
		uid := a.Store.State().Device.UID
		model := a.Store.State().Device.Model
		a.Store.Unlock()
		if host == "" || did == "" || uid == "" {
			a.wait(ctx, 5*time.Second)
			continue
		}
		access, _, err := a.Auth.Authorize(ctx)
		if err != nil {
			a.wait(ctx, wait)
			continue
		}
		c.Addr = host
		c.ServerName = host
		if i := strings.LastIndex(host, ":"); i >= 0 {
			c.ServerName = host[:i]
		}
		c.Username = uid
		c.Password = access
		region := a.Cfg.Region
		if cur := a.Auth.Current(); cur.Region != "" {
			region = cur.Region
		}
		c.Topic = "/status/" + did + "/" + uid + "/" + model + "/" + region + "/"
		c.OnReady = func() {
			wait = 5 * time.Second
			a.Store.Lock()
			a.Store.State().MQTT = "connected"
			a.Store.Unlock()
		}
		a.Store.Lock()
		a.Store.State().MQTT = "reconnecting"
		a.Store.Unlock()
		a.mqttRefresh.Store(false)
		err = c.ServeOnce(ctx)
		a.Store.Lock()
		if a.Store.State().MQTT == "connected" {
			a.Store.State().MQTT = "reconnecting"
		}
		a.Store.Unlock()
		if ctx.Err() != nil {
			return
		}
		if a.mqttRefresh.Swap(false) {
			wait = 5 * time.Second
			logx.Info("mqtt", "mqtt reconnecting after token refresh")
			continue
		}
		if err != nil {
			logx.Warn("mqtt", "mqtt reconnecting", "err", err.Error())
		}
		a.wait(ctx, wait)
		if wait < 5*time.Minute {
			wait *= 2
			if wait > 5*time.Minute {
				wait = 5 * time.Minute
			}
		}
	}
}

func (a *App) applyPush(p mqtt.Push, now time.Time) {
	var reading *store.Reading
	var ev *store.EventRecord
	var publish *Event
	a.Store.Lock()
	st := a.Store.State()
	switch p.Kind {
	case "properties":
		for _, prop := range p.Props {
			key := prop.Key()
			st.Props[key] = store.PropSnap{Value: prop.Value, At: now, Code: 0}
		}
		if a.lastRead.IsZero() || now.Sub(a.lastRead) >= 10*time.Second {
			a.lastRead = now
			r := readingFrom(st, now)
			reading = &r
		}
		st.Status = "online"
		st.LastPoll = now
		publish = &Event{Name: "state", Data: `{"ok":true}`}
	case "props":
		prev := st.OTA.State
		if p.OTAState != "" {
			st.OTA.State = p.OTAState
		}
		if p.OTAProgress != "" {
			st.OTA.Raw = p.OTAProgress
		}
		st.OTA.At = now
		if prev != "" && p.OTAState != "" && prev != p.OTAState {
			st.OTA.Changed = now
			ev = &store.EventRecord{TS: now.Unix(), Kind: "ota", Target: "ota", Detail: prev + " -> " + p.OTAState}
			publish = &Event{Name: "ota", Data: `{"ok":true}`}
		}
	case "otc":
		prev := st.OTC
		band := st.RSSIBand
		next := band
		changed := false
		if p.HasRSSI {
			next = rssiBand(p.RSSI, band)
			if band != "" && next != band {
				changed = true
			}
		}
		if prev.IP != "" && p.IP != "" && prev.IP != p.IP {
			changed = true
		}
		if prev.BSSID != "" && p.BSSID != "" && prev.BSSID != p.BSSID {
			changed = true
		}
		if changed {
			ev = &store.EventRecord{TS: now.Unix(), Kind: "presence", Target: "otc", Detail: "ip=" + p.IP + " bssid=" + p.BSSID}
			publish = &Event{Name: "presence", Data: `{"ok":true}`}
		}
		st.OTC = store.OTCSnap{SSID: p.SSID, BSSID: p.BSSID, RSSI: p.RSSI, HasRSSI: p.HasRSSI, IP: p.IP, Mask: p.Mask, GW: p.GW, At: now}
		st.RSSIBand = next
		if p.HW != "" {
			st.Device.HW = p.HW
		}
	}
	a.Store.Unlock()
	if reading != nil {
		_ = a.Store.AppendReading(*reading)
		a.NoteReading(*reading)
	}
	if ev != nil {
		_ = a.Store.AppendEvent(*ev)
	}
	if publish != nil && a.Hub != nil {
		a.Hub.Publish(*publish)
	}
}
