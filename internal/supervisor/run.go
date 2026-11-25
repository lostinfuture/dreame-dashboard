package supervisor

import (
	"context"
	"errors"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"dreame-dashboard/internal/config"
	"dreame-dashboard/internal/dreame"
	"dreame-dashboard/internal/flock"
	"dreame-dashboard/internal/logx"
	"dreame-dashboard/internal/mqtt"
	"dreame-dashboard/internal/store"
	"dreame-dashboard/internal/web"
)

// Run holds the data lock and serves until ctx is cancelled.
func Run(ctx context.Context, cfg config.Config) error {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.RunDir, 0o700); err != nil {
		return err
	}
	lock, err := flock.Acquire(filepath.Join(cfg.DataDir, ".lock"))
	if err != nil {
		return err
	}
	defer lock.Release()
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	auth := dreame.NewManager(filepath.Join(cfg.DataDir, "tokens.json"))
	auth.SetDefaultLocale(cfg.Region, cfg.Country)
	auth.SetDefaultTenant(cfg.Tenant)
	auth.Email = cfg.Email
	auth.Password = cfg.Password
	auth.SendRLC = cfg.SendRLC
	region, country := auth.Locale()
	client := &dreame.Client{
		Region: region, Country: country, Lang: dreame.Lang,
		SendRLC: cfg.SendRLC, BatchSize: cfg.BatchSize, Auth: auth,
	}
	client.OnRequest = func() {
		st.Lock()
		st.NoteCall(time.Now())
		st.Unlock()
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	app := NewApp(cfg, st, auth, client)
	app.Poller.ExternalCount = true
	app.runCtx = rctx
	var mqttClient *mqtt.Client
	if cfg.EnableMQTT {
		mqttClient = &mqtt.Client{
			CAFile: cfg.MQTTCAFile, Insecure: cfg.MQTTInsecureSkipVerify,
			ClientID: mqtt.ClientID(),
		}
		auth.SetOnRefresh(func() {
			app.mqttRefresh.Store(true)
			mqttClient.Close()
		})
	}
	srv := web.NewServer(newToken(), app)
	srv.BasicUser = cfg.BasicUser
	srv.BasicPass = cfg.BasicPassword
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	app.httpOK.Store(true)
	app.writeHealth()
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		app.healthLoop(rctx)
	}()
	go func() {
		defer wg.Done()
		_ = ServeCtl(rctx, filepath.Join(cfg.RunDir, "ctl.sock"), app.ctl)
	}()
	go func() {
		defer wg.Done()
		app.loop(rctx)
	}()
	go func() {
		defer wg.Done()
		app.aiDailyLoop(rctx)
	}()
	if mqttClient != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			app.mqttLoop(rctx, mqttClient)
		}()
	}
	err = srv.Serve(rctx, ln)
	cancel()
	app.CloseHub()
	wg.Wait()
	app.httpOK.Store(false)
	app.writeHealth()
	st.Lock()
	saveErr := st.SaveState()
	st.Unlock()
	if saveErr != nil {
		logx.Warn("supervisor", "state save", "err", saveErr.Error())
	}
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func (a *App) loop(ctx context.Context) {
	now := time.Now().UTC()
	_ = a.Store.Backfill(now)
	_ = a.Store.Retain(now, a.Cfg.RetentionDays)
	hour := now.Truncate(time.Hour)
	for ctx.Err() == nil {
		if a.signedOut.Load() || a.Auth.State() == "auth_required" {
			a.booted.Store(true)
			a.wait(ctx, 2*time.Second)
			continue
		}
		if a.Auth.State() != "ready" {
			actx, cancel := context.WithTimeout(ctx, 30*time.Second)
			_, _, err := a.Auth.Authorize(actx)
			cancel()
			if err != nil {
				if errors.Is(err, dreame.ErrAuthRequired) {
					a.booted.Store(true)
				}
				a.wait(ctx, 5*time.Second)
				continue
			}
		}
		a.busy.Lock()
		a.profMu.RLock()
		empty := a.prof == nil || a.prof.DID == "" || len(a.prof.Properties) == 0
		a.profMu.RUnlock()
		var err error
		if empty {
			if _, verr := a.verifyLocked(ctx); verr != nil {
				logx.Warn("supervisor", "verify-map", "err", verr.Error())
				a.Poller.fail(time.Now(), verr)
				err = verr
			}
		}
		if err == nil {
			iv := a.pollInterval()
			a.Poller.setInterval(iv)
			a.Worker.setInterval(iv)
			err = a.Poller.PollOnce(ctx, false)
			if err != nil {
				logx.Warn("supervisor", "poll", "err", err.Error())
			}
		}
		a.busy.Unlock()
		a.Store.Lock()
		status := a.Store.State().Status
		a.Store.Unlock()
		if err == nil || status == "online" || status == "degraded" || status == "offline" {
			a.booted.Store(true)
		}
		tick := time.Now().UTC()
		if tick.Truncate(time.Hour).After(hour) {
			hour = tick.Truncate(time.Hour)
			_ = a.Store.Backfill(tick)
			if err := a.Store.Retain(tick, a.Cfg.RetentionDays); err != nil {
				logx.Warn("supervisor", "retain", "err", err.Error())
			}
		}
		a.Store.Lock()
		streak := a.Store.State().Streak
		a.Store.Unlock()
		a.wait(ctx, Jitter(NextWait(a.pollInterval(), streak), rand.Float64()))
	}
}

func (a *App) healthLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.writeHealth()
		}
	}
}

func (a *App) writeHealth() {
	st := "boot"
	if a.Auth != nil {
		st = a.Auth.State()
	}
	now := time.Now()
	a.mu.Lock()
	if st == "backoff" || st == "degraded" {
		if a.trouble.IsZero() {
			a.trouble = now
		}
	} else {
		a.trouble = time.Time{}
	}
	trouble := a.trouble
	a.mu.Unlock()
	_ = WriteStatus(filepath.Join(a.Cfg.RunDir, "status.json"), Status{
		At: now, HTTPOk: a.httpOK.Load(), AuthState: st,
		Unattended: a.Cfg.HasPassword() && !a.signedOut.Load(), AuthTroubleSince: trouble,
	})
}

func (a *App) ctl(verb string) (string, error) {
	ctx := a.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	switch verb {
	case "poll-now":
		err := a.Poll(ctx)
		if err != nil {
			return "", err
		}
		return "ok", nil
	case "verify-map":
		err := a.VerifyMap(ctx)
		a.mu.Lock()
		msg := a.flash
		a.mu.Unlock()
		if msg == "" {
			msg = "ok"
		}
		return msg, err
	default:
		return "", errors.New("unknown")
	}
}
