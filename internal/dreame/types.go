package dreame

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// PropRef identifies one property.
type PropRef struct {
	SIID int
	PIID int
}

// Key is "siid.piid".
func (p PropRef) Key() string { return fmt.Sprintf("%d.%d", p.SIID, p.PIID) }

// PropWrite is one set_properties entry.
type PropWrite struct {
	SIID  int
	PIID  int
	Value any
}

// PropResult is one get_properties row.
type PropResult struct {
	DID   string
	SIID  int
	PIID  int
	Value any
	Code  int
	Has   bool
}

// UnmarshalJSON accepts numeric or string did values from the gateway.
func (p *PropResult) UnmarshalJSON(b []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	p.DID = rawString(m["did"])
	p.SIID = rawInt(m["siid"])
	p.PIID = rawInt(m["piid"])
	if c, ok := m["code"]; ok {
		p.Code = rawInt(c)
		p.Has = true
	}
	if v, ok := m["value"]; ok && string(v) != "null" {
		var anyv any
		if err := json.Unmarshal(v, &anyv); err != nil {
			return err
		}
		p.Value = anyv
	}
	return nil
}

// Device is one listV2 record the dashboard cares about.
type Device struct {
	DID        string
	Model      string
	UID        string
	SN         string
	MAC        string
	Ver        string
	BindDomain string
	Online     bool
	Name       string
	Codename   string
	Category   string
	ProductID  string
}

// OTC is the devOTCInfo payload kept for the device page.
type OTC struct {
	HWVer   string
	FWVer   string
	SSID    string
	BSSID   string
	RSSI    int
	HasRSSI bool
	IP      string
	Mask    string
	GW      string
	Online  *bool
	Raw     map[string]any
}

// OTAVersion is the cloud checkDeviceVersionV2 result.
type OTAVersion struct {
	CurVersion  string
	NewVersion  string
	HasNew      bool
	Target      string // "COMM" or "MCU"
	Status      int
	Description string
}

// APIError is a gateway or transport failure.
type APIError struct {
	Op        string
	HTTP      int
	Code      int
	Msg       string
	Temporary bool
	Invalid   bool
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	msg := e.Msg
	if msg == "" {
		msg = "request failed"
	}
	return fmt.Sprintf("%s http=%d code=%d %s", e.Op, e.HTTP, e.Code, msg)
}

// CodeOf returns the Dreame code when err is an APIError.
func CodeOf(err error) (int, bool) {
	var e *APIError
	if errors.As(err, &e) {
		return e.Code, true
	}
	return 0, false
}

func rawString(b json.RawMessage) string {
	if len(b) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(b, &n) == nil {
		return n.String()
	}
	return strings.Trim(string(b), `"`)
}

func rawInt(b json.RawMessage) int {
	if len(b) == 0 {
		return 0
	}
	var n int
	if json.Unmarshal(b, &n) == nil {
		return n
	}
	var f float64
	if json.Unmarshal(b, &f) == nil {
		return int(f)
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		i, _ := strconv.Atoi(s)
		return i
	}
	return 0
}
