package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"dreame-dashboard/internal/config"
	"dreame-dashboard/internal/logx"
	"dreame-dashboard/internal/supervisor"
)

var version = "dev"

func main() {
	cmd := "run"
	args := []string{}
	if len(os.Args) > 1 {
		cmd = os.Args[1]
		args = os.Args[2:]
	}
	if cmd == "version" {
		fmt.Printf("dmd %s\n%s\n", version, runtime.Version())
		return
	}
	cfg, err := config.Load(os.Environ())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	logx.SetLevel(cfg.LogLevel)
	if err := dispatch(cfg, cmd, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func dispatch(cfg config.Config, cmd string, args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	switch cmd {
	case "run":
		return supervisor.Run(ctx, cfg)
	case "healthcheck":
		st, err := supervisor.ReadStatus(filepath.Join(cfg.RunDir, "status.json"))
		if err != nil {
			return err
		}
		return supervisor.Evaluate(st, time.Now())
	case "check-refresh":
		return supervisor.CheckRefresh(ctx, cfg)
	case "verify-map":
		sock := filepath.Join(cfg.RunDir, "ctl.sock")
		msg, err := supervisor.CallCtl(sock, "verify-map")
		if err == nil {
			fmt.Println(msg)
			return nil
		}
		if !errors.Is(err, supervisor.ErrCtlUnavailable) {
			return err
		}
		return supervisor.VerifyStandalone(ctx, cfg)
	case "probe-writes":
		if !hasFlag(args, "--yes-actuate") {
			return errors.New("probe-writes actuates the purifier and requires --yes-actuate")
		}
		return supervisor.ProbeStandalone(ctx, cfg)
	case "poll-now":
		msg, err := supervisor.CallCtl(filepath.Join(cfg.RunDir, "ctl.sock"), "poll-now")
		if err != nil {
			return err
		}
		fmt.Println(msg)
		return nil
	default:
		return fmt.Errorf("unknown command %s", cmd)
	}
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}
