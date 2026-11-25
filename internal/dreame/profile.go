package dreame

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"dreame-dashboard/internal/filex"
)

// PropSpec is one realized or pruned property in device_profile.json.
type PropSpec struct {
	SIID     int    `json:"siid"`
	PIID     int    `json:"piid"`
	Key      string `json:"key"`
	JSONType string `json:"json_type,omitempty"`
	Access   string `json:"access"`
	Realized bool   `json:"realized"`
	Pruned   bool   `json:"pruned,omitempty"`
	Failures int    `json:"failures,omitempty"`
	Value    any    `json:"value,omitempty"`
}

// Controls records the bounds probe-writes observed.
type Controls struct {
	Mode                 []int  `json:"mode,omitempty"`
	FanMin               int    `json:"fan_min,omitempty"`
	FanMax               int    `json:"fan_max,omitempty"`
	FanAutoModes         []int  `json:"fan_auto_modes,omitempty"`
	FanForcesCustom      bool   `json:"fan_forces_custom,omitempty"`
	Volume               []int  `json:"volume,omitempty"`
	LightKind            string `json:"light_kind,omitempty"`
	Light                []int  `json:"light,omitempty"`
	Tone                 []int  `json:"tone,omitempty"`
	HeaterMin            int    `json:"heater_min,omitempty"`
	HeaterMax            int    `json:"heater_max,omitempty"`
	Oscillation          []int  `json:"oscillation,omitempty"`
	ContinuousMonitoring bool   `json:"continuous_monitoring,omitempty"`
	NearbyPowerOn        bool   `json:"nearby_power_on,omitempty"`
	FollowMotion         bool   `json:"follow_motion,omitempty"`
	PowerAction          bool   `json:"power_action,omitempty"`
}

// IsPM20 reports whether this profile uses the Dreame AirPursue PM20 (u2402)
// control map rather than legacy indicator/volume/key-tone controls.
func (c Controls) IsPM20() bool {
	return c.HeaterMax > 0 || len(c.Oscillation) > 0 || c.ContinuousMonitoring || c.NearbyPowerOn || c.FollowMotion
}

// Profile is device_profile.json.
type Profile struct {
	DID        string            `json:"did"`
	Model      string            `json:"model,omitempty"`
	UID        string            `json:"uid,omitempty"`
	VerifiedBy string            `json:"verified_by,omitempty"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Properties []PropSpec        `json:"properties"`
	AQLevel    map[string]string `json:"aq_level,omitempty"`
	HCHOScale  string            `json:"hcho_scale,omitempty"`
	Filter2    string            `json:"filter2,omitempty"`
	Controls   Controls          `json:"controls"`
}

type knownProp struct {
	Key    string
	Access string
}

var baseline = map[[2]int]knownProp{
	{1, 4}:  {"fw_build", "ro"},
	{2, 1}:  {"power", "ro"},
	{2, 2}:  {"error_code", "ro"},
	{2, 3}:  {"mode", "rw"},
	{2, 4}:  {"fan_speed", "rw"},
	{2, 5}:  {"humidity_target", "ro"},
	{2, 6}:  {"heater_temp", "rw"},
	{2, 7}:  {"oscillation", "rw"},
	{2, 8}:  {"vertical_airflow", "ro"},
	{3, 2}:  {"humidity", "ro"},
	{3, 3}:  {"temp_c", "ro"},
	{3, 4}:  {"aq_level", "ro"},
	{3, 5}:  {"pm25", "ro"},
	{3, 6}:  {"pm10", "ro"},
	{3, 7}:  {"hcho", "ro"},
	{3, 8}:  {"tvoc", "ro"},
	{3, 9}:  {"tvoc_dup", "ro"},
	{3, 10}: {"pm1", "ro"},
	{3, 12}: {"aq_pct", "ro"},
	{3, 13}: {"aq_driver", "ro"},
	{4, 1}:  {"hepa_pct", "ro"},
	{4, 2}:  {"hepa_days_left", "ro"},
	{4, 3}:  {"carbon_pct", "ro"},
	{4, 4}:  {"carbon_days_left", "ro"},
	{6, 14}: {"sensor_config", "ro"},
	{6, 15}: {"continuous_monitoring", "rw"},
	{6, 19}: {"nearby_power_on", "rw"},
	{6, 20}: {"follow_motion", "rw"},
}

// BaselineKeys returns the recon table of expected properties.
func BaselineKeys() map[string]string {
	out := map[string]string{}
	for id, k := range baseline {
		out[fmt.Sprintf("%d.%d", id[0], id[1])] = k.Key
	}
	return out
}

func specFor(siid, piid int) (string, string) {
	if k, ok := baseline[[2]int{siid, piid}]; ok {
		return k.Key, k.Access
	}
	return fmt.Sprintf("prop_%d_%d", siid, piid), "ro"
}

// ProfilePath is device_profile.json under the data directory.
func ProfilePath(dir string) string { return filepath.Join(dir, "device_profile.json") }

// LoadProfile reads device_profile.json.
func LoadProfile(dir string) (Profile, error) {
	b, err := os.ReadFile(ProfilePath(dir))
	if err != nil {
		return Profile{}, err
	}
	var p Profile
	if err := json.Unmarshal(b, &p); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// SaveProfile writes device_profile.json atomically.
func SaveProfile(dir string, p Profile) error {
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = time.Now().UTC()
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return filex.WriteAtomic(ProfilePath(dir), b)
}

// Prop returns the spec for a key, if present.
func (p Profile) Prop(key string) (PropSpec, bool) {
	for _, s := range p.Properties {
		if s.Key == key || fmt.Sprintf("%d.%d", s.SIID, s.PIID) == key {
			return s, true
		}
	}
	return PropSpec{}, false
}

// WriteVerified reports whether probe-writes has unlocked controls.
func (p Profile) WriteVerified() bool { return p.VerifiedBy == "write-probe" }

// ActiveRefs returns realized, unpruned properties for the poller.
func (p Profile) ActiveRefs(hot bool) []PropRef {
	var out []PropRef
	for _, s := range p.Properties {
		if !s.Realized || s.Pruned {
			continue
		}
		isHot := s.SIID == 2 || s.SIID == 3
		if hot == isHot {
			out = append(out, PropRef{SIID: s.SIID, PIID: s.PIID})
		}
	}
	return out
}

func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "bool"
	case float64, float32, int, int64, json.Number:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "other"
	}
}
