package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKeyStoreEncryptsAtRestAndMasks(t *testing.T) {
	dir := t.TempDir()
	ks := NewKeyStore(dir, "", "")
	if st := ks.Status(); st.Configured || st.Source != "none" || st.Model != DefaultModel {
		t.Fatalf("initial status: %+v", st)
	}
	if _, _, err := ks.Resolve(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
	const secret = "sk-proj-testkey1234567890abcd"
	if err := ks.Save(secret, "gpt-4.1-mini"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "openai_key.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "testkey123456") {
		t.Fatalf("plaintext key leaked on disk: %s", raw)
	}
	info, err := os.Stat(filepath.Join(dir, "openai_key.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("openai_key.json perm %v err %v", info.Mode().Perm(), err)
	}
	minfo, err := os.Stat(filepath.Join(dir, ".secret.key"))
	if err != nil || minfo.Mode().Perm() != 0o600 {
		t.Fatalf(".secret.key perm %v err %v", minfo.Mode().Perm(), err)
	}
	st := ks.Status()
	if !st.Configured || !st.Saved || st.Source != "saved" || st.Model != "gpt-4.1-mini" || st.Hint != "sk-proj-••••abcd" {
		t.Fatalf("status: %+v", st)
	}
	// Reload in a fresh KeyStore instance.
	ks2 := NewKeyStore(dir, "", "")
	gotKey, gotModel, err := ks2.Resolve()
	if err != nil || gotKey != secret || gotModel != "gpt-4.1-mini" {
		t.Fatalf("resolve key=%q model=%q err=%v", gotKey, gotModel, err)
	}
	// Model update without re-entering API key preserves key.
	if err := ks2.Save("", "gpt-4o"); err != nil {
		t.Fatal(err)
	}
	gotKey, gotModel, err = ks2.Resolve()
	if err != nil || gotKey != secret || gotModel != "gpt-4o" {
		t.Fatalf("after model update key=%q model=%q err=%v", gotKey, gotModel, err)
	}
	// Delete removes stored key.
	if err := ks2.Delete(); err != nil {
		t.Fatal(err)
	}
	if ks2.Status().Configured {
		t.Fatal("still configured after Delete")
	}
}

func TestClientAnalyzeMetricAndGeneral(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = nil
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "gpt-4o-mini-2024-07-18",
			"choices": []any{map[string]any{
				"message": map[string]any{
					"content": `{"status":"good","headline":"PM2.5 is low","summary":"PM2.5 stayed under 3 µg/m³.","findings":["Peak 3 µg/m³"],"recommendations":["Keep AI mode on"]}`,
				},
			}},
		})
	}))
	defer srv.Close()

	c := &Client{Endpoint: srv.URL, HTTP: srv.Client()}
	rep, err := c.Analyze(context.Background(), "sk-test-1234567890abcdef", "gpt-4o-mini", AnalyzeInput{
		Scope:       "metric",
		Metric:      "pm25",
		Span:        "6h",
		DeviceName:  "Air Purifier PM20",
		PowerState:  "Working",
		GeneratedAt: time.Date(2026, 11, 22, 17, 0, 0, 0, time.UTC),
		Metrics:     []MetricInput{{ID: "pm25", Label: "PM2.5", Unit: "µg/m³", Current: "2 µg/m³", Min: "0 µg/m³", Avg: "1.4 µg/m³", Peak: "3 µg/m³", Count: 12}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer sk-test-1234567890abcdef" {
		t.Fatalf("auth header %q", gotAuth)
	}
	if rep.Status != "good" || rep.Headline != "PM2.5 is low" || len(rep.Findings) != 1 || len(rep.Recommendations) != 1 {
		t.Fatalf("report %+v", rep)
	}
	if _, hasTemp := gotBody["temperature"]; !hasTemp {
		t.Fatalf("expected temperature on non-o model: %+v", gotBody)
	}
	// Reasoning model omits temperature and uses max_completion_tokens.
	if _, err := c.Analyze(context.Background(), "sk-test-1234567890abcdef", "o4-mini", AnalyzeInput{Scope: "general", Span: "24h"}); err != nil {
		t.Fatal(err)
	}
	if _, hasTemp := gotBody["temperature"]; hasTemp {
		t.Fatalf("o4-mini should omit temperature: %+v", gotBody)
	}
	if _, hasMax := gotBody["max_completion_tokens"]; !hasMax {
		t.Fatalf("o4-mini should set max_completion_tokens: %+v", gotBody)
	}
	// Daily hourly summary scope stamps DailyPromptVersion and requests a compact paragraph.
	repDaily, err := c.Analyze(context.Background(), "sk-test-1234567890abcdef", "gpt-4.1", AnalyzeInput{Scope: "daily", Span: "24h", Granularity: "1h"})
	if err != nil || repDaily.Scope != "daily" || repDaily.Span != "24h" || repDaily.Version != DailyPromptVersion {
		t.Fatalf("daily rep=%+v err=%v", repDaily, err)
	}
	if maxTok, ok := gotBody["max_tokens"].(float64); !ok || maxTok < 200 || maxTok > 400 {
		t.Fatalf("expected compact max_tokens for daily summary: %+v", gotBody)
	}
}
