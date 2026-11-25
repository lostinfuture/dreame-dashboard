package dreame

import (
	"encoding/json"
	"strconv"
	"strings"
)

func parseDevices(raw json.RawMessage) []Device {
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '[' {
		var rows []json.RawMessage
		if json.Unmarshal(raw, &rows) != nil {
			return nil
		}
		out := make([]Device, 0, len(rows))
		for _, row := range rows {
			out = append(out, parseDeviceObject(row))
		}
		return out
	}
	var wrap map[string]json.RawMessage
	if json.Unmarshal(raw, &wrap) != nil {
		return nil
	}
	if page, ok := wrap["page"]; ok {
		return parseDevices(page)
	}
	if rec, ok := wrap["records"]; ok {
		return parseDevices(rec)
	}
	if _, ok := wrap["did"]; ok {
		return []Device{parseDeviceObject(raw)}
	}
	return nil
}

func parseDeviceObject(raw json.RawMessage) Device {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return Device{}
	}
	d := Device{
		DID:        stringField(m["did"]),
		Model:      stringField(m["model"]),
		UID:        firstNonEmpty(stringField(m["masterUid"]), stringField(m["uid"])),
		SN:         stringField(m["sn"]),
		MAC:        stringField(m["mac"]),
		Ver:        stringField(m["ver"]),
		BindDomain: stringField(m["bindDomain"]),
		Codename:   firstNonEmpty(stringField(m["codename"]), stringField(m["codeName"])),
	}
	if b, ok := m["online"].(bool); ok {
		d.Online = b
	}
	if info, ok := m["deviceInfo"].(map[string]any); ok {
		if d.Name == "" {
			d.Name = stringField(info["displayName"])
		}
		d.Category = stringField(info["categoryPath"])
		d.ProductID = stringField(info["productId"])
		if d.Codename == "" {
			d.Codename = stringField(info["codename"])
		}
	}
	if d.Name == "" {
		d.Name = stringField(m["displayName"])
	}
	if d.Name == "" {
		d.Name = stringField(m["customName"])
	}
	if prop, ok := m["property"].(string); ok && d.MAC == "" {
		var pm map[string]any
		if json.Unmarshal([]byte(prop), &pm) == nil {
			d.MAC = stringField(pm["mac"])
		}
	}
	return d
}

func parseOTC(raw json.RawMessage) OTC {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return OTC{}
	}
	if inner, ok := m["result"].(map[string]any); ok {
		m = inner
	}
	if inner, ok := m["data"].(map[string]any); ok {
		m = inner
	}
	if inner, ok := m["otcInfo"].(map[string]any); ok {
		m = inner
	}
	if inner, ok := m["params"].(map[string]any); ok {
		m = inner
	}
	otc := OTC{Raw: m, HWVer: stringField(m["hw_ver"]), FWVer: firstNonEmpty(stringField(m["fw_ver"]), stringField(m["fwVer"]))}
	if ap, ok := m["ap"].(map[string]any); ok {
		otc.SSID = stringField(ap["ssid"])
		otc.BSSID = stringField(ap["bssid"])
		if rssi, ok := number(ap["rssi"]); ok {
			otc.RSSI = int(rssi)
			otc.HasRSSI = true
		}
	}
	if net, ok := m["netif"].(map[string]any); ok {
		otc.IP = firstNonEmpty(stringField(net["localIp"]), stringField(net["localip"]), stringField(net["ip"]))
		otc.Mask = firstNonEmpty(stringField(net["mask"]), stringField(net["netmask"]))
		otc.GW = firstNonEmpty(stringField(net["gw"]), stringField(net["gateway"]))
	}
	if b, ok := m["online"].(bool); ok {
		otc.Online = &b
	}
	return otc
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	default:
		return 0, false
	}
}
