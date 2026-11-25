package store

import (
	"encoding/json"
	"os"
	"time"

	"dreame-dashboard/internal/logx"
)

// PropSnap is the latest value of one property.
type PropSnap struct {
	Value any       `json:"value,omitempty"`
	At    time.Time `json:"at"`
	Code  int       `json:"code,omitempty"`
}

// DeviceSnap is identity copied from listV2.
type DeviceSnap struct {
	DID        string `json:"did,omitempty"`
	Model      string `json:"model,omitempty"`
	UID        string `json:"uid,omitempty"`
	SN         string `json:"sn,omitempty"`
	MAC        string `json:"mac,omitempty"`
	Ver        string `json:"ver,omitempty"`
	Name       string `json:"name,omitempty"`
	Codename   string `json:"codename,omitempty"`
	BindDomain string `json:"bind_domain,omitempty"`
	Category   string `json:"category,omitempty"`
	HW         string `json:"hw,omitempty"`
	Online     bool   `json:"online,omitempty"`
}

// OTASnap is the latest firmware update read.
type OTASnap struct {
	State       string    `json:"state,omitempty"`
	Progress    float64   `json:"progress,omitempty"`
	Raw         string    `json:"raw,omitempty"`
	CurVersion  string    `json:"cur_version,omitempty"`
	NewVersion  string    `json:"new_version,omitempty"`
	HasNew      bool      `json:"has_new,omitempty"`
	Target      string    `json:"target,omitempty"`
	Description string    `json:"description,omitempty"`
	CheckedAt   time.Time `json:"checked_at,omitempty"`
	At          time.Time `json:"at,omitempty"`
	Changed     time.Time `json:"changed,omitempty"`
}

// OTCSnap is network telemetry.
type OTCSnap struct {
	SSID    string    `json:"ssid,omitempty"`
	BSSID   string    `json:"bssid,omitempty"`
	RSSI    int       `json:"rssi,omitempty"`
	HasRSSI bool      `json:"has_rssi,omitempty"`
	IP      string    `json:"ip,omitempty"`
	Mask    string    `json:"mask,omitempty"`
	GW      string    `json:"gw,omitempty"`
	At      time.Time `json:"at,omitempty"`
}

// Overrides are values saved from /admin/settings.
type Overrides struct {
	PollSeconds *int    `json:"poll_seconds,omitempty"`
	TempUnit    *string `json:"temp_unit,omitempty"`
	ReadOnly    *bool   `json:"read_only,omitempty"`
}

// HasAny reports whether the operator saved a runtime override.
func (o Overrides) HasAny() bool {
	return o.PollSeconds != nil || o.TempUnit != nil || o.ReadOnly != nil
}

// State is state.json.
type State struct {
	Version    int                 `json:"version"`
	UpdatedAt  time.Time           `json:"updated_at"`
	Device     DeviceSnap          `json:"device"`
	Props      map[string]PropSnap `json:"props"`
	OTA        OTASnap             `json:"ota"`
	OTC        OTCSnap             `json:"otc"`
	Overrides  Overrides           `json:"overrides"`
	LastPoll   time.Time           `json:"last_poll"`
	Status     string              `json:"status"`
	Streak     int                 `json:"streak"`
	MQTT       string              `json:"mqtt,omitempty"`
	AuthExp    int64               `json:"auth_exp,omitempty"`
	Calls      []int64             `json:"calls,omitempty"`
	RSSIBand   string              `json:"rssi_band,omitempty"`
	CloudSince time.Time           `json:"cloud_since,omitempty"`
}

func newState() *State {
	return &State{Version: 1, Status: "boot", Props: map[string]PropSnap{}}
}

func (s *Store) loadState() {
	b, err := os.ReadFile(s.path("state.json"))
	if err != nil {
		if !os.IsNotExist(err) {
			logx.Warn("store", "state.json rebuilt", "err", err.Error())
		}
		s.state = newState()
		return
	}
	var st State
	if json.Unmarshal(b, &st) != nil || st.Version != 1 {
		logx.Warn("store", "state.json rebuilt", "err", "invalid")
		s.state = newState()
		return
	}
	if st.Props == nil {
		st.Props = map[string]PropSnap{}
	}
	s.state = &st
}

// State returns the in-memory snapshot. Callers must hold the store lock.
func (s *Store) State() *State { return s.state }

// SaveState writes state.json.
func (s *Store) SaveState() error {
	s.state.UpdatedAt = time.Now().UTC()
	if s.state.Version == 0 {
		s.state.Version = 1
	}
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeAtomic(s.path("state.json"), b)
}

// NoteCall records one upstream request time and drops entries older than a minute.
func (s *Store) NoteCall(at time.Time) {
	cut := at.Add(-time.Minute).UnixNano()
	next := s.state.Calls[:0]
	for _, n := range s.state.Calls {
		if n >= cut {
			next = append(next, n)
		}
	}
	s.state.Calls = append(next, at.UnixNano())
}

// CallsPerMin is the count of NoteCall timestamps in the last minute.
func (s *Store) CallsPerMin(now time.Time) float64 {
	cut := now.Add(-time.Minute).UnixNano()
	n := 0
	for _, ts := range s.state.Calls {
		if ts >= cut {
			n++
		}
	}
	return float64(n)
}
