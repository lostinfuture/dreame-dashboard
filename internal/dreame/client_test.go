package dreame

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type staticAuth struct {
	access  string
	refresh int
}

func (s *staticAuth) Authorize(context.Context) (string, string, error) {
	return s.access, "000000", nil
}
func (s *staticAuth) ForceRefresh(context.Context) error {
	s.refresh++
	s.access = "fresh"
	return nil
}

func TestWireShapes(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	var sleeps []time.Duration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, r.URL.Path+" "+string(b))
		mu.Unlock()
		if !strings.Contains(r.URL.Path, "/dreame-iot-com-10000/device/sendCommand") {
			t.Errorf("path %s", r.URL.Path)
		}
		var env map[string]any
		if err := json.Unmarshal(b, &env); err != nil {
			t.Fatal(err)
		}
		if _, ok := env["did"].(string); !ok {
			t.Fatalf("did is not a string: %s", b)
		}
		data := env["data"].(map[string]any)
		method := data["method"].(string)
		switch method {
		case "get_properties", "set_properties":
			arr, ok := data["params"].([]any)
			if !ok || len(arr) > 10 {
				t.Fatalf("params %T len issue %s", data["params"], b)
			}
			w.Header().Set("Content-Type", "application/json")
			var rows []map[string]any
			for _, item := range arr {
				m := item.(map[string]any)
				rows = append(rows, map[string]any{"did": m["did"], "siid": m["siid"], "piid": m["piid"], "code": 0, "value": 1})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"result": rows}})
		case "action":
			if _, ok := data["params"].(map[string]any); !ok {
				t.Fatalf("action params must be object: %s", b)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"result": map[string]any{"code": 0}}})
		default:
			t.Fatal(method)
		}
	}))
	defer srv.Close()
	c := &Client{Region: "us", Prefix: "10000", BatchSize: 10, Auth: &staticAuth{access: "tok"}, HTTP: &http.Client{Transport: rewrite(srv.URL)}, Sleep: func(d time.Duration) { sleeps = append(sleeps, d) }}
	refs := make([]PropRef, 11)
	for i := range refs {
		refs[i] = PropRef{SIID: 3, PIID: i + 1}
	}
	rows, err := c.GetProperties(context.Background(), "-100000001", refs)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 11 {
		t.Fatal(len(rows))
	}
	if len(sleeps) != 1 || sleeps[0] < 250*time.Millisecond {
		t.Fatal(sleeps)
	}
	if err := c.SetProperties(context.Background(), "-100000001", []PropWrite{{SIID: 2, PIID: 3, Value: 2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Action(context.Background(), "-100000001", 2, 3, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(bodies, "\n")
	if strings.Contains(joined, `"-100000001"`) == false {
		t.Fatal(joined)
	}
	if strings.Contains(joined, `"did":-100000001`) {
		t.Fatal("numeric did")
	}
	if !strings.Contains(joined, `"aiid":3`) || strings.Contains(joined, `"params":[{"did":"-100000001","siid":2,"aiid"`) {
		t.Fatal(joined)
	}
}

func TestUnauthorizedRefreshesOnce(t *testing.T) {
	var n int
	auth := &staticAuth{access: "old"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if r.Header.Get("dreame-auth") == "bearer old" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"result": []any{map[string]any{"siid": 2, "piid": 1, "code": 0, "value": 1}}}})
	}))
	defer srv.Close()
	c := &Client{Region: "us", Prefix: "10000", Auth: auth, HTTP: &http.Client{Transport: rewrite(srv.URL)}}
	rows, err := c.GetProperties(context.Background(), "-1", []PropRef{{SIID: 2, PIID: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || auth.refresh != 1 || n != 2 {
		t.Fatalf("rows %d refresh %d calls %d", len(rows), auth.refresh, n)
	}
}

func TestCodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "get_ota_state") {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"result": []any{"dowloaded"}}})
			return
		}
		if strings.Contains(string(b), "get_ota_progress") && !strings.Contains(string(b), "progress-num") {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"result": "40"}})
			return
		}
		if strings.Contains(string(b), "slow") {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 80001, "msg": "设备可能不在线，指令发送超时"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"result": []any{
			map[string]any{"siid": 3, "piid": 9, "code": -4003},
			map[string]any{"siid": 3, "piid": 5, "code": 0, "value": 4},
		}}})
	}))
	defer srv.Close()
	c := &Client{Region: "us", Prefix: "10000", Auth: &staticAuth{access: "tok"}, HTTP: &http.Client{Transport: rewrite(srv.URL)}}
	rows, err := c.GetProperties(context.Background(), "-1", []PropRef{{SIID: 3, PIID: 9}, {SIID: 3, PIID: 5}})
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Code != -4003 || rows[1].Code != 0 {
		t.Fatal(rows)
	}
	_, err = c.GetProperties(context.Background(), "slow", []PropRef{{SIID: 1, PIID: 1}})
	code, ok := CodeOf(err)
	if !ok || code != 80001 {
		t.Fatal(err)
	}
	st, err := c.GetOTAState(context.Background(), "-1")
	if err != nil || st != "dowloaded" {
		t.Fatal(st, err)
	}
	n, raw, err := c.GetOTAProgress(context.Background(), "-1")
	if err != nil || n != 40 || raw != "40" {
		t.Fatal(n, raw, err)
	}
}

