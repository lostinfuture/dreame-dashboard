package dreame

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Client is the Dreame HTTPS gateway client.
type Client struct {
	Region    string
	Country   string
	Lang      string
	SendRLC   bool
	BatchSize int
	Prefix    string
	HTTP      *http.Client
	Auth      TokenSource
	Sleep     func(time.Duration)
	OnRequest func()
	seq       atomic.Int64
	mu        sync.Mutex
}

var gatewayHTTP = &http.Client{Timeout: 20 * time.Second}

// TokenSource supplies an access token. ForceRefresh runs after HTTP 401.
type TokenSource interface {
	Authorize(ctx context.Context) (access string, tenant string, err error)
	ForceRefresh(ctx context.Context) error
}

// SetPrefix records the iotComPrefix parsed from bindDomain.
func (c *Client) SetPrefix(bindDomain string) {
	c.mu.Lock()
	c.Prefix = PrefixFromBind(bindDomain)
	c.mu.Unlock()
}

// SetLocale updates the region used for the gateway host after a successful login.
func (c *Client) SetLocale(region, country string) {
	c.mu.Lock()
	if region != "" {
		c.Region = region
	}
	if country != "" {
		c.Country = country
	}
	c.mu.Unlock()
}

func (c *Client) locale() (region, country, prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Region, c.Country, c.Prefix
}

func (c *Client) batch() int {
	n := c.BatchSize
	if n < 1 || n > 10 {
		return 10
	}
	return n
}

func (c *Client) pause() {
	if c.Sleep != nil {
		c.Sleep(250 * time.Millisecond)
		return
	}
	time.Sleep(250 * time.Millisecond)
}

func (c *Client) note() {
	if c.OnRequest != nil {
		c.OnRequest()
	}
}

// ListDevices calls listV2.
func (c *Client) ListDevices(ctx context.Context) ([]Device, error) {
	var raw json.RawMessage
	body := map[string]any{"current": 1, "size": 100, "sharedStatus": 1, "lang": "en", "timestamp": time.Now().UnixMilli()}
	if err := c.postJSON(ctx, "/dreame-user-iot/iotuserbind/device/listV2", body, &raw); err != nil {
		return nil, err
	}
	return parseDevices(raw), nil
}

// DeviceInfo calls the device info endpoint.
func (c *Client) DeviceInfo(ctx context.Context, did string) (Device, error) {
	var raw json.RawMessage
	if err := c.postJSON(ctx, "/dreame-user-iot/iotuserbind/device/info", map[string]any{"did": did}, &raw); err != nil {
		return Device{}, err
	}
	list := parseDevices(raw)
	if len(list) == 0 {
		d := parseDeviceObject(raw)
		return d, nil
	}
	return list[0], nil
}

// DevOTCInfo reads network telemetry.
func (c *Client) DevOTCInfo(ctx context.Context, did string) (OTC, error) {
	var raw json.RawMessage
	if err := c.postJSON(ctx, "/dreame-user-iot/iotstatus/devOTCInfo", map[string]any{"did": did}, &raw); err != nil {
		return OTC{}, err
	}
	return parseOTC(raw), nil
}

// GetProperties reads properties in chunks of at most 10.
func (c *Client) GetProperties(ctx context.Context, did string, refs []PropRef) ([]PropResult, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	size := c.batch()
	var all []PropResult
	for i := 0; i < len(refs); i += size {
		if i > 0 {
			c.pause()
		}
		end := i + size
		if end > len(refs) {
			end = len(refs)
		}
		params := make([]map[string]any, 0, end-i)
		for _, r := range refs[i:end] {
			params = append(params, map[string]any{"did": did, "siid": r.SIID, "piid": r.PIID})
		}
		rows, err := c.command(ctx, did, "get_properties", params)
		if err != nil {
			return all, err
		}
		all = append(all, rows...)
	}
	return all, nil
}

// SetProperties writes one or more properties. params is a JSON array.
func (c *Client) SetProperties(ctx context.Context, did string, writes []PropWrite) error {
	if len(writes) == 0 {
		return nil
	}
	size := c.batch()
	for i := 0; i < len(writes); i += size {
		if i > 0 {
			c.pause()
		}
		end := i + size
		if end > len(writes) {
			end = len(writes)
		}
		params := make([]map[string]any, 0, end-i)
		for _, w := range writes[i:end] {
			params = append(params, map[string]any{"did": did, "siid": w.SIID, "piid": w.PIID, "value": w.Value})
		}
		rows, err := c.command(ctx, did, "set_properties", params)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Has && row.Code != 0 {
				return &APIError{Op: "set_properties", Code: row.Code, Msg: "property rejected"}
			}
		}
	}
	return nil
}

