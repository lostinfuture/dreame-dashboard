package mqtt

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ClientID is the vendor prefix plus 8 hex characters.
func ClientID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "p_" + hex.EncodeToString(b[:])
}

func encodeConnect(id, user, pass string) []byte {
	var body []byte
	body = append(body, 0, 4, 'M', 'Q', 'T', 'T', 4, 0xC2, 0, 50)
	body = appendMQTTString(body, id)
	body = appendMQTTString(body, user)
	body = appendMQTTString(body, pass)
	return append(append([]byte{0x10}, encodeLen(len(body))...), body...)
}

func encodeSubscribe(id uint16, topic string) []byte {
	body := []byte{byte(id >> 8), byte(id)}
	body = appendMQTTString(body, topic)
	body = append(body, 0)
	return append(append([]byte{0x82}, encodeLen(len(body))...), body...)
}

func encodePubAck(id uint16) []byte {
	return []byte{0x40, 0x02, byte(id >> 8), byte(id)}
}

func encodePing() []byte { return []byte{0xC0, 0x00} }

func encodeDisconnect() []byte { return []byte{0xE0, 0x00} }

func appendMQTTString(b []byte, s string) []byte {
	b = append(b, byte(len(s)>>8), byte(len(s)))
	return append(b, s...)
}

func encodeLen(n int) []byte {
	var b []byte
	for {
		d := byte(n % 128)
		n /= 128
		if n > 0 {
			d |= 0x80
		}
		b = append(b, d)
		if n == 0 {
			break
		}
	}
	return b
}

// ReadPacket reads one MQTT 3.1.1 packet.
func ReadPacket(r *bufio.Reader) (byte, []byte, error) {
	first, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	n, err := readLen(r)
	if err != nil {
		return 0, nil, err
	}
	if n > 1<<20 {
		return 0, nil, errors.New("mqtt packet too large")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return first, body, nil
}

func readLen(r *bufio.Reader) (int, error) {
	mul := 1
	val := 0
	for i := 0; i < 4; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		val += int(b&127) * mul
		if b&128 == 0 {
			return val, nil
		}
		mul *= 128
	}
	return 0, errors.New("bad remaining length")
}

// DecodePublish returns the topic and payload. QoS 1 payloads start at 4+topicLen.
func DecodePublish(first byte, body []byte) (topic string, payload []byte, qos int, id uint16, err error) {
	if len(body) < 2 {
		return "", nil, 0, 0, errors.New("short publish")
	}
	topicLen := int(body[0])<<8 | int(body[1])
	if len(body) < 2+topicLen {
		return "", nil, 0, 0, errors.New("short topic")
	}
	topic = string(body[2 : 2+topicLen])
	off := 2 + topicLen
	qos = int((first >> 1) & 0x03)
	if qos > 0 {
		if len(body) < off+2 {
			return "", nil, 0, 0, errors.New("short packet id")
		}
		id = uint16(body[off])<<8 | uint16(body[off+1])
		off += 2
	}
	if qos > 0 && off != 4+topicLen {
		return "", nil, 0, 0, fmt.Errorf("qos1 offset %d", off)
	}
	return topic, body[off:], qos, id, nil
}

// Prop is one pushed property.
type Prop struct {
	SIID  int
	PIID  int
	Value any
}

// Key is siid.piid.
func (p Prop) Key() string { return fmt.Sprintf("%d.%d", p.SIID, p.PIID) }

// Push is one decoded broker message.
type Push struct {
	Kind        string
	Props       []Prop
	OTAState    string
	OTAProgress string
	HW          string
	FW          string
	SSID        string
	BSSID       string
	IP          string
	Mask        string
	GW          string
	RSSI        int
	HasRSSI     bool
}

// Parse accepts properties_changed, props, and _otc.info envelopes.
func Parse(b []byte) (Push, error) {
	var env struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return Push{}, err
	}
	switch env.Method {
	case "properties_changed":
		var rows []struct {
			SIID  int `json:"siid"`
			PIID  int `json:"piid"`
			Value any `json:"value"`
		}
		if err := json.Unmarshal(env.Params, &rows); err != nil {
			return Push{}, err
		}
		p := Push{Kind: "properties"}
		for _, row := range rows {
			p.Props = append(p.Props, Prop{SIID: row.SIID, PIID: row.PIID, Value: row.Value})
		}
		return p, nil
	case "props":
		var m map[string]any
		if err := json.Unmarshal(env.Params, &m); err != nil {
			return Push{}, err
		}
		return Push{Kind: "props", OTAState: str(m["ota_state"]), OTAProgress: str(m["ota_progress"])}, nil
	case "_otc.info":
		var m map[string]any
		if err := json.Unmarshal(env.Params, &m); err != nil {
			return Push{}, err
		}
		p := Push{Kind: "otc", HW: str(m["hw_ver"]), FW: str(m["fw_ver"])}
		if ap, ok := m["ap"].(map[string]any); ok {
			p.SSID = str(ap["ssid"])
			p.BSSID = str(ap["bssid"])
			if v, ok := num(ap["rssi"]); ok {
				p.RSSI = int(v)
				p.HasRSSI = true
			}
		}
		if net, ok := m["netif"].(map[string]any); ok {
			p.IP = first(str(net["localIp"]), str(net["localip"]), str(net["ip"]))
			p.Mask = str(net["mask"])
			p.GW = first(str(net["gw"]), str(net["gateway"]))
		}
		return p, nil
	default:
		return Push{}, fmt.Errorf("unknown method")
	}
}

func str(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case float64:
		return fmt.Sprint(int(n))
	default:
		return ""
	}
}

func num(v any) (float64, bool) {
	n, ok := v.(float64)
	return n, ok
}

func first(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
