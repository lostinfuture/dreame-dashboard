package supervisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"dreame-dashboard/internal/config"
	"dreame-dashboard/internal/dreame"
	"dreame-dashboard/internal/flock"
	"dreame-dashboard/internal/store"
)

// CheckRefresh logs in, rotates the refresh token once, and lists devices.
// It prints whether the refresh token changed. It does not print token values.
func CheckRefresh(ctx context.Context, cfg config.Config) error {
	return withLock(cfg, func() error {
		auth, client, err := session(cfg)
		if err != nil {
			return err
		}
		if _, _, err := auth.Authorize(ctx); err != nil {
			return err
		}
		before := auth.Current().RefreshToken
		if err := auth.RefreshOnly(ctx); err != nil {
			return err
		}
		after := auth.Current().RefreshToken
		devs, err := client.ListDevices(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("uid=%s\nrefresh_rotated=%t\ndevices=%d\n", auth.Current().UID, before != "" && after != "" && before != after, len(devs))
		return nil
	})
}

// VerifyStandalone runs a read-only property sweep under the data lock.
func VerifyStandalone(ctx context.Context, cfg config.Config) error {
	return withLock(cfg, func() error {
		auth, client, err := session(cfg)
		if err != nil {
			return err
		}
		st, err := store.Open(cfg.DataDir)
		if err != nil {
			return err
		}
		app := NewApp(cfg, st, auth, client)
		if _, _, err := auth.Authorize(ctx); err != nil {
			return err
		}
		text, err := app.verifyLocked(ctx)
		if text != "" {
			fmt.Print(text)
			if text[len(text)-1] != '\n' {
				fmt.Println()
			}
		}
		return err
	})
}

// ProbeStandalone actuates the purifier, restores it, and records write bounds.
func ProbeStandalone(ctx context.Context, cfg config.Config) error {
	return withLock(cfg, func() error {
		auth, client, err := session(cfg)
		if err != nil {
			return err
		}
		if _, _, err := auth.Authorize(ctx); err != nil {
			return err
		}
		devs, err := client.ListDevices(ctx)
		if err != nil {
			return err
		}
		dev, err := pickDevice(devs, cfg.DID)
		if err != nil {
			return err
		}
		if dev.BindDomain != "" {
			client.SetPrefix(dev.BindDomain)
		}
		base, _ := dreame.LoadProfile(cfg.DataDir)
		if base.DID == "" {
			base.DID = dev.DID
		}
		res, err := dreame.ProbeWrites(ctx, client, dev.DID, base, func(d time.Duration) {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
			case <-timer.C:
			}
		})
		if res.Log != "" {
			fmt.Println(res.Log)
		}
		if err != nil {
			return err
		}
		return dreame.SaveProfile(cfg.DataDir, res.Profile)
	})
}

func session(cfg config.Config) (*dreame.Manager, *dreame.Client, error) {
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
	return auth, client, nil
}

func withLock(cfg config.Config, fn func() error) error {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	lock, err := flock.Acquire(filepath.Join(cfg.DataDir, ".lock"))
	if err != nil {
		return err
	}
	defer lock.Release()
	return fn()
}