// Action sends one action. params is a single JSON object, never an array.
func (c *Client) Action(ctx context.Context, did string, siid, aiid int, in []any) (int, error) {
	if in == nil {
		in = []any{}
	}
	params := map[string]any{"did": did, "siid": siid, "aiid": aiid, "in": in}
	rows, err := c.command(ctx, did, "action", params)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if rows[0].Has && rows[0].Code != 0 {
		return rows[0].Code, &APIError{Op: "action", Code: rows[0].Code, Msg: "action rejected"}
	}
	return rows[0].Code, nil
}

// GetOTAState reads miIO.get_ota_state and keeps the vendor spelling.
func (c *Client) GetOTAState(ctx context.Context, did string) (string, error) {
	raw, err := c.commandRaw(ctx, did, "miIO.get_ota_state", []any{})
	if err != nil {
		return "", err
	}
	return firstString(raw), nil
}

// GetOTAProgress reads miIO.get_ota_progress as a string or a number.
func (c *Client) GetOTAProgress(ctx context.Context, did string) (float64, string, error) {
	raw, err := c.commandRaw(ctx, did, "miIO.get_ota_progress", []any{})
	if err != nil {
		return 0, "", err
	}
	return asNumberOrString(raw)
}

// CheckDeviceVersion queries the cloud OTA metadata endpoint for newer COMM/MCU firmware.
func (c *Client) CheckDeviceVersion(ctx context.Context, did string) (OTAVersion, error) {
	lang := c.Lang
	if lang == "" {
		lang = "en"
	}
	type partInfo struct {
		HasNewFirmware bool   `json:"hasNewFirmware"`
		Status         int    `json:"status"`
		Description    string `json:"description"`
		NewVersion     string `json:"newVersion"`
	}
	var resp struct {
		CurVersion      string `json:"curVersion"`
		NewVersion      string `json:"newVersion"`
		HasNewFirmware  bool   `json:"hasNewFirmware"`
		Description     string `json:"description"`
		Status          int    `json:"status"`
		FirmwareVersion struct {
			MCU  *partInfo `json:"MCU"`
			COMM *partInfo `json:"COMM"`
		} `json:"firmwareVersion"`
	}
	if err := c.postJSON(ctx, "/dreame-user-iot/iotuserbind/checkDeviceVersionV2", map[string]any{"did": did, "lang": lang}, &resp); err != nil {
		return OTAVersion{}, err
	}
	out := OTAVersion{
		CurVersion:  strings.TrimSpace(resp.CurVersion),
		NewVersion:  strings.TrimSpace(resp.NewVersion),
		HasNew:      resp.HasNewFirmware,
		Status:      resp.Status,
		Description: strings.TrimSpace(resp.Description),
	}
	if resp.FirmwareVersion.COMM != nil && resp.FirmwareVersion.COMM.HasNewFirmware {
		out.HasNew = true
		out.Target = "COMM"
		out.Status = resp.FirmwareVersion.COMM.Status
		if d := strings.TrimSpace(resp.FirmwareVersion.COMM.Description); d != "" {
			out.Description = d
		}
		out.NewVersion = nextOTAVersion(out.CurVersion, firstNonEmpty(strings.TrimSpace(resp.FirmwareVersion.COMM.NewVersion), out.NewVersion), "COMM")
	} else if resp.FirmwareVersion.MCU != nil && resp.FirmwareVersion.MCU.HasNewFirmware {
		out.HasNew = true
		out.Target = "MCU"
		out.Status = resp.FirmwareVersion.MCU.Status
		if d := strings.TrimSpace(resp.FirmwareVersion.MCU.Description); d != "" {
			out.Description = d
		}
		out.NewVersion = nextOTAVersion(out.CurVersion, firstNonEmpty(strings.TrimSpace(resp.FirmwareVersion.MCU.NewVersion), out.NewVersion), "MCU")
	}
	return out, nil
}

