package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"dreame-dashboard/internal/config"
	"dreame-dashboard/internal/dreame"
	"dreame-dashboard/internal/logx"
	"dreame-dashboard/internal/mqtt"
	"dreame-dashboard/internal/store"
	"dreame-dashboard/internal/web"
)

// safeCloud is a Cloud double whose counters are safe to hit from loop and HTTP together.
type safeCloud struct {
	mu sync.Mutex
	st *store.Store
	n  int
}

func (c *safeCloud) note() {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	if c.st == nil {
		return
	}
	c.st.Lock()
	c.st.NoteCall(time.Now())
	c.st.Unlock()
}

func (c *safeCloud) ListDevices(context.Context) ([]dreame.Device, error) {
	c.note()
	return []dreame.Device{{
		DID: "-1", Model: "dreame.airp.u2402", UID: "U0000001",
		BindDomain: "127.0.0.1:1", Name: "Air Purifier PM20", Online: true,
	}}, nil
}

func (c *safeCloud) GetProperties(_ context.Context, _ string, refs []dreame.PropRef) ([]dreame.PropResult, error) {
	c.note()
	out := make([]dreame.PropResult, 0, len(refs))
	for _, r := range refs {
		out = append(out, dreame.PropResult{SIID: r.SIID, PIID: r.PIID, Code: 0, Has: true, Value: float64(1)})
	}
	return out, nil
}

func (c *safeCloud) SetProperties(context.Context, string, []dreame.PropWrite) error {
	c.note()
	return nil
}

func (c *safeCloud) Action(context.Context, string, int, int, []any) (int, error) {
	c.note()
	return 0, nil
}

func (c *safeCloud) DevOTCInfo(context.Context, string) (dreame.OTC, error) {
	c.note()
	return dreame.OTC{IP: "192.0.2.10", FWVer: "1.8.17_1070", RSSI: -40, HasRSSI: true}, nil
}

func (c *safeCloud) GetOTAState(context.Context, string) (string, error) {
	c.note()
	return "idle", nil
}

func (c *safeCloud) GetOTAProgress(context.Context, string) (float64, string, error) {
	c.note()
	return 0, "0", nil
}

func (c *safeCloud) CheckDeviceVersion(context.Context, string) (dreame.OTAVersion, error) {
	c.note()
	return dreame.OTAVersion{CurVersion: "1.8.17_1070"}, nil
}

func (c *safeCloud) StartFirmwareUpdate(context.Context, string, string) error {
	c.note()
	return nil
}

func (c *safeCloud) SetPrefix(string) { c.note() }

func TestConcurrentHTTPAndLoops(t *testing.T) {
	dir := t.TempDir()
	exp := time.Now().Add(2 * time.Hour).Unix()
	raw, err := json.Marshal(dreame.Tokens{
		AccessToken: "race-access-token", RefreshToken: "race-refresh",
		TenantID: "000000", UID: "U0000001", Region: "us", ExpiresAt: exp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokens.json"), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.Lock()
	st.State().Device.DID = "-1"
	st.State().Device.UID = "U0000001"
	st.State().Device.Model = "dreame.airp.u2402"
	st.State().Device.BindDomain = "127.0.0.1:1"
	st.State().Device.Name = "Air Purifier PM20"
	st.State().Status = "online"
	st.Unlock()

	auth := dreame.NewManager(filepath.Join(dir, "tokens.json"))
	auth.Region = "us"
	auth.Country = "US"
	cloud := &safeCloud{st: st}
	cfg := config.Config{
		DataDir: dir, RunDir: dir, Region: "us", Country: "US",
		TempUnit: "C", PollInterval: 20 * time.Millisecond, Location: time.UTC,
	}
	app := NewApp(cfg, st, auth, cloud)
	app.setProfile(*testProfile())
	app.booted.Store(true)
	app.Worker.Sleep = func(time.Duration) {}
	app.Poller.ExternalCount = true

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var loops sync.WaitGroup
	loops.Add(3)
	go func() {
		defer loops.Done()
		app.loop(ctx)
	}()
	go func() {
		defer loops.Done()
		app.healthLoop(ctx)
	}()
	go func() {
		defer loops.Done()
		app.mqttLoop(ctx, &mqtt.Client{
			Dial: func(context.Context, string, string) (net.Conn, error) {
				return nil, errors.New("down")
			},
		})
	}()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := web.NewServer("tok", app)
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ctx, ln) }()

	base := "http://" + ln.Addr().String()
	client := &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := client.Get(base + "/")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	hammer, stopHammer := context.WithTimeout(ctx, 500*time.Millisecond)
	var hits sync.WaitGroup
	posts := []string{
		"/poll", "/control", "/ota/check", "/admin/verify-map", "/admin/settings", "/dismiss", "/login",
	}
	gets := []string{"/", "/device", "/activity", "/api/state", "/api/history?metric=pm25&span=24h", "/api/ai/report?scope=general&span=6h", "/page.css", "/chart.js", "/app.js"}
	for i := 0; i < 6; i++ {
		hits.Add(1)
		go func(n int) {
			defer hits.Done()
			for hammer.Err() == nil {
				logx.Info("race", "tick", "n", "1")
				app.writeHealth()
				app.applyPush(mqtt.Push{Kind: "properties", Props: []mqtt.Prop{{SIID: 3, PIID: 5, Value: float64(4)}}}, time.Now())
				_ = app.Store.Backfill(time.Now().UTC())
				_ = app.Store.Retain(time.Now().UTC(), 90)
				if n%2 == 0 {
					reqCtx, cancelReq := context.WithTimeout(hammer, 80*time.Millisecond)
					req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, base+"/events", nil)
					if err == nil {
						resp, err := client.Do(req)
						if err == nil {
							_, _ = io.Copy(io.Discard, resp.Body)
							resp.Body.Close()
						}
					}
					cancelReq()
				}
				for _, path := range gets {
					resp, err := client.Get(base + path)
					if err == nil {
						_, _ = io.Copy(io.Discard, resp.Body)
						resp.Body.Close()
					}
				}
				form := url.Values{}
				form.Set("token", "tok")
				form.Set("confirmed", "1")
				form.Set("action", "set")
				form.Set("siid", "2")
				form.Set("piid", "3")
				form.Set("value", "2")
				form.Set("poll", "30")
				form.Set("temp_unit", "C")
				resp, err := client.PostForm(base+posts[n%len(posts)], form)
				if err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
			}
		}(i)
	}
	hits.Wait()
	stopHammer()

	logout := url.Values{}
	logout.Set("token", "tok")
	logout.Set("confirmed", "1")
	resp, err := client.PostForm(base+"/logout", logout)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	cancel()
	done := make(chan struct{})
	go func() {
		loops.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("loop shutdown deadlock")
	}
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("http shutdown deadlock")
	}
}