func TestPrefixAndList(t *testing.T) {
	if PrefixFromBind("10000.mt.us.iot.dreame.tech:19973") != "10000" {
		t.Fatal(PrefixFromBind("10000.mt.us.iot.dreame.tech:19973"))
	}
	h, err := RLCHeader("us", "en", "US")
	if err != nil || len(h) != 32 {
		t.Fatal(h, err)
	}
	h2, _ := RLCHeader("us", "en", "US")
	if h != h2 {
		t.Fatal("rlc unstable")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dreame-user-iot/iotuserbind/device/listV2" {
			t.Fatal(r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"page": map[string]any{"records": []any{
			map[string]any{"did": "-100000001", "model": "dreame.airp.u2402", "bindDomain": "10000.mt.us.iot.dreame.tech:19973", "masterUid": "U0000001", "online": true, "deviceInfo": map[string]any{"displayName": "Air Purifier PM20", "categoryPath": "/lifeapps/airp"}},
		}}}})
	}))
	defer srv.Close()
	c := &Client{Region: "us", Auth: &staticAuth{access: "tok"}, HTTP: &http.Client{Transport: rewrite(srv.URL)}}
	list, err := c.ListDevices(context.Background())
	if err != nil || len(list) != 1 || list[0].UID != "U0000001" || list[0].Name != "Air Purifier PM20" {
		t.Fatal(err, list)
	}
	c.SetPrefix(list[0].BindDomain)
	if c.Prefix != "10000" {
		t.Fatal(c.Prefix)
	}
}

func TestDevOTCInfoUnwrapsParams(t *testing.T) {
	const payload = `{"code":0,"data":{"otcInfo":{"partner_id":24,"params":{"hw_ver":"Linux","fw_ver":"1.8.17_1070","ap":{"ssid":"lab","bssid":"02:00:00:00:00:01","rssi":-40},"netif":{"localip":"192.0.2.10","mask":"255.255.255.0","gw":"192.0.2.1"}}}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dreame-user-iot/iotstatus/devOTCInfo" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()
	c := &Client{Region: "us", Auth: &staticAuth{access: "tok"}, HTTP: &http.Client{Transport: rewrite(srv.URL)}}
	otc, err := c.DevOTCInfo(context.Background(), "-100000001")
	if err != nil {
		t.Fatal(err)
	}
	if otc.HWVer != "Linux" || otc.FWVer != "1.8.17_1070" || otc.SSID != "lab" || otc.BSSID != "02:00:00:00:00:01" {
		t.Fatalf("%+v", otc)
	}
	if !otc.HasRSSI || otc.RSSI != -40 || otc.IP != "192.0.2.10" || otc.Mask != "255.255.255.0" || otc.GW != "192.0.2.1" {
		t.Fatalf("%+v", otc)
	}
}

func TestProgressNumber(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"result": 15}})
	}))
	defer srv.Close()
	c := &Client{Region: "us", Auth: &staticAuth{access: "tok"}, HTTP: &http.Client{Transport: rewrite(srv.URL)}}
	n, raw, err := c.GetOTAProgress(context.Background(), "-1")
	if err != nil || n != 15 || raw == "" {
		t.Fatal(n, raw, err)
	}
}

func TestActionRejectedReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"result": map[string]any{"code": -4004}}})
	}))
	defer srv.Close()
	c := &Client{Region: "us", Prefix: "10000", Auth: &staticAuth{access: "tok"}, HTTP: &http.Client{Transport: rewrite(srv.URL)}}
	code, err := c.Action(context.Background(), "-1", 2, 3, nil)
	if err == nil || code != -4004 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if got, ok := CodeOf(err); !ok || got != -4004 {
		t.Fatalf("CodeOf(%v) = %d, %v", err, got, ok)
	}
}

func TestCheckDeviceVersionAndStartUpdate(t *testing.T) {
	var updateBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/dreame-user-iot/iotuserbind/checkDeviceVersionV2":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "success": true,
				"data": map[string]any{
					"curVersion":     "1.8.17_1070",
					"newVersion":     "1.8.18_1075",
					"hasNewFirmware": true,
					"firmwareVersion": map[string]any{
						"MCU":  map[string]any{"hasNewFirmware": true, "status": 1, "description": "Stability fixes"},
						"COMM": map[string]any{"hasNewFirmware": false, "status": 1},
					},
				},
			})
		case "/dreame-user-iot/iotuserbind/manualFirmwareUpdateV2":
			updateBody = string(b)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "success": true, "data": map[string]any{"code": 0},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := &Client{Region: "us", Auth: &staticAuth{access: "tok"}, HTTP: &http.Client{Transport: rewrite(srv.URL)}}
	ver, err := c.CheckDeviceVersion(context.Background(), "-100000001")
	if err != nil {
		t.Fatal(err)
	}
	if !ver.HasNew || ver.Target != "MCU" || ver.CurVersion != "1.8.17_1070" || ver.NewVersion != "1.8.17_1075" || ver.Description != "Stability fixes" {
		t.Fatalf("unexpected ver: %+v", ver)
	}
	if err := c.StartFirmwareUpdate(context.Background(), "-100000001", "MCU"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updateBody, `"MCU":"update"`) || !strings.Contains(updateBody, `"-100000001"`) {
		t.Fatalf("unexpected update body: %s", updateBody)
	}
	if err := c.StartFirmwareUpdate(context.Background(), "-100000001", "BAD"); err == nil {
		t.Fatal("expected error for invalid target")
	}
}
