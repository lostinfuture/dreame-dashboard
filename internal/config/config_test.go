package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TZ != "UTC" || cfg.Location.String() != "UTC" {
		t.Fatal(cfg.TZ)
	}
	if cfg.Listen != "0.0.0.0:9094" || cfg.DataDir != "/data" || cfg.RunDir != "/run/dmd" {
		t.Fatalf("%+v", cfg)
	}
	if cfg.Region != "us" || cfg.Country != "US" || cfg.Tenant != "000000" {
		t.Fatalf("%+v", cfg)
	}
	if cfg.PollInterval != 30*time.Second || cfg.MQTTPollInterval != 5*time.Minute {
		t.Fatal(cfg.PollInterval, cfg.MQTTPollInterval)
	}
	if cfg.BatchSize != 10 || cfg.RetentionDays != 90 || cfg.TempUnit != "C" || cfg.LogLevel != "info" {
		t.Fatalf("%+v", cfg)
	}
	if cfg.ReadOnly || cfg.EnableMQTT || cfg.SendRLC || cfg.MQTTInsecureSkipVerify || cfg.HasPassword() {
		t.Fatal("flags")
	}
}

func TestBounds(t *testing.T) {
	ok := []string{
		"DMD_POLL_INTERVAL=20s", "DMD_POLL_INTERVAL=300s",
		"DMD_MQTT_POLL_INTERVAL=1m", "DMD_MQTT_POLL_INTERVAL=30m",
		"DMD_BATCH_SIZE=1", "DMD_BATCH_SIZE=10",
		"DMD_RETENTION_DAYS=7", "DMD_RETENTION_DAYS=365",
		"DMD_REGION=eu", "DMD_REGION=KR",
		"DMD_LISTEN=:9094",
	}
	for _, e := range ok {
		cfg, err := Load([]string{e})
		if err != nil {
			t.Fatalf("%s: %v", e, err)
		}
		if strings.HasPrefix(e, "DMD_REGION=eu") && cfg.Country != "DE" {
			t.Fatal(cfg.Country)
		}
		if strings.HasPrefix(e, "DMD_REGION=KR") && (cfg.Region != "kr" || cfg.Country != "KR") {
			t.Fatalf("%s %s", cfg.Region, cfg.Country)
		}
		if e == "DMD_LISTEN=:9094" && cfg.Listen != ":9094" {
			t.Fatal(cfg.Listen)
		}
	}
	bad := []string{
		"DMD_POLL_INTERVAL=19s", "DMD_POLL_INTERVAL=301s", "DMD_POLL_INTERVAL=nope",
		"DMD_MQTT_POLL_INTERVAL=59s", "DMD_MQTT_POLL_INTERVAL=31m",
		"DMD_BATCH_SIZE=0", "DMD_BATCH_SIZE=11",
		"DMD_RETENTION_DAYS=6", "DMD_RETENTION_DAYS=366",
		"DMD_REGION=jp", "DMD_COUNTRY=USA", "DMD_TENANT=12",
		"DMD_LISTEN=9094", "DMD_TEMP_UNIT=K", "DMD_LOG_LEVEL=trace",
		"DMD_READ_ONLY=maybe", "TZ=Not/AZone",
	}
	for _, e := range bad {
		if _, err := Load([]string{e}); err == nil {
			t.Fatalf("accepted %s", e)
		}
	}
}

func TestPasswordMutualExclusion(t *testing.T) {
	dir := t.TempDir()
	pw := filepath.Join(dir, "dreame.pw")
	if err := os.WriteFile(pw, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load([]string{"DMD_PASSWORD=a", "DMD_PASSWORD_FILE=" + pw, "DMD_EMAIL=a@b.c"})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatal(err)
	}
	cfg, err := Load([]string{"DMD_PASSWORD_FILE=" + pw, "DMD_EMAIL=a@b.c"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Password != "s3cret" || !cfg.HasPassword() {
		t.Fatal(cfg.Password)
	}
	if _, err := Load([]string{"DMD_PASSWORD=s3cret"}); err == nil {
		t.Fatal("email required")
	}
	cfg, err = Load([]string{"DMD_PASSWORD=s3cret", "DMD_EMAIL=a@b.c"})
	if err != nil || cfg.Password != "s3cret" {
		t.Fatal(err, cfg.Password)
	}
}

func TestBasicPair(t *testing.T) {
	dir := t.TempDir()
	pw := filepath.Join(dir, "basic.pw")
	if err := os.WriteFile(pw, []byte("lan\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load([]string{"DMD_BASIC_USER=op"}); err == nil {
		t.Fatal("pair")
	}
	if _, err := Load([]string{"DMD_BASIC_PASSWORD_FILE=" + pw}); err == nil {
		t.Fatal("pair")
	}
	cfg, err := Load([]string{"DMD_BASIC_USER=op", "DMD_BASIC_PASSWORD_FILE=" + pw})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BasicUser != "op" || cfg.BasicPassword != "lan" {
		t.Fatal(cfg.BasicUser, cfg.BasicPassword)
	}
}

func TestUnknown(t *testing.T) {
	_, err := Load([]string{"DMD_TYPO=1", "DMD_OTHER=2"})
	if err == nil || !strings.Contains(err.Error(), "DMD_OTHER") || !strings.Contains(err.Error(), "DMD_TYPO") {
		t.Fatal(err)
	}
	cfg, err := Load([]string{"UNRELATED=1", "TZ=America/New_York"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Location.String() != "America/New_York" {
		t.Fatal(cfg.Location)
	}
}

func TestCountryOverride(t *testing.T) {
	cfg, err := Load([]string{"DMD_REGION=eu", "DMD_COUNTRY=fr"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Country != "FR" {
		t.Fatal(cfg.Country)
	}
}