// StartFirmwareUpdate triggers an official cloud-pushed OTA update for target ("COMM" or "MCU").
func (c *Client) StartFirmwareUpdate(ctx context.Context, did, target string) error {
	target = strings.ToUpper(strings.TrimSpace(target))
	if target != "COMM" && target != "MCU" {
		return &APIError{Op: "manualFirmwareUpdateV2", Code: 400, Msg: "invalid firmware target"}
	}
	body := map[string]any{
		"did":  did,
		"data": map[string]string{target: "update"},
	}
	var raw json.RawMessage
	if err := c.postJSON(ctx, "/dreame-user-iot/iotuserbind/manualFirmwareUpdateV2", body, &raw); err != nil {
		return err
	}
	var inner struct {
		Code *int   `json:"code"`
		Msg  string `json:"msg"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &inner) == nil && inner.Code != nil && *inner.Code != 0 {
		msg := strings.TrimSpace(inner.Msg)
		if msg == "" {
			msg = "firmware update rejected"
		}
		return &APIError{Op: "manualFirmwareUpdateV2", Code: *inner.Code, Msg: msg}
	}
	return nil
}

func nextOTAVersion(cur, next, target string) string {
	cur = strings.TrimSpace(cur)
	next = strings.TrimSpace(next)
	if cur == "" || next == "" {
		return firstNonEmpty(next, cur)
	}
	c := strings.Split(cur, "_")
	n := strings.Split(next, "_")
	if len(c) < 2 || len(n) < 2 {
		return next
	}
	if target == "COMM" {
		return n[0] + "_" + c[1]
	}
	return c[0] + "_" + n[1]
}

func (c *Client) command(ctx context.Context, did, method string, params any) ([]PropResult, error) {
	raw, err := c.commandRaw(ctx, did, method, params)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '[' {
		var rows []PropResult
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, err
		}
		return rows, nil
	}
	var one PropResult
	if json.Unmarshal(raw, &one) == nil && (one.SIID != 0 || one.Has) {
		return []PropResult{one}, nil
	}
	return nil, nil
}

func (c *Client) commandRaw(ctx context.Context, did, method string, params any) (json.RawMessage, error) {
	id := c.seq.Add(1)
	body := map[string]any{
		"did": did,
		"id":  id,
		"data": map[string]any{
			"did":    did,
			"id":     id,
			"method": method,
			"params": params,
		},
	}
	var data json.RawMessage
	path := "/dreame-iot-com-" + c.prefix() + "/device/sendCommand"
	if err := c.postJSON(ctx, path, body, &data); err != nil {
		return nil, err
	}
	return unwrapResult(data), nil
}

func (c *Client) prefix() string {
	_, _, prefix := c.locale()
	if prefix != "" {
		return prefix
	}
	return "10000"
}

func (c *Client) postJSON(ctx context.Context, path string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return c.post(ctx, path, raw, out, false)
}

func (c *Client) post(ctx context.Context, path string, body []byte, out any, refreshed bool) error {
	if c.Auth == nil {
		return ErrAuthRequired
	}
	access, tenant, err := c.Auth.Authorize(ctx)
	if err != nil {
		return err
	}
	region, country, _ := c.locale()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL(region)+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Authorization", BasicAuth)
	req.Header.Set("dreame-auth", "bearer "+access)
	req.Header.Set("dreame-meta", MetaCV)
	if tenant == "" {
		tenant = "000000"
	}
	req.Header.Set("Tenant-Id", tenant)
	if c.SendRLC {
		h, err := RLCHeader(region, c.Lang, country)
		if err != nil {
			return err
		}
		req.Header.Set("dreame-rlc", h)
	}
	c.note()
	resp, err := c.http().Do(req)
	if err != nil {
		return &APIError{Op: path, Msg: err.Error(), Temporary: true}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return &APIError{Op: path, Msg: err.Error(), Temporary: true, HTTP: resp.StatusCode}
	}
	if resp.StatusCode == http.StatusUnauthorized && !refreshed {
		if err := c.Auth.ForceRefresh(ctx); err != nil {
			return err
		}
		return c.post(ctx, path, body, out, true)
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return &APIError{Op: path, HTTP: resp.StatusCode, Msg: http.StatusText(resp.StatusCode), Temporary: true}
	}
	if resp.StatusCode >= 300 {
		return &APIError{Op: path, HTTP: resp.StatusCode, Msg: strings.TrimSpace(string(b))}
	}
	var env struct {
		Code    int             `json:"code"`
		Msg     string          `json:"msg"`
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return &APIError{Op: path, Msg: "bad json"}
	}
	if env.Code != 0 {
		temp := env.Code == 80001
		return &APIError{Op: path, Code: env.Code, Msg: env.Msg, Temporary: temp}
	}
	if out != nil && env.Data != nil {
		switch dest := out.(type) {
		case *json.RawMessage:
			*dest = env.Data
		default:
			if err := json.Unmarshal(env.Data, dest); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return gatewayHTTP
}

func unwrapResult(data json.RawMessage) json.RawMessage {
	if len(data) == 0 {
		return data
	}
	if data[0] == '[' || data[0] == '"' || data[0] == '-' || (data[0] >= '0' && data[0] <= '9') {
		return data
	}
	var wrap struct {
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(data, &wrap) == nil && len(wrap.Result) > 0 {
		return wrap.Result
	}
	return data
}

func firstString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var arr []any
	if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
		if s, ok := arr[0].(string); ok {
			return s
		}
		return fmt.Sprint(arr[0])
	}
	return strings.Trim(string(raw), `"`)
}

func asNumberOrString(raw json.RawMessage) (float64, string, error) {
	if len(raw) == 0 {
		return 0, "", nil
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return n, fmt.Sprint(n), nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		var f float64
		if _, err := fmt.Sscan(s, &f); err == nil {
			return f, s, nil
		}
		return 0, s, nil
	}
	var arr []any
	if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
		switch v := arr[0].(type) {
		case float64:
			return v, fmt.Sprint(v), nil
		case string:
			var f float64
			fmt.Sscan(v, &f)
			return f, v, nil
		}
	}
	return 0, string(raw), nil
}
