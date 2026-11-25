// Package config loads the dmd environment contract.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Config is the process configuration from the environment.
type Config struct {
	TZ                     string
	Location               *time.Location
	Listen                 string
	DataDir                string
	RunDir                 string
	Region                 string
	Country                string
	Tenant                 string
	Email                  string
	Password               string
	PasswordFile           string
	DID                    string
	PollInterval           time.Duration
	MQTTPollInterval       time.Duration
	BatchSize              int
	ReadOnly               bool
	EnableMQTT             bool
	MQTTCAFile             string
	MQTTInsecureSkipVerify bool
	SendRLC                bool
	RetentionDays          int
	TempUnit               string
	BasicUser              string
	BasicPassword          string
	BasicPasswordFile      string
	OpenAIKeyFile          string
	OpenAIKey              string
	OpenAIModel            string
	LogLevel               string
}

// HasPassword reports whether an unattended Dreame password is configured.
func (c Config) HasPassword() bool { return c.Password != "" }

var known = map[string]bool{
	"DMD_LISTEN": true, "DMD_DATA_DIR": true, "DMD_RUN_DIR": true,
	"DMD_REGION": true, "DMD_COUNTRY": true, "DMD_TENANT": true,
	"DMD_EMAIL": true, "DMD_PASSWORD_FILE": true, "DMD_PASSWORD": true,
	"DMD_DID": true, "DMD_POLL_INTERVAL": true, "DMD_MQTT_POLL_INTERVAL": true,
	"DMD_BATCH_SIZE": true, "DMD_READ_ONLY": true, "DMD_ENABLE_MQTT": true,
	"DMD_MQTT_CA_FILE": true, "DMD_MQTT_INSECURE_SKIP_VERIFY": true,
	"DMD_SEND_RLC": true, "DMD_RETENTION_DAYS": true, "DMD_TEMP_UNIT": true,
	"DMD_BASIC_USER": true, "DMD_BASIC_PASSWORD_FILE": true,
	"DMD_OPENAI_API_KEY_FILE": true, "DMD_OPENAI_MODEL": true,
	"DMD_LOG_LEVEL": true,
}

var countries = map[string]string{
	"us": "US", "eu": "DE", "cn": "CN", "sg": "SG", "kr": "KR", "ru": "RU",
}

