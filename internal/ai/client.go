package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultEndpoint = "https://api.openai.com/v1/chat/completions"

// DailyPromptVersion triggers one automatic regeneration when the daily summary format changes.
const DailyPromptVersion = 5

var defaultHTTP = &http.Client{Timeout: 60 * time.Second}

// Point is one downsampled sensor reading sent to the analyzer.
type Point struct {
	When string  `json:"when"`
	V    float64 `json:"v"`
	Min  float64 `json:"min,omitempty"`
	Max  float64 `json:"max,omitempty"`
}

// MetricInput summarizes one sensor metric over the chosen span.
type MetricInput struct {
	ID      string  `json:"id"`
	Label   string  `json:"label"`
	Unit    string  `json:"unit,omitempty"`
	Current string  `json:"current"`
	Min     string  `json:"min"`
	Avg     string  `json:"avg"`
	Peak    string  `json:"peak"`
	Count   int     `json:"count"`
	Points  []Point `json:"points,omitempty"`
}

// AnalyzeInput is the sensor and purifier context sent to OpenAI.
type AnalyzeInput struct {
	Scope       string        `json:"scope"` // "daily", "metric", or "general"
	Metric      string        `json:"metric,omitempty"`
	Span        string        `json:"span"`
	Granularity string        `json:"granularity,omitempty"`
	CurrentTime string        `json:"current_time,omitempty"`
	Trigger     string        `json:"trigger,omitempty"`
	DeviceName  string        `json:"device_name"`
	PowerState  string        `json:"power_state"`
	Mode        string        `json:"mode"`
	Fan         string        `json:"fan"`
	HEPA        string        `json:"hepa"`
	TempUnit    string        `json:"temp_unit"`
	Metrics     []MetricInput `json:"metrics"`
	GeneratedAt time.Time     `json:"-"`
}

// MetricNote is one per-metric summary line inside a general report.
type MetricNote struct {
	ID         string `json:"id"`
	Label      string `json:"label,omitempty"`
	Status     string `json:"status,omitempty"`
	Assessment string `json:"assessment"`
}

// Report is the structured ChatGPT analysis rendered in the dashboard.
type Report struct {
	Scope           string       `json:"scope"`
	Metric          string       `json:"metric,omitempty"`
	Span            string       `json:"span"`
	Version         int          `json:"version,omitempty"`
	Model           string       `json:"model"`
	GeneratedAt     string       `json:"generated_at"`
	Status          string       `json:"status"` // "good", "attention", "concern"
	Headline        string       `json:"headline"`
	Summary         string       `json:"summary"`
	Findings        []string     `json:"findings,omitempty"`
	Metrics         []MetricNote `json:"metrics,omitempty"`
	Recommendations []string     `json:"recommendations,omitempty"`
}

// Client talks to the OpenAI Chat Completions API.
type Client struct {
	Endpoint string
	HTTP     *http.Client
}

// Analyze requests a concise indoor air-quality report from OpenAI.
func (c *Client) Analyze(ctx context.Context, apiKey, model string, in AnalyzeInput) (Report, error) {
	if strings.TrimSpace(apiKey) == "" {
		return Report{}, ErrNotConfigured
	}
	model = NormalizeModel(model)
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = defaultHTTP
	}

	payloadJSON, err := json.Marshal(in)
	if err != nil {
		return Report{}, err
	}

	systemPrompt := buildSystemPrompt(in.Scope)
	userPrompt := "Analyze these indoor air purifier telemetry readings and respond strictly as JSON:\n" + string(payloadJSON)

	reqBody := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"response_format": map[string]string{"type": "json_object"},
	}
	if strings.HasPrefix(model, "o") {
		if in.Scope == "daily" {
			reqBody["max_completion_tokens"] = 500
		} else {
			reqBody["max_completion_tokens"] = 900
		}
	} else {
		if in.Scope == "daily" {
			reqBody["temperature"] = 0.35
			reqBody["max_tokens"] = 260
		} else {
			reqBody["temperature"] = 0.2
			reqBody["max_tokens"] = 700
		}
	}

	rawReq, err := json.Marshal(reqBody)
	if err != nil {
		return Report{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(rawReq))
	if err != nil {
		return Report{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))

	resp, err := httpClient.Do(req)
	if err != nil {
		return Report{}, fmt.Errorf("OpenAI request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return Report{}, fmt.Errorf("OpenAI response read failed: %w", err)
	}
	if resp.StatusCode >= 300 {
		return Report{}, fmt.Errorf("OpenAI HTTP %d: %s", resp.StatusCode, extractAPIError(body, resp.StatusCode))
	}

	var envelope struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return Report{}, errors.New("OpenAI returned invalid JSON")
	}
	if len(envelope.Choices) == 0 || strings.TrimSpace(envelope.Choices[0].Message.Content) == "" {
		return Report{}, errors.New("OpenAI returned an empty analysis")
	}

	now := in.GeneratedAt
	if now.IsZero() {
		now = time.Now()
	}
	rep := parseModelContent(envelope.Choices[0].Message.Content, in)
	rep.Scope = in.Scope
	rep.Metric = in.Metric
	rep.Span = in.Span
	if in.Scope == "daily" {
		rep.Version = DailyPromptVersion
	}
	rep.Model = model
	if envelope.Model != "" {
		rep.Model = envelope.Model
	}
	rep.GeneratedAt = now.UTC().Format(time.RFC3339)
	return rep, nil
}