// Load reads the dashboard environment. Unknown DMD_* names fail the process.
func Load(environ []string) (Config, error) {
	m, err := parseEnv(environ)
	if err != nil {
		return Config{}, err
	}
	if err := unknown(m); err != nil {
		return Config{}, err
	}
	cfg := Config{
		TZ:               "UTC",
		Listen:           "0.0.0.0:9094",
		DataDir:          "/data",
		RunDir:           "/run/dmd",
		Region:           "us",
		Tenant:           "000000",
		PollInterval:     30 * time.Second,
		MQTTPollInterval: 5 * time.Minute,
		BatchSize:        10,
		RetentionDays:    90,
		TempUnit:         "C",
		LogLevel:         "info",
	}
	if v, ok := m["TZ"]; ok && v != "" {
		cfg.TZ = v
	}
	loc, err := time.LoadLocation(cfg.TZ)
	if err != nil {
		return Config{}, errors.New("TZ")
	}
	cfg.Location = loc
	if v, ok := m["DMD_LISTEN"]; ok {
		_, port, err := net.SplitHostPort(v)
		n, nerr := strconv.Atoi(port)
		if err != nil || nerr != nil || n < 1 || n > 65535 {
			return Config{}, errors.New("DMD_LISTEN")
		}
		cfg.Listen = v
	}
	if v, ok := m["DMD_DATA_DIR"]; ok {
		if v == "" || strings.Contains(v, "\x00") {
			return Config{}, errors.New("DMD_DATA_DIR")
		}
		cfg.DataDir = v
	}
	if v, ok := m["DMD_RUN_DIR"]; ok {
		if v == "" {
			return Config{}, errors.New("DMD_RUN_DIR")
		}
		cfg.RunDir = v
	}
	if v, ok := m["DMD_REGION"]; ok {
		v = strings.ToLower(v)
		if _, ok := countries[v]; !ok {
			return Config{}, errors.New("DMD_REGION")
		}
		cfg.Region = v
	}
	cfg.Country = countries[cfg.Region]
	if v, ok := m["DMD_COUNTRY"]; ok {
		if len(v) != 2 || !isAlpha(v) {
			return Config{}, errors.New("DMD_COUNTRY")
		}
		cfg.Country = strings.ToUpper(v)
	}
	if v, ok := m["DMD_TENANT"]; ok {
		if len(v) != 6 || !isDigits(v) {
			return Config{}, errors.New("DMD_TENANT")
		}
		cfg.Tenant = v
	}
	if v, ok := m["DMD_EMAIL"]; ok {
		cfg.Email = v
	}
	if v, ok := m["DMD_DID"]; ok {
		cfg.DID = v
	}
	if _, okPass := m["DMD_PASSWORD"]; okPass {
		if _, okFile := m["DMD_PASSWORD_FILE"]; okFile {
			return Config{}, errors.New("DMD_PASSWORD and DMD_PASSWORD_FILE are mutually exclusive")
		}
	}
	if v, ok := m["DMD_PASSWORD_FILE"]; ok && v != "" {
		pw, err := readSecret(v)
		if err != nil {
			return Config{}, fmt.Errorf("DMD_PASSWORD_FILE: %w", err)
		}
		cfg.PasswordFile = v
		cfg.Password = pw
	}
	if v, ok := m["DMD_PASSWORD"]; ok && v != "" {
		cfg.Password = v
	}
	if cfg.Password != "" && strings.TrimSpace(cfg.Email) == "" {
		return Config{}, errors.New("DMD_EMAIL is required when a password is set")
	}
	if v, ok := m["DMD_POLL_INTERVAL"]; ok {
		d, err := durationBetween(v, 20*time.Second, 300*time.Second)
		if err != nil {
			return Config{}, errors.New("DMD_POLL_INTERVAL")
		}
		cfg.PollInterval = d
	}
	if v, ok := m["DMD_MQTT_POLL_INTERVAL"]; ok {
		d, err := durationBetween(v, time.Minute, 30*time.Minute)
		if err != nil {
			return Config{}, errors.New("DMD_MQTT_POLL_INTERVAL")
		}
		cfg.MQTTPollInterval = d
	}
	if v, ok := m["DMD_BATCH_SIZE"]; ok {
		n, err := intBetween(v, 1, 10)
		if err != nil {
			return Config{}, errors.New("DMD_BATCH_SIZE")
		}
		cfg.BatchSize = n
	}
	if v, ok := m["DMD_READ_ONLY"]; ok {
		b, err := parseBool(v)
		if err != nil {
			return Config{}, errors.New("DMD_READ_ONLY")
		}
		cfg.ReadOnly = b
	}
	if v, ok := m["DMD_ENABLE_MQTT"]; ok {
		b, err := parseBool(v)
		if err != nil {
			return Config{}, errors.New("DMD_ENABLE_MQTT")
		}
		cfg.EnableMQTT = b
	}
	if v, ok := m["DMD_MQTT_CA_FILE"]; ok {
		cfg.MQTTCAFile = v
	}
	if v, ok := m["DMD_MQTT_INSECURE_SKIP_VERIFY"]; ok {
		b, err := parseBool(v)
		if err != nil {
			return Config{}, errors.New("DMD_MQTT_INSECURE_SKIP_VERIFY")
		}
		cfg.MQTTInsecureSkipVerify = b
	}
	if v, ok := m["DMD_SEND_RLC"]; ok {
		b, err := parseBool(v)
		if err != nil {
			return Config{}, errors.New("DMD_SEND_RLC")
		}
		cfg.SendRLC = b
	}
	if v, ok := m["DMD_RETENTION_DAYS"]; ok {
		n, err := intBetween(v, 7, 365)
		if err != nil {
			return Config{}, errors.New("DMD_RETENTION_DAYS")
		}
		cfg.RetentionDays = n
	}
	if v, ok := m["DMD_TEMP_UNIT"]; ok {
		u := strings.ToUpper(v)
		if u != "C" && u != "F" {
			return Config{}, errors.New("DMD_TEMP_UNIT")
		}
		cfg.TempUnit = u
	}
	user, userSet := m["DMD_BASIC_USER"]
	passFile, passSet := m["DMD_BASIC_PASSWORD_FILE"]
	if userSet != passSet || (userSet && (user == "" || passFile == "")) {
		return Config{}, errors.New("DMD_BASIC_USER and DMD_BASIC_PASSWORD_FILE must both be set")
	}
	if userSet {
		pw, err := readSecret(passFile)
		if err != nil {
			return Config{}, fmt.Errorf("DMD_BASIC_PASSWORD_FILE: %w", err)
		}
		cfg.BasicUser = user
		cfg.BasicPasswordFile = passFile
		cfg.BasicPassword = pw
	}
	if v, ok := m["DMD_OPENAI_API_KEY_FILE"]; ok && v != "" {
		key, err := readSecret(v)
		if err != nil {
			return Config{}, fmt.Errorf("DMD_OPENAI_API_KEY_FILE: %w", err)
		}
		cfg.OpenAIKeyFile = v
		cfg.OpenAIKey = key
	}
	if v, ok := m["DMD_OPENAI_MODEL"]; ok && v != "" {
		cfg.OpenAIModel = v
	}
	if v, ok := m["DMD_LOG_LEVEL"]; ok {
		if v != "info" && v != "debug" {
			return Config{}, errors.New("DMD_LOG_LEVEL")
		}
		cfg.LogLevel = v
	}
	return cfg, nil
}

func parseEnv(environ []string) (map[string]string, error) {
	m := map[string]string{}
	for _, e := range environ {
		k, v, ok := strings.Cut(e, "=")
		if !ok || k == "" {
			continue
		}
		m[k] = v
	}
	return m, nil
}

func unknown(m map[string]string) error {
	var bad []string
	for k := range m {
		if strings.HasPrefix(k, "DMD_") && !known[k] {
			bad = append(bad, k)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return fmt.Errorf("unknown environment variable %s", strings.Join(bad, ", "))
}

func readSecret(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := strings.TrimRight(string(b), "\r\n")
	if s == "" {
		return "", errors.New("empty")
	}
	return s, nil
}

func durationBetween(v string, min, max time.Duration) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	if err != nil || d < min || d > max {
		return 0, errors.New("range")
	}
	return d, nil
}

func intBetween(v string, min, max int) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		return 0, errors.New("range")
	}
	return n, nil
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, errors.New("bool")
	}
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func isAlpha(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return s != ""
}