const thresholdGuide = "Use these exact indoor thresholds: " +
	"PM2.5 (<9.1 Good, 9.1-35.5 Moderate, >=35.5 High µg/m³), " +
	"PM10 (<20 Good, 20-50 Moderate, >=50 High µg/m³), " +
	"PM1 (<10 Good, 10-25 Moderate, >=25 High µg/m³), " +
	"Air quality index (<30% Low/Good, 30-60% Moderate, >=60% High; lower % is cleaner), " +
	"Temperature (19-25 °C / 66-77 °F Comfort; 16-19 or 25-28 °C Cool/Warm; <16 or >=28 °C Extreme), " +
	"Humidity (35-55% Comfort; 25-35% or 55-65% Dry/Humid; <25% or >=65% Extreme), " +
	"HCHO (<0.05 Good, 0.05-0.08 Moderate, >=0.08 High mg/m³, WHO 0.10 limit), " +
	"TVOC (<200 Good, 200-500 Moderate, >=500 High µg/m³). "

func buildSystemPrompt(scope string) string {
	switch scope {
	case "daily":
		return "You are describing home air quality in plain, everyday human English based on Dreame AirPursue PM20 live readings and today's hourly (1h) history since midnight. " +
			"When power_state is Standby, state right away that the purifier is in standby; if particle/climate/HCHO current values say 'paused in standby', mention briefly that those sensors are paused while citing active TVOC/Air quality or earlier hours from today instead of treating paused sensors as live. " +
			thresholdGuide +
			"Write 2 to 3 natural sentences (35 to 60 words total) as one clean paragraph, as if calmly telling someone at home how the air feels right now and how today has gone so far. " +
			"If trigger indicates a sudden live change, lead directly with what just shifted right now using plain numbers before mentioning earlier hours. " +
			"Match the current time of day naturally and mention specific times or numbers only where they matter (like a spike, warmth, or clear drop); do not mechanically list all eight sensors or recite threshold limits. " +
			"Do not include bullet points, headings, labels, recommendations, advice, or robotic filler like 'optimal range', 'consistently well below', 'no action needed', or 'overall'. " +
			`Return a JSON object with a single key: {"summary": "..."}.`
	case "general":
		return "You are an indoor air quality analyst reviewing Dreame AirPursue PM20 sensor telemetry. " +
			"Note that when power_state is Standby, particle/climate/HCHO sensors power off while TVOC and Air quality stay active. " +
			thresholdGuide +
			"Keep prose concise, factual, and direct. Return a JSON object with keys: " +
			`"status" ("good"|"attention"|"concern"), "headline" (<=80 chars), "summary" (2-3 crisp sentences), ` +
			`"metrics" (array of {"id","label","status","assessment"} for each metric provided), and "recommendations" (2-4 actionable short bullets).`
	default:
		return "You are an indoor air quality analyst reviewing one metric from a Dreame AirPursue PM20 purifier. " +
			thresholdGuide +
			"Keep prose concise, grounded in the exact numbers and trend points provided. Return a JSON object with keys: " +
			`"status" ("good"|"attention"|"concern"), "headline" (<=80 chars), "summary" (2-3 crisp sentences), ` +
			`"findings" (2-4 short data-backed bullets covering range, spikes, and trend), and "recommendations" (1-3 practical bullets).`
	}
}

func parseModelContent(content string, in AnalyzeInput) Report {
	clean := strings.TrimSpace(content)
	clean = strings.TrimPrefix(clean, "```json")
	clean = strings.TrimPrefix(clean, "```")
	clean = strings.TrimSuffix(clean, "```")
	clean = strings.TrimSpace(clean)

	var parsed struct {
		Status          string       `json:"status"`
		Headline        string       `json:"headline"`
		Summary         string       `json:"summary"`
		Findings        []string     `json:"findings"`
		Metrics         []MetricNote `json:"metrics"`
		Recommendations []string     `json:"recommendations"`
	}
	if err := json.Unmarshal([]byte(clean), &parsed); err != nil {
		return Report{
			Status:   "attention",
			Headline: "Air quality analysis",
			Summary:  clean,
		}
	}
	status := strings.ToLower(strings.TrimSpace(parsed.Status))
	switch status {
	case "good", "attention", "concern":
	default:
		status = "good"
	}
	headline := strings.TrimSpace(parsed.Headline)
	if headline == "" {
		switch in.Scope {
		case "daily":
			headline = "Hourly daily air quality summary"
		case "general":
			headline = "Overall air quality report"
		default:
			headline = "Metric analysis"
		}
	}
	return Report{
		Status:          status,
		Headline:        headline,
		Summary:         strings.TrimSpace(parsed.Summary),
		Findings:        trimStrings(parsed.Findings),
		Metrics:         cleanMetricNotes(parsed.Metrics),
		Recommendations: trimStrings(parsed.Recommendations),
	}
}

func trimStrings(in []string) []string {
	var out []string
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func cleanMetricNotes(in []MetricNote) []MetricNote {
	var out []MetricNote
	for _, m := range in {
		m.ID = strings.TrimSpace(m.ID)
		m.Label = strings.TrimSpace(m.Label)
		m.Assessment = strings.TrimSpace(m.Assessment)
		if m.Assessment == "" {
			continue
		}
		st := strings.ToLower(strings.TrimSpace(m.Status))
		switch st {
		case "good", "attention", "concern":
			m.Status = st
		default:
			m.Status = "good"
		}
		out = append(out, m)
	}
	return out
}

func extractAPIError(body []byte, code int) string {
	var env struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &env) == nil && strings.TrimSpace(env.Error.Message) != "" {
		msg := strings.TrimSpace(env.Error.Message)
		if len(msg) > 180 {
			msg = msg[:180]
		}
		return msg
	}
	return http.StatusText(code)
}
