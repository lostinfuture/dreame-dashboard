package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dreame-dashboard/internal/ai"
)

//go:embed layout.html overview.html device.html activity.html gate.html page.css chart.js app.js
var assets embed.FS

// Sample is one history point.
type Sample struct {
	TS    time.Time
	V     float64
	Min   float64
	Max   float64
	Range bool
	Gap   bool
}

// Series is one chart line.
type Series struct {
	Name   string
	Unit   string
	Class  string
	Fill   bool
	Points []Sample
}

// ChartGroup is one metric panel's raw series data.
type ChartGroup struct {
	ID      string
	Title   string
	Overlay bool
	Bands   *Bands
	Series  []Series
}

// ChartCard is one rendered chart panel on the overview page.
type ChartCard struct {
	ID          string
	Title       string
	Meaning     string
	NoTranslate bool
	Overlay     bool
	Compact     bool
	Zone        string
	AriaLabel   string
	Tracks      []Track
	XTicks      []string
	Stats       []Stat
	Readouts    []Readout
	EmptyChart  bool
	EmptyText   string
	ChartJSON   template.HTML
}

// Act is one activity row before time formatting.
type Act struct {
	TS     time.Time
	Kind   string
	Target string
	Detail string
	Result string
	MS     int64
	Code   int
}

// PropRow is one device-map row.
type PropRow struct {
	Key   string
	ID    string
	Value string
	Age   string
	State string
}

// Snapshot is the data the supervisor hands the renderer.
type Snapshot struct {
	Now                       time.Time
	Loc                       *time.Location
	Token                     string
	Phase                     string
	Page                      string
	Query                     url.Values
	LoginError                string
	Email                     string
	Region                    string
	Name                      string
	Status                    string
	StatusAt                  time.Time
	MQTT                      string
	ReadOnly                  bool
	ReadOnlyLocked            bool
	ReadOnlySource            string
	WritesOK                  bool
	WriteReason               string
	Power                     int
	PowerKnown                bool
	PowerActionOK             bool
	Mode                      int
	ModeKnown                 bool
	Fan                       int
	FanKnown                  bool
	FanMin                    int
	FanMax                    int
	FanAuto                   bool
	FanForcesCustom           bool
	ModeVals                  []int
	PM20Controls              bool
	Heater                    int
	HeaterKnown               bool
	HeaterMin                 int
	HeaterMax                 int
	Oscillation               int
	OscillationKnown          bool
	OscillationVals           []int
	ContinuousMonitoring      int
	ContinuousMonitoringKnown bool
	ContinuousMonitoringOK    bool
	NearbyPowerOn             int
	NearbyPowerOnKnown        bool
	NearbyPowerOnOK           bool
	FollowMotion              int
	FollowMotionKnown         bool
	FollowMotionOK            bool
	Light                     int
	LightKnown                bool
	LightKind                 string
	LightVals                 []int
	Volume                    int
	VolumeKnown               bool
	VolumeVals                []int
	Tone                      int
	ToneKnown                 bool
	ToneVals                  []int
	AQ                        float64
	AQKnown                   bool
	AQL                       int
	AQLKnown                  bool
	AQLMap                    map[string]string
	PM25                      float64
	PM25Known                 bool
	PM10                      float64
	PM10Known                 bool
	PM1                       float64
	PM1Known                  bool
	TempC                     float64
	TempKnown                 bool
	TempUnit                  string
	TempSource                string
	RH                        float64
	RHKnown                   bool
	HCHO                      float64
	HCHOKnown                 bool
	TVOC                      float64
	TVOCKnown                 bool
	HEPA                      float64
	HEPAKnown                 bool
	HEPADays                  float64
	HEPADaysKnown             bool
	Carbon                    float64
	CarbonKnown               bool
	CarbonDays                float64
	CarbonDaysKnown           bool
	FilterHours               float64
	FilterHoursKnown          bool
	Metric                    string
	Span                      string
	Series                    []Series
	Charts                    []ChartGroup
	Props                     []PropRow
	Verified                  string
	AQEstimate                bool
	Identity                  [][2]string
	Network                   [][2]string
	OTAState                  string
	OTAProgress               string
	OTAWhen                   string
	OTAVersion                string
	OTANewVersion             string
	OTAHasUpdate              bool
	OTATarget                 string
	OTADescription            string
	OTAChecked                string
	PollSeconds               int
	PollSource                string
	HasOverrides              bool
	OpenAIConfigured          bool
	OpenAISaved               bool
	OpenAIEnvLocked           bool
	OpenAIHint                string
	OpenAIModel               string
	OpenAISource              string
	DailyAI                   *ai.Report
	DailyAIError              string
	DailyAIBusy               bool
	DailyAINext               string
	Rows                      []Act
	Kind                      string
	Days                      int
	Banner                    string
	Flash                     string
	FlashInfo                 bool
	Pruned                    map[string]bool
	Announce                  string
}

// Pair is a label and value.
type Pair struct {
	Label string
	Value template.HTML
	Mono  bool
}

// Opt is one segmented choice.
type Opt struct {
	Label     string
	Value     string
	Pressed   bool
	Stage     string
	Old       string
	Confirm   string
	DataLabel string
}

// Group is one control row.
type Group struct {
	Label        string
	Opts         []Opt
	Note         string
	Fan          bool
	FanText      string
	Heater       bool
	HeaterText   string
	HeaterActive bool
	HeaterUnit   string
	HeaterOff    string
	Dec          string
	Inc          string
	DecOff       bool
	IncOff       bool
	Cur          int
	Min          int
	Max          int
	Forces       bool
	ModeName     string
	Off          bool
}

// Confirm is the second step of a write.
type Confirm struct {
	Text     string
	Action   string
	SIID     int
	PIID     int
	Value    string
	Expected string
	Verb     string
}

// Track is one SVG plot.
type Track struct {
	SVG    template.HTML
	YTicks []string
	Half   bool
	Label  string
}

// Readout is one live chart value.
type Readout struct {
	Label       string
	Value       string
	Zone        string
	NoTranslate bool
}

// Stat is one summary figure.
type Stat struct {
	Label string
	Value string
}

// Row is a formatted activity line.
type Row struct {
	When   string
	Kind   string
	Target string
	Detail string
	Result string
	Filter string
}

// Page is the template view model.
type Page struct {
	Title            string
	Body             string
	Gate             string
	Token            string
	Active           string
	Age              string
	Dot              string
	ReadOnly         bool
	Announce         string
	Banner           string
	Flash            string
	FlashInfo        bool
	LoginError       string
	Region           string
	Email            string
	H1               string
	Lead             string
	Pill             string
	PillClass        string
	Sentence         string
	Sensors          []Pair
	HEPA             int
	HEPAKnown        bool
	HEPAClass        string
	HEPAText         string
	HEPALabel        string
	ShowCarbon       bool
	Carbon           int
	CarbonKnown      bool
	CarbonClass      string
	CarbonText       string
	CarbonLabel      string
	PowerLabel       string
	PowerStage       string
	PowerOld         string
	PowerText        string
	PowerVerb        string
	PowerOff         bool
	PowerActionOK    bool
	PowerState       string
	PowerStateClass  string
	Standby          bool
	Groups           []Group
	Confirm          *Confirm
	Metric           string
	Span             string
	Charts           []ChartCard
	Tracks           []Track
	XTicks           []string
	Stats            []Stat
	Readouts         []Readout
	EmptyChart       bool
	ChartJSON        template.HTML
	Identity         []Pair
	Network          []Pair
	Props            []PropRow
	Verified         string
	OTAState         string
	OTAClass         string
	OTAProgress      string
	OTAWhen          string
	OTAVersion       string
	OTANewVersion    string
	OTAHasUpdate     bool
	OTATarget        string
	OTADescription   string
	OTAChecked       string
	OTAPill          string
	OTAPillClass     string
	OTACanUpdate     bool
	OTAUpdateConfirm bool
	Poll             int
	PollSource       string
	TempUnit         string
	TempSource       string
	ReadOnlyOn       bool
	ReadOnlySource   string
	ReadOnlyLocked   bool
	HasOverrides     bool
	OpenAIConfigured bool
	OpenAISaved      bool
	OpenAIEnvLocked  bool
	OpenAIHint       string
	OpenAIModel      string
	OpenAISource     string
	OpenAIModels     []string
	DailyAI          *ai.Report
	DailyAIText      string
	DailyAIClass     string
	DailyAILabel     string
	DailyAIMeta      string
	DailyAIState     string
	WriteReason      string
	AQEstimate       bool
	LogoutConfirm    bool
	Rows             []Row
	Kind             string
	Days             int
	Shown            int
	Total            int
}

var funcs = template.FuncMap{
	"nb": func(v, unit string) template.HTML {
		if v == "" || v == "—" {
			return "—"
		}
		return template.HTML(template.HTMLEscapeString(v) + "\u00a0" + template.HTMLEscapeString(unit))
	},
}

var tmpl = template.Must(template.New("").Funcs(funcs).ParseFS(assets, "layout.html", "overview.html", "device.html", "activity.html", "gate.html"))

// StateJSON is the live overview payload for /api/state.
func StateJSON(s Snapshot) []byte {
	p := Build(s)
	sensors := make([]map[string]string, 0, len(p.Sensors))
	for _, pair := range p.Sensors {
		sensors = append(sensors, map[string]string{"label": pair.Label, "value": plainHTML(pair.Value)})
	}
	var controls []map[string]any
	var fan map[string]any
	var heater map[string]any
	for _, g := range p.Groups {
		for _, o := range g.Opts {
			controls = append(controls, map[string]any{
				"stage": o.Stage, "pressed": o.Pressed, "off": g.Off || o.Pressed,
				"old": o.Old, "confirm": o.Confirm,
			})
		}
		if g.Fan {
			fan = map[string]any{
				"cur": g.Cur, "min": g.Min, "max": g.Max, "text": g.FanText,
				"dec": g.Dec, "inc": g.Inc, "dec_off": g.Off || g.DecOff, "inc_off": g.Off || g.IncOff,
				"forces": g.Forces, "mode_name": g.ModeName, "off": g.Off, "note": g.Note,
			}
		}
		if g.Heater {
			heater = map[string]any{
				"cur": g.Cur, "min": g.Min, "max": g.Max, "text": g.HeaterText,
				"active": g.HeaterActive, "unit": g.HeaterUnit,
				"dec": g.Dec, "inc": g.Inc, "off_stage": g.HeaterOff,
				"dec_off": g.Off || g.DecOff, "inc_off": g.Off || g.IncOff,
				"off_btn_off": g.Off || !g.HeaterActive, "off": g.Off,
			}
		}
	}
	body, err := json.Marshal(map[string]any{
		"name": p.H1, "status": s.Status, "read_only": s.ReadOnly,
		"lead": p.Lead, "pill": p.Pill, "pill_class": p.PillClass, "sentence": p.Sentence,
		"age": p.Age, "dot": p.Dot, "banner": p.Banner,
		"hepa": p.HEPA, "hepa_known": p.HEPAKnown, "hepa_class": p.HEPAClass,
		"hepa_text": p.HEPAText, "hepa_label": p.HEPALabel,
		"carbon": p.Carbon, "carbon_known": p.CarbonKnown, "carbon_class": p.CarbonClass,
		"carbon_text": p.CarbonText, "carbon_label": p.CarbonLabel,
		"power_label": p.PowerLabel, "power_stage": p.PowerStage, "power_off": p.PowerOff, "power_text": p.PowerText,
		"power_old": p.PowerOld, "power_verb": p.PowerVerb,
		"power_state": p.PowerState, "power_state_class": p.PowerStateClass, "standby": p.Standby, "write_reason": p.WriteReason,
		"sensors": sensors, "controls": controls, "fan": fan, "heater": heater,
	})
	if err != nil {
		return []byte(`{"error":"state"}`)
	}
	return body
}

func plainHTML(h template.HTML) string {
	var b strings.Builder
	in := false
	for _, r := range string(h) {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// HistoryJSON is the chart payload for /api/history.
func HistoryJSON(s Snapshot) []byte {
	if s.Loc == nil {
		s.Loc = time.UTC
	}
	if s.Metric == "" {
		s.Metric = "particles"
	}
	if s.Span == "" {
		s.Span = "6h"
	}
	groups := chartGroups(s)
	compact := len(groups) > 3
	charts := make([]map[string]any, 0, len(groups))
	var chosen map[string]any
	for _, g := range groups {
		_, payload := buildChartCard(g, s, compact)
		charts = append(charts, payload)
		if g.ID == s.Metric {
			chosen = payload
		}
	}
	if chosen == nil && len(charts) > 0 {
		chosen = charts[0]
	}
	out := map[string]any{
		"metric":  s.Metric,
		"span":    s.Span,
		"overlay": false,
		"series":  []any{},
		"charts":  charts,
	}
	if chosen != nil {
		out["overlay"] = chosen["overlay"]
		out["series"] = chosen["series"]
	}
	raw, err := json.Marshal(out)
	if err != nil || len(raw) == 0 {
		return []byte(`{"series":[],"charts":[]}`)
	}
	return raw
}

// RenderHTML builds a page from a snapshot.
func RenderHTML(s Snapshot) ([]byte, error) {
	var buf strings.Builder
	if err := tmpl.ExecuteTemplate(&buf, "layout", Build(s)); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

// Build converts a snapshot into template data.
func Build(s Snapshot) Page {
	if s.Loc == nil {
		s.Loc = time.UTC
	}
	if s.Now.IsZero() {
		s.Now = time.Now()
	}
	if s.Metric == "" {
		s.Metric = "particles"
	}
	if s.Span == "" {
		s.Span = "6h"
	}
	if s.Kind == "" {
		s.Kind = "all"
	}
	if s.Days == 0 {
		s.Days = 1
	}
	if s.Region == "" {
		s.Region = "us"
	}
	if s.TempUnit == "" {
		s.TempUnit = "C"
	}
	if s.Name == "" {
		s.Name = "Air Purifier PM20"
	}
	p := Page{
		Token: s.Token, Banner: s.Banner, Flash: s.Flash, FlashInfo: s.FlashInfo, Announce: s.Announce,
		Region: s.Region, Email: s.Email, LoginError: s.LoginError,
		Metric: s.Metric, Span: s.Span, Kind: s.Kind, Days: s.Days,
		ReadOnly: s.ReadOnly, Poll: s.PollSeconds, PollSource: sourceLabel(s.PollSource),
		TempUnit: s.TempUnit, TempSource: sourceLabel(s.TempSource),
		ReadOnlyOn: s.ReadOnly, ReadOnlySource: sourceLabel(s.ReadOnlySource),
		ReadOnlyLocked: s.ReadOnlyLocked, HasOverrides: s.HasOverrides,
		OpenAIConfigured: s.OpenAIConfigured, OpenAISaved: s.OpenAISaved,
		OpenAIEnvLocked: s.OpenAIEnvLocked, OpenAIHint: s.OpenAIHint,
		OpenAIModel: ai.NormalizeModel(s.OpenAIModel), OpenAISource: s.OpenAISource,
		OpenAIModels: ai.AllowedModels,
	}
	switch s.Phase {
	case "boot":
		p.Body, p.Gate, p.Title, p.H1 = "gate", "boot", "Connecting to Dreame", "Connecting to Dreame"
		return p
	case "auth", "auth_required":
		if s.Page == "device" || s.Page == "activity" {
			p.Body = "redirect"
			return p
		}
		p.Body, p.Gate, p.Title, p.H1 = "gate", "login", "Sign in to Dreame", "Sign in to Dreame"
		return p
	}
	p.Active = s.Page
	if p.Active == "" {
		p.Active = "overview"
	}
	p.Age, p.Dot = connText(s)
	switch p.Active {
	case "device":
		p.Title, p.H1, p.Body = "Device & settings", "Device", "device"
		fillDevice(&p, s)
	case "activity":
		p.Title, p.H1, p.Body = "Activity", "Activity", "activity"
		p.Rows = formatRows(s.Rows, s.Now, s.Loc, s.Kind)
		p.Total = len(p.Rows)
		p.Shown = len(p.Rows)
	default:
		p.Title, p.H1, p.Body = s.Name, s.Name, "overview"
		p.Active = "overview"
		fillOverview(&p, s)
	}
	return p
}

func fillOverview(p *Page, s Snapshot) {
	label, class, estimate := band(s)
	if s.PowerKnown && s.Power == 2 {
		label, class = "Standby", "muted"
	} else if !s.PM25Known && !s.AQLKnown {
		label, class = "Unavailable", "muted"
	}
	p.Pill, p.PillClass = label, class
	p.Lead = dashNum(s.PM25, s.PM25Known, 0)
	p.Sentence = sentence(s, label)
	p.Sensors = sensorPairs(s)
	fillDeviceControls(p, s)
	p.Charts = chartCards(s)
	if len(p.Charts) > 0 {
		p.Tracks = p.Charts[0].Tracks
		p.XTicks = p.Charts[0].XTicks
		p.Stats = p.Charts[0].Stats
		p.Readouts = p.Charts[0].Readouts
		p.EmptyChart = p.Charts[0].EmptyChart
		p.ChartJSON = p.Charts[0].ChartJSON
	}
	p.AQEstimate = estimate
	fillDailyAI(p, s)
}

func fillDailyAI(p *Page, s Snapshot) {
	if !s.OpenAIConfigured {
		return
	}
	p.DailyAI = s.DailyAI
	p.DailyAIClass = "muted"
	p.DailyAIMeta = "Auto-updated hourly · 1h granularity"
	if s.DailyAI != nil {
		p.DailyAIText = strings.TrimSpace(s.DailyAI.Summary)
		if p.DailyAIText == "" {
			p.DailyAIText = strings.TrimSpace(s.DailyAI.Headline)
		}
		switch s.DailyAI.Status {
		case "concern":
			p.DailyAIClass, p.DailyAILabel = "danger", "Concern"
		case "attention":
			p.DailyAIClass, p.DailyAILabel = "warn", "Attention"
		default:
			p.DailyAIClass, p.DailyAILabel = "ok", "Good"
		}
		var parts []string
		parts = append(parts, "Hourly (1h)")
		if s.DailyAI.Model != "" {
			parts = append(parts, s.DailyAI.Model)
		}
		if genAt, err := time.Parse(time.RFC3339, s.DailyAI.GeneratedAt); err == nil {
			loc := s.Loc
			if loc == nil {
				loc = time.UTC
			}
			parts = append(parts, "Updated "+genAt.In(loc).Format("15:04"))
		}
		if s.DailyAINext != "" {
			parts = append(parts, "Next "+s.DailyAINext)
		}
		p.DailyAIMeta = strings.Join(parts, " · ")
		if s.DailyAIError != "" {
			p.DailyAIState = "Last refresh failed: " + s.DailyAIError
		}
		return
	}
	if s.DailyAIError != "" {
		p.DailyAIState = "Last update failed: " + s.DailyAIError
	} else {
		p.DailyAIState = "Generating hourly summary from current and 1h daily readings…"
	}
}

func fillDevice(p *Page, s Snapshot) {
	p.Identity = pairs(s.Identity)
	p.Network = pairs(s.Network)
	p.Props = s.Props
	p.Verified = s.Verified
	p.OTAState = otaLabel(s.OTAState)
	p.OTAClass = otaClass(s.OTAState)
	p.OTAProgress = s.OTAProgress
	p.OTAWhen = s.OTAWhen
	p.OTAVersion = s.OTAVersion
	if p.OTAVersion == "" {
		p.OTAVersion = "—"
	}
	p.OTANewVersion = s.OTANewVersion
	p.OTAHasUpdate = s.OTAHasUpdate
	p.OTATarget = s.OTATarget
	p.OTADescription = s.OTADescription
	p.OTAChecked = s.OTAChecked
	p.OTAPill, p.OTAPillClass = otaBadge(p.OTAState, p.OTAClass, s.OTAHasUpdate, s.OTAChecked != "")
	activeOTA := p.OTAClass == "warn"
	p.OTACanUpdate = s.OTAHasUpdate && !s.ReadOnly && s.Status != "offline" && !activeOTA
	if s.Query.Get("stage") == "logout" {
		p.LogoutConfirm = true
	}
	if s.Query.Get("stage") == "ota_update" && p.OTACanUpdate {
		p.OTAUpdateConfirm = true
	}
	p.AQEstimate = s.AQEstimate
	fillDeviceControls(p, s)
}

func fillDeviceControls(p *Page, s Snapshot) {
	p.HEPAKnown = s.HEPAKnown
	p.HEPAClass = filterClass(s.HEPA, s.HEPAKnown)
	if s.HEPAKnown {
		p.HEPA = clamp(int(math.Round(s.HEPA)), 0, 100)
		p.HEPALabel = strconv.Itoa(p.HEPA)
	}
	p.HEPAText = hepaText(s)
	p.ShowCarbon = s.PM20Controls || s.CarbonKnown || s.CarbonDaysKnown
	p.CarbonKnown = s.CarbonKnown
	p.CarbonClass = filterClass(s.Carbon, s.CarbonKnown)
	if s.CarbonKnown {
		p.Carbon = clamp(int(math.Round(s.Carbon)), 0, 100)
		p.CarbonLabel = strconv.Itoa(p.Carbon)
	}
	p.CarbonText = carbonText(s)
	p.PowerActionOK = s.PowerActionOK
	p.PowerOff = !s.WritesOK || !s.PowerKnown || !s.PowerActionOK
	p.Standby = s.PowerKnown && s.Power == 2
	if s.PowerKnown {
		if s.Power == 2 {
			p.PowerState = "Standby"
			p.PowerStateClass = "muted"
		} else {
			p.PowerState = "Working"
			p.PowerStateClass = "ok"
		}
	}
	if s.PowerKnown && s.Power == 2 {
		p.PowerLabel = "Turn on"
		p.PowerStage = "power:2"
		p.PowerOld = "2"
		p.PowerText = "Turn purifier on? (Power: Standby -> Working)"
		p.PowerVerb = "Confirm turn on"
	} else {
		p.PowerLabel = "Standby"
		p.PowerStage = "power:1"
		p.PowerOld = "1"
		p.PowerText = "Put purifier in standby? (Power: Working -> Standby)"
		p.PowerVerb = "Confirm standby"
	}
	p.Groups = controlGroups(s)
	p.Confirm = confirmation(s)
	p.WriteReason = writeNote(s)
}

func writeNote(s Snapshot) string {
	if s.ReadOnly {
		return "Read-only mode is on"
	}
	if !s.WritesOK {
		if s.WriteReason != "" {
			return s.WriteReason
		}
		return "Run dmd probe-writes to enable controls"
	}
	if s.PowerKnown && s.Power == 2 {
		if s.PowerActionOK {
			return "In standby — turn on the purifier to adjust airflow or heater."
		}
		return "In standby — wake the purifier before changing mode or fan speed."
	}
	return ""
}

func sourceLabel(s string) string {
	if s == "" {
		return "env"
	}
	return s
}

func connText(s Snapshot) (string, string) {
	age := "—"
	if !s.StatusAt.IsZero() {
		age = ageText(s.Now.Sub(s.StatusAt))
	}
	switch s.Status {
	case "offline":
		return "Offline", "danger"
	case "degraded":
		return "Degraded, " + age, "warn"
	case "", "boot":
		return "Connecting", "muted"
	default:
		if s.MQTT == "connected" {
			return "Live via MQTT", "ok"
		}
		return "Online, " + age, "ok"
	}
}

func ageText(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%d s ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d m ago", int(d.Minutes()))
	}
	return fmt.Sprintf("%d h ago", int(d.Hours()))
}

// AQBand maps a PM2.5 reading to the 2024 EPA half-open breakpoints.
func AQBand(pm float64) (string, string) {
	switch {
	case pm < 9.1:
		return "Good", "ok"
	case pm < 35.5:
		return "Moderate", "warn"
	case pm < 55.5:
		return "Poor", "poor"
	default:
		return "Unhealthy", "danger"
	}
}

func band(s Snapshot) (string, string, bool) {
	if s.AQLKnown && s.AQLMap != nil {
		if label := s.AQLMap[strconv.Itoa(s.AQL)]; label != "" {
			return label, classFor(label), false
		}
	}
	if !s.PM25Known {
		return "Unavailable", "muted", true
	}
	l, c := AQBand(s.PM25)
	return l, c, true
}

func classFor(label string) string {
	switch strings.ToLower(label) {
	case "good", "excellent":
		return "ok"
	case "moderate":
		return "warn"
	case "poor":
		return "poor"
	case "unhealthy", "bad":
		return "danger"
	default:
		return "muted"
	}
}

func sentence(s Snapshot, label string) string {
	if s.PowerKnown && s.Power == 2 {
		when := "just now"
		if !s.StatusAt.IsZero() {
			when = ageText(s.Now.Sub(s.StatusAt))
		}
		return "In standby. Last reading " + when + "."
	}
	aq := "—"
	if s.AQKnown {
		aq = trim(s.AQ) + "%"
	}
	mode := "an unknown mode"
	if s.ModeKnown {
		mode = modeName(s.Mode) + " mode"
	}
	fan := ""
	if s.FanKnown {
		fan = fmt.Sprintf(" at fan speed %d", s.Fan)
	}
	if label == "Unavailable" {
		return "Waiting for the first sensor reading."
	}
	return fmt.Sprintf("Air quality is %s. Running in %s%s.", aq, mode, fan)
}

func modeName(v int) string {
	switch v {
	case 0:
		return "AI"
	case 1:
		return "Strong"
	case 2:
		return "Sleep"
	case 3:
		return "Custom"
	case 4:
		return "Pet"
	case 5:
		return "Comfort"
	default:
		return strconv.Itoa(v)
	}
}

func filterClass(pct float64, known bool) string {
	if !known {
		return "muted"
	}
	if pct < 10 {
		return "danger"
	}
	if pct < 20 {
		return "warn"
	}
	return "ok"
}

func sensorPairs(s Snapshot) []Pair {
	unit := "°C"
	temp := s.TempC
	if strings.EqualFold(s.TempUnit, "F") && s.TempKnown {
		temp = s.TempC*9/5 + 32
		unit = "°F"
	}
	type item struct {
		id    string
		label string
		value template.HTML
	}
	all := []item{
		{"3.12", "Air quality", nb(dashNum(s.AQ, s.AQKnown, 0), "%")},
		{"3.3", "Temperature", nb(dashNum(temp, s.TempKnown, 1), unit)},
		{"3.10", "PM1", nb(dashNum(s.PM1, s.PM1Known, 0), "µg/m³")},
		{"3.2", "Humidity", nb(dashNum(s.RH, s.RHKnown, 0), "%")},
		{"3.6", "PM10", nb(dashNum(s.PM10, s.PM10Known, 0), "µg/m³")},
		{"3.7", "HCHO", nb(dashNum(s.HCHO, s.HCHOKnown, 3), "mg/m³")},
		{"3.8", "TVOC", nb(dashNum(s.TVOC, s.TVOCKnown, 0), "µg/m³")},
		{"3.4", "AQ level", html(dashNum(float64(s.AQL), s.AQLKnown, 0))},
	}
	var out []Pair
	for _, it := range all {
		if s.Pruned[it.id] {
			continue
		}
		out = append(out, Pair{Label: it.label, Value: it.value})
	}
	return out
}

func hepaText(s Snapshot) string {
	if s.PM20Controls || s.CarbonDaysKnown {
		if s.HEPADaysKnown {
			return daysLeftText(s.HEPADays)
		}
		if s.HEPAKnown {
			return "Filter life remaining"
		}
		return "Filter life has not been read yet."
	}
	if !s.HEPAKnown && !s.HEPADaysKnown && !s.FilterHoursKnown {
		return "Filter life has not been read yet."
	}
	days := "—"
	total := "720"
	if s.HEPADaysKnown && s.HEPAKnown {
		left := s.HEPADays * s.HEPA / 100
		days = trim(left)
		total = trim(s.HEPADays)
	} else if s.HEPADaysKnown {
		total = trim(s.HEPADays)
	}
	hours := "—"
	if s.FilterHoursKnown {
		hours = trim(s.FilterHours)
	}
	return days + " of " + total + " days left, " + hours + " h used"
}

func carbonText(s Snapshot) string {
	if s.CarbonDaysKnown {
		return daysLeftText(s.CarbonDays)
	}
	if s.CarbonKnown {
		return "Filter life remaining"
	}
	return "Filter life has not been read yet."
}

func daysLeftText(days float64) string {
	d := trim(days)
	if d == "1" {
		return "1 day left"
	}
	return d + " days left"
}

func controlGroups(s Snapshot) []Group {
	off := !s.WritesOK
	standby := s.PowerKnown && s.Power == 2
	var g []Group
	if !s.Pruned["2.3"] {
		modes := s.ModeVals
		if len(modes) == 0 {
			modes = []int{0, 1, 2, 3, 4}
		}
		old := ""
		if s.ModeKnown {
			old = strconv.Itoa(s.Mode)
		}
		var modeOpts []Opt
		for _, v := range modes {
			modeOpts = append(modeOpts, Opt{
				Label: modeName(v), Value: strconv.Itoa(v), Pressed: s.ModeKnown && s.Mode == v,
				Stage: "2.3:" + strconv.Itoa(v), Old: old, DataLabel: "Mode",
				Confirm: fmt.Sprintf("Change mode from %s to %s.", modeOld(s), modeName(v)),
			})
		}
		g = append(g, Group{Label: "Mode", Opts: modeOpts, Off: off || standby})
	}
	if s.Pruned["2.4"] {
		if s.PM20Controls {
			return appendPM20Controls(g, s, off, standby)
		}
		return appendLights(g, s, off)
	}
	fan := Group{Label: "Fan speed", Fan: true, Off: off || standby || s.FanAuto}
	max := s.FanMax
	if max == 0 {
		max = 5
	}
	min := s.FanMin
	if min == 0 && s.FanMax == 0 {
		min = 1
	}
	cur := s.Fan
	if !s.FanKnown {
		cur = min
	}
	dec, inc := cur-1, cur+1
	if dec < min {
		dec = min
	}
	if inc > max {
		inc = max
	}
	fan.Dec = "2.4:" + strconv.Itoa(dec)
	fan.Inc = "2.4:" + strconv.Itoa(inc)
	fan.FanText = fmt.Sprintf("%d / %d", cur, max)
	fan.Cur = cur
	fan.Min = min
	fan.Max = max
	fan.DecOff = !s.FanKnown || cur <= min
	fan.IncOff = !s.FanKnown || cur >= max
	fan.Forces = s.FanForcesCustom && s.ModeKnown && s.Mode != 3
	if s.ModeKnown {
		fan.ModeName = modeName(s.Mode)
	}
	if s.FanAuto && !standby {
		fan.Note = "Fan speed is automatic in " + modeName(s.Mode) + " mode."
		fan.Off = true
	}
	g = append(g, fan)
	if s.PM20Controls {
		return appendPM20Controls(g, s, off, standby)
	}
	return appendLights(g, s, off)
}

func appendPM20Controls(g []Group, s Snapshot, off, standby bool) []Group {
	if !s.Pruned["2.7"] && len(s.OscillationVals) > 0 {
		old := ""
		from := "—"
		if s.OscillationKnown {
			old = strconv.Itoa(s.Oscillation)
			from = oscName(s.Oscillation)
		}
		var opts []Opt
		for _, v := range s.OscillationVals {
			confirm := fmt.Sprintf("Change oscillation from %s to %s.", from, oscName(v))
			if v != 0 && s.FollowMotionKnown && s.FollowMotion == 1 {
				confirm += " Turns off Follow motion."
			}
			opts = append(opts, Opt{
				Label: oscName(v), Value: strconv.Itoa(v), Pressed: s.OscillationKnown && s.Oscillation == v,
				Stage: "2.7:" + strconv.Itoa(v), Old: old, DataLabel: "Oscillation",
				Confirm: confirm,
			})
		}
		g = append(g, Group{Label: "Oscillation", Opts: opts, Off: off || standby})
	}
	if !s.Pruned["2.6"] && s.HeaterMax > 0 {
		min := s.HeaterMin
		if min <= 0 {
			min = 18
		}
		max := s.HeaterMax
		active := s.HeaterKnown && s.Heater >= min && s.Heater <= max
		cur := -1
		if active {
			cur = s.Heater
		}
		startTemp := clamp(22, min, max)
		dec, inc := min, startTemp
		if active {
			dec = clamp(cur-1, min, max)
			inc = clamp(cur+1, min, max)
		}
		unit := "C"
		if strings.EqualFold(s.TempUnit, "F") {
			unit = "F"
		}
		text := "—"
		if s.HeaterKnown {
			text = heaterDisplay(cur, unit)
		}
		g = append(g, Group{
			Label:        "Heater",
			Heater:       true,
			HeaterText:   text,
			HeaterActive: active,
			HeaterUnit:   unit,
			HeaterOff:    "2.6:-1",
			Dec:          "2.6:" + strconv.Itoa(dec),
			Inc:          "2.6:" + strconv.Itoa(inc),
			DecOff:       !active || cur <= min,
			IncOff:       !s.HeaterKnown || (active && cur >= max),
			Cur:          cur,
			Min:          min,
			Max:          max,
			Off:          off || standby,
		})
	}
	if !s.Pruned["6.20"] && s.FollowMotionOK {
		g = append(g, boolControlGroup("Follow motion", "6.20", s.FollowMotion, s.FollowMotionKnown, off || standby))
	}
	if !s.Pruned["6.19"] && s.NearbyPowerOnOK {
		g = append(g, boolControlGroup("Nearby power-on", "6.19", s.NearbyPowerOn, s.NearbyPowerOnKnown, off))
	}
	if !s.Pruned["6.15"] && s.ContinuousMonitoringOK {
		g = append(g, boolControlGroup("Continuous monitoring", "6.15", s.ContinuousMonitoring, s.ContinuousMonitoringKnown, off))
	}
	return g
}

func boolControlGroup(label, prop string, cur int, known, off bool) Group {
	old := ""
	from := "—"
	if known {
		old = strconv.Itoa(cur)
		from = onOff(cur)
	}
	low := strings.ToLower(label)
	opts := make([]Opt, 0, 2)
	for _, v := range []int{0, 1} {
		opts = append(opts, Opt{
			Label: onOff(v), Value: strconv.Itoa(v), Pressed: known && cur == v,
			Stage: prop + ":" + strconv.Itoa(v), Old: old, DataLabel: label,
			Confirm: fmt.Sprintf("Change %s from %s to %s.", low, from, onOff(v)),
		})
	}
	return Group{Label: label, Opts: opts, Off: off}
}

func oscName(v int) string {
	if v == 0 {
		return "Off"
	}
	return fmt.Sprintf("%d°", v)
}

func heaterDisplay(c int, unit string) string {
	if c < 0 {
		return "Off"
	}
	if strings.EqualFold(unit, "F") {
		f := int(math.Round(float64(c)*9/5 + 32))
		return fmt.Sprintf("%d °F", f)
	}
	return fmt.Sprintf("%d °C", c)
}

func heaterConfirmVal(c int, unit string) string {
	if c < 0 {
		return "Off"
	}
	if strings.EqualFold(unit, "F") {
		f := int(math.Round(float64(c)*9/5 + 32))
		return fmt.Sprintf("%d °F (%d °C)", f, c)
	}
	return fmt.Sprintf("%d °C", c)
}

func appendLights(g []Group, s Snapshot, off bool) []Group {
	lights := s.LightVals
	kind := s.LightKind
	if len(lights) == 0 {
		if kind == "color" {
			lights = []int{-1, 0, 1, 2}
		} else {
			lights = []int{-1, 0}
			kind = "onoff"
		}
	}
	if !s.Pruned["2.6"] {
		old := ""
		from := "—"
		if s.LightKnown {
			old = strconv.Itoa(s.Light)
			from = lightName(s.Light, kind)
		}
		var lopts []Opt
		for _, v := range lights {
			lopts = append(lopts, Opt{
				Label: lightName(v, kind), Value: strconv.Itoa(v), Pressed: s.LightKnown && s.Light == v,
				Stage: "2.6:" + strconv.Itoa(v), Old: old, DataLabel: "Indicator",
				Confirm: fmt.Sprintf("Change indicator from %s to %s.", from, lightName(v, kind)),
			})
		}
		g = append(g, Group{Label: "Indicator", Opts: lopts, Off: off})
	}
	if !s.Pruned["2.5"] {
		vols := s.VolumeVals
		if len(vols) == 0 {
			vols = []int{80, 90, 100}
		}
		old := ""
		from := "—"
		if s.VolumeKnown {
			old = strconv.Itoa(s.Volume)
			from = old + "%"
		}
		var vopts []Opt
		for _, v := range vols {
			vopts = append(vopts, Opt{
				Label: strconv.Itoa(v) + "%", Value: strconv.Itoa(v), Pressed: s.VolumeKnown && s.Volume == v,
				Stage: "2.5:" + strconv.Itoa(v), Old: old, DataLabel: "Voice volume",
				Confirm: fmt.Sprintf("Change voice volume from %s to %d%%.", from, v),
			})
		}
		g = append(g, Group{Label: "Voice volume", Opts: vopts, Off: off})
	}
	if !s.Pruned["2.7"] {
		tones := s.ToneVals
		if len(tones) == 0 {
			tones = []int{1, 0}
		}
		old := ""
		from := "—"
		if s.ToneKnown {
			old = strconv.Itoa(s.Tone)
			from = onOff(s.Tone)
		}
		var topts []Opt
		for _, v := range tones {
			topts = append(topts, Opt{
				Label: onOff(v), Value: strconv.Itoa(v), Pressed: s.ToneKnown && s.Tone == v,
				Stage: "2.7:" + strconv.Itoa(v), Old: old, DataLabel: "Key tone",
				Confirm: fmt.Sprintf("Change key tone from %s to %s.", from, onOff(v)),
			})
		}
		g = append(g, Group{Label: "Key tone", Opts: topts, Off: off})
	}
	return g
}

func modeOld(s Snapshot) string {
	if !s.ModeKnown {
		return "—"
	}
	return modeName(s.Mode)
}

func lightName(v int, kind string) string {
	if kind == "color" {
		switch v {
		case -1:
			return "Off"
		case 0:
			return "Blue"
		case 1:
			return "Orange"
		case 2:
			return "Green"
		}
	}
	if v == 0 || v == 1 {
		return "On"
	}
	return "Off"
}

func onOff(v int) string {
	if v == 0 {
		return "Off"
	}
	return "On"
}

func confirmation(s Snapshot) *Confirm {
	if !s.WritesOK {
		return nil
	}
	stage := ""
	if s.Query != nil {
		stage = s.Query.Get("stage")
	}
	if stage == "" || stage == "logout" || stage == "ota_update" {
		return nil
	}
	parts := strings.SplitN(stage, ":", 2)
	if len(parts) != 2 {
		return nil
	}
	if parts[0] == "power" {
		if !s.PowerKnown || (parts[1] != "1" && parts[1] != "2") {
			return nil
		}
		if parts[1] == "2" {
			return &Confirm{Text: "Turn purifier on? (Power: Standby -> Working)", Action: "toggle_power", SIID: 2, PIID: 1, Value: "2", Expected: "2", Verb: "Confirm turn on"}
		}
		return &Confirm{Text: "Put purifier in standby? (Power: Working -> Standby)", Action: "toggle_power", SIID: 2, PIID: 1, Value: "1", Expected: "1", Verb: "Confirm standby"}
	}
	if !strings.Contains(parts[0], ".") {
		return nil
	}
	bits := strings.Split(parts[0], ".")
	if len(bits) != 2 {
		return nil
	}
	siid, err1 := strconv.Atoi(bits[0])
	piid, err2 := strconv.Atoi(bits[1])
	if err1 != nil || err2 != nil {
		return nil
	}
	return propConfirm(s, siid, piid, parts[1])
}

func propConfirm(s Snapshot, siid, piid int, raw string) *Confirm {
	val, err := strconv.Atoi(raw)
	if err != nil || !stageAllowed(s, siid, piid, val) {
		return nil
	}
	c := &Confirm{Action: "set", SIID: siid, PIID: piid, Value: raw, Verb: "Confirm change"}
	switch fmt.Sprintf("%d.%d", siid, piid) {
	case "2.3":
		old := "—"
		if s.ModeKnown {
			old = modeName(s.Mode)
			c.Expected = strconv.Itoa(s.Mode)
		}
		c.Text = fmt.Sprintf("Change mode from %s to %s.", old, modeName(val))
	case "2.4":
		old := "—"
		if s.FanKnown {
			old = strconv.Itoa(s.Fan)
			c.Expected = old
		}
		c.Text = fmt.Sprintf("Fan speed: %s -> %s.", old, raw)
		if s.FanForcesCustom && s.ModeKnown && s.Mode != 3 {
			c.Text += " Mode changes from " + modeName(s.Mode) + " to Custom."
		}
	case "2.5":
		old := "—"
		if s.VolumeKnown {
			old = strconv.Itoa(s.Volume) + "%"
			c.Expected = strconv.Itoa(s.Volume)
		}
		c.Text = fmt.Sprintf("Change voice volume from %s to %s%%.", old, raw)
	case "2.6":
		if s.PM20Controls {
			old := "—"
			if s.HeaterKnown {
				old = heaterConfirmVal(s.Heater, s.TempUnit)
				c.Expected = strconv.Itoa(s.Heater)
			}
			if val < 0 {
				c.Text = fmt.Sprintf("Turn heater off? (%s -> Off)", old)
			} else {
				c.Text = fmt.Sprintf("Heater: %s -> %s.", old, heaterConfirmVal(val, s.TempUnit))
			}
			break
		}
		old := "—"
		if s.LightKnown {
			old = lightName(s.Light, s.LightKind)
			c.Expected = strconv.Itoa(s.Light)
		}
		c.Text = fmt.Sprintf("Change indicator from %s to %s.", old, lightName(val, s.LightKind))
	case "2.7":
		if s.PM20Controls {
			old := "—"
			if s.OscillationKnown {
				old = oscName(s.Oscillation)
				c.Expected = strconv.Itoa(s.Oscillation)
			}
			c.Text = fmt.Sprintf("Change oscillation from %s to %s.", old, oscName(val))
			if val != 0 && s.FollowMotionKnown && s.FollowMotion == 1 {
				c.Text += " Turns off Follow motion."
			}
			break
		}
		old := "—"
		if s.ToneKnown {
			old = onOff(s.Tone)
			c.Expected = strconv.Itoa(s.Tone)
		}
		c.Text = fmt.Sprintf("Change key tone from %s to %s.", old, onOff(val))
	case "6.15":
		old := "—"
		if s.ContinuousMonitoringKnown {
			old = onOff(s.ContinuousMonitoring)
			c.Expected = strconv.Itoa(s.ContinuousMonitoring)
		}
		c.Text = fmt.Sprintf("Change continuous monitoring from %s to %s.", old, onOff(val))
	case "6.19":
		old := "—"
		if s.NearbyPowerOnKnown {
			old = onOff(s.NearbyPowerOn)
			c.Expected = strconv.Itoa(s.NearbyPowerOn)
		}
		c.Text = fmt.Sprintf("Change nearby power-on from %s to %s.", old, onOff(val))
	case "6.20":
		old := "—"
		if s.FollowMotionKnown {
			old = onOff(s.FollowMotion)
			c.Expected = strconv.Itoa(s.FollowMotion)
		}
		c.Text = fmt.Sprintf("Change follow motion from %s to %s.", old, onOff(val))
	default:
		return nil
	}
	return c
}

func stageAllowed(s Snapshot, siid, piid, val int) bool {
	if s.Pruned[fmt.Sprintf("%d.%d", siid, piid)] {
		return false
	}
	standby := s.PowerKnown && s.Power == 2
	switch fmt.Sprintf("%d.%d", siid, piid) {
	case "2.3":
		if standby {
			return false
		}
		modes := s.ModeVals
		if len(modes) == 0 {
			modes = []int{0, 1, 2, 3, 4}
		}
		if s.ModeKnown && s.Mode == val {
			return false
		}
		return containsInt(modes, val)
	case "2.4":
		if standby {
			return false
		}
		min, max := 1, 5
		if s.FanMax > 0 || s.FanMin > 0 {
			min, max = s.FanMin, s.FanMax
		}
		if val < min || val > max {
			return false
		}
		if s.FanKnown && s.Fan == val {
			return false
		}
		return true
	case "2.5":
		if s.PM20Controls {
			return false
		}
		vols := s.VolumeVals
		if len(vols) == 0 {
			vols = []int{80, 90, 100}
		}
		if s.VolumeKnown && s.Volume == val {
			return false
		}
		return containsInt(vols, val)
	case "2.6":
		if s.PM20Controls {
			if standby || s.HeaterMax <= 0 {
				return false
			}
			min := s.HeaterMin
			if min <= 0 {
				min = 18
			}
			if val != -1 && (val < min || val > s.HeaterMax) {
				return false
			}
			if s.HeaterKnown && s.Heater == val {
				return false
			}
			return true
		}
		lights := s.LightVals
		kind := s.LightKind
		if len(lights) == 0 {
			if kind == "color" {
				lights = []int{-1, 0, 1, 2}
			} else {
				lights = []int{-1, 0}
			}
		}
		if s.LightKnown && s.Light == val {
			return false
		}
		return containsInt(lights, val)
	case "2.7":
		if s.PM20Controls {
			if standby {
				return false
			}
			if s.OscillationKnown && s.Oscillation == val {
				return false
			}
			return containsInt(s.OscillationVals, val)
		}
		tones := s.ToneVals
		if len(tones) == 0 {
			tones = []int{1, 0}
		}
		if s.ToneKnown && s.Tone == val {
			return false
		}
		return containsInt(tones, val)
	case "6.15":
		if !s.ContinuousMonitoringOK || (val != 0 && val != 1) {
			return false
		}
		return !(s.ContinuousMonitoringKnown && s.ContinuousMonitoring == val)
	case "6.19":
		if !s.NearbyPowerOnOK || (val != 0 && val != 1) {
			return false
		}
		return !(s.NearbyPowerOnKnown && s.NearbyPowerOn == val)
	case "6.20":
		if standby || !s.FollowMotionOK || (val != 0 && val != 1) {
			return false
		}
		return !(s.FollowMotionKnown && s.FollowMotion == val)
	default:
		return false
	}
}

func containsInt(vals []int, n int) bool {
	for _, v := range vals {
		if v == n {
			return true
		}
	}
	return false
}

func chartGroups(s Snapshot) []ChartGroup {
	if len(s.Charts) > 0 {
		return s.Charts
	}
	id := s.Metric
	if id == "" {
		id = "particles"
	}
	title := metricTitle(id)
	return []ChartGroup{{
		ID:      id,
		Title:   title,
		Overlay: id == "particles" || len(s.Series) > 2,
		Series:  s.Series,
	}}
}

func metricTitle(id string) string {
	switch id {
	case "pm25":
		return "PM2.5"
	case "pm10":
		return "PM10"
	case "pm1":
		return "PM1"
	case "aq":
		return "Air quality"
	case "t":
		return "Temperature"
	case "rh":
		return "Humidity"
	case "hcho":
		return "HCHO"
	case "tvoc":
		return "TVOC"
	case "climate":
		return "Climate"
	case "gas":
		return "Gas"
	default:
		return "Particles"
	}
}

func isTechnicalLabel(s string) bool {
	return s == "PM2.5" || s == "PM10" || s == "PM1" || s == "HCHO" || s == "TVOC"
}

func seriesColorVar(class string) string {
	switch class {
	case "series-2":
		return "var(--series-2)"
	case "series-3":
		return "var(--series-3)"
	default:
		return "var(--ok)"
	}
}

func chartCards(s Snapshot) []ChartCard {
	groups := chartGroups(s)
	compact := len(groups) > 3
	cards := make([]ChartCard, 0, len(groups))
	for _, g := range groups {
		card, _ := buildChartCard(g, s, compact)
		cards = append(cards, card)
	}
	return cards
}

func buildChartCard(g ChartGroup, s Snapshot, compact bool) (ChartCard, map[string]any) {
	id := g.ID
	if id == "" {
		id = "chart"
	}
	title := g.Title
	if title == "" {
		title = "Air history"
	}
	longest := 0
	for _, ser := range g.Series {
		if len(ser.Points) > longest {
			longest = len(ser.Points)
		}
	}
	empty := longest < 2
	overlay := g.Overlay || len(g.Series) > 2
	half := len(g.Series) > 1 && !overlay
	h := 136.0
	if half {
		h = 64
	} else if compact {
		h = 108
	}
	var primary []Sample
	for _, ser := range g.Series {
		if len(ser.Points) > len(primary) {
			primary = ser.Points
		}
	}
	unit := ""
	color := "var(--ok)"
	if len(g.Series) > 0 {
		unit = g.Series[0].Unit
		color = seriesColorVar(g.Series[0].Class)
	}
	bands := g.Bands
	if bands == nil && !overlay && len(g.Series) == 1 {
		bands = BandsFor(id, unit)
	}
	var tracks []Track
	if overlay {
		var all []Pt
		var ys []float64
		var seriesPts [][]Pt
		for _, ser := range g.Series {
			var vals []Pt
			for _, pt := range ser.Points {
				p := Pt{X: float64(pt.TS.Unix()), Y: pt.V, Gap: pt.Gap}
				vals = append(vals, p)
				all = append(all, p)
				ys = append(ys, pt.V)
			}
			seriesPts = append(seriesPts, vals)
		}
		minX, maxX, minY, maxY, ok := dataBounds(all)
		var projs [][]Pt
		if ok {
			for _, vals := range seriesPts {
				projs = append(projs, projectOnto(vals, 640, h, 8, minX, maxX, minY, maxY))
			}
		}
		fillID := fmt.Sprintf("fill-%s-0", id)
		svg := svgOverlay(g.Series, projs, fillID, h)
		tracks = []Track{{SVG: svg, YTicks: YTicks(ys), Label: "µg/m³"}}
	} else {
		var allX []Pt
		for _, ser := range g.Series {
			for _, pt := range ser.Points {
				allX = append(allX, Pt{X: float64(pt.TS.Unix()), Y: pt.V, Gap: pt.Gap})
			}
		}
		minX, maxX, _, _, hasX := dataBounds(allX)
		for i, ser := range g.Series {
			var vals []Pt
			var ys []float64
			for _, pt := range ser.Points {
				vals = append(vals, Pt{X: float64(pt.TS.Unix()), Y: pt.V, Gap: pt.Gap})
				ys = append(ys, pt.V)
			}
			fillID := fmt.Sprintf("fill-%s-%d", id, i)
			tickCount := 4
			if half || compact {
				tickCount = 3
			}
			label := ser.Name
			if len(g.Series) == 1 {
				label = ""
			}
			if bands != nil {
				minY, maxY := defaultBandRange(bands)
				var proj []Pt
				if hasX && len(vals) > 0 {
					var ok bool
					_, _, minY, maxY, ok = bandBounds(vals, bands)
					if ok {
						proj = projectOnto(vals, 640, h, 8, minX, maxX, minY, maxY)
					}
				}
				strokeID := fmt.Sprintf("stroke-%s-%d", id, i)
				stops := gradientStops(minY, maxY, h, 8, bands)
				marks := bandThresholds(bands, minY, maxY)
				drawSer := ser
				if bands.TwoSided {
					drawSer.Fill = false
				}
				tracks = append(tracks, Track{
					SVG:    svgBand(proj, drawSer, fillID, strokeID, h, minY, maxY, stops, marks),
					YTicks: yTicksBounds(minY, maxY, tickCount),
					Half:   half,
					Label:  label,
				})
			} else {
				var proj []Pt
				if hasX && len(vals) > 0 {
					_, _, minY, maxY, ok := dataBounds(vals)
					if ok {
						proj = projectOnto(vals, 640, h, 8, minX, maxX, minY, maxY)
					}
				}
				tracks = append(tracks, Track{SVG: svgOne(proj, ser, fillID, h), YTicks: yTicksN(ys, tickCount), Half: half, Label: label})
			}
		}
	}
	xt := xTicks(primary, s.Span, s.Loc)
	if compact && len(xt) > 3 {
		xt = xTicksN(primary, s.Span, s.Loc, 3)
	}
	readouts := liveReadoutsFor(g.Series, s.Span, bands)
	cardZone := ""
	if bands != nil && len(readouts) > 0 {
		cardZone = readouts[0].Zone
	}
	stats := cardStats(g.Series, overlay)
	aria := chartAriaLabel(title, s.Span, readouts)

	seriesPayload := make([]map[string]any, 0, len(g.Series))
	for _, ser := range g.Series {
		pts := make([]map[string]any, 0, len(ser.Points))
		for _, pt := range ser.Points {
			item := map[string]any{"t": pt.TS.Unix(), "v": pt.V, "gap": pt.Gap}
			if pt.Range {
				item["min"] = pt.Min
				item["max"] = pt.Max
			}
			pts = append(pts, item)
		}
		seriesPayload = append(seriesPayload, map[string]any{"name": ser.Name, "unit": ser.Unit, "class": ser.Class, "fill": ser.Fill, "points": pts})
	}
	tracksPayload := make([]map[string]any, 0, len(tracks))
	for _, tr := range tracks {
		tracksPayload = append(tracksPayload, map[string]any{"label": tr.Label, "y_ticks": tr.YTicks})
	}
	readoutsPayload := make([]map[string]any, 0, len(readouts))
	for _, rd := range readouts {
		item := map[string]any{"label": rd.Label, "value": rd.Value}
		if rd.Zone != "" {
			item["zone"] = rd.Zone
		}
		readoutsPayload = append(readoutsPayload, item)
	}
	statsPayload := make([]map[string]any, 0, len(stats))
	for _, st := range stats {
		statsPayload = append(statsPayload, map[string]any{"label": st.Label, "value": st.Value})
	}
	if xt == nil {
		xt = []string{}
	}
	meaning := MetricMeaning(id, unit)
	emptyText := "Collecting readings…"
	if empty && s.PowerKnown && s.Power == 2 && standbyPausesMetric(id) && !(s.ContinuousMonitoringKnown && s.ContinuousMonitoring == 1) {
		emptyText = "Paused in standby"
	}
	payload := map[string]any{
		"id":         id,
		"title":      title,
		"meaning":    meaning,
		"metric":     id,
		"unit":       unit,
		"color":      color,
		"zone":       cardZone,
		"span":       s.Span,
		"overlay":    overlay,
		"compact":    compact,
		"empty":      empty,
		"empty_text": emptyText,
		"aria_label": aria,
		"x_ticks":    xt,
		"tracks":     tracksPayload,
		"readouts":   readoutsPayload,
		"stats":      statsPayload,
		"series":     seriesPayload,
	}
	if bands != nil {
		payload["bands"] = bands
	}
	raw, _ := json.Marshal(payload)
	card := ChartCard{
		ID:          id,
		Title:       title,
		Meaning:     meaning,
		NoTranslate: isTechnicalLabel(title),
		Overlay:     overlay,
		Compact:     compact,
		Zone:        cardZone,
		AriaLabel:   aria,
		Tracks:      tracks,
		XTicks:      xt,
		Stats:       stats,
		Readouts:    readouts,
		EmptyChart:  empty,
		EmptyText:   emptyText,
		ChartJSON:   template.HTML(raw),
	}
	return card, payload
}

func standbyPausesMetric(id string) bool {
	switch id {
	case "pm25", "pm10", "pm1", "t", "rh", "hcho", "particles", "climate":
		return true
	default:
		return false
	}
}

func chartAriaLabel(title, span string, readouts []Readout) string {
	spanText := span
	switch span {
	case "6h":
		spanText = "6 h"
	case "24h":
		spanText = "24 h"
	case "7d":
		spanText = "7 d"
	case "30d":
		spanText = "30 d"
	}
	var parts []string
	for _, r := range readouts {
		parts = append(parts, r.Label+" "+r.Value)
	}
	if len(parts) == 0 {
		return title + " over " + spanText
	}
	return title + " over " + spanText + ": " + strings.Join(parts, ", ")
}

func liveReadoutsFor(series []Series, span string, bands *Bands) []Readout {
	rollup := span == "7d" || span == "30d"
	var out []Readout
	for _, ser := range series {
		val := "—"
		zone := ""
		if n := len(ser.Points); n > 0 {
			pt := ser.Points[n-1]
			if bands != nil {
				zone = ZoneOf(pt.V, bands)
			}
			if rollup && pt.Range {
				val = trim(pt.V) + " (" + trim(pt.Min) + "–" + trim(pt.Max) + ")"
			} else {
				val = trim(pt.V)
				if ser.Unit != "" {
					val += " " + ser.Unit
				}
			}
		}
		nt := ser.Name == "PM2.5" || ser.Name == "PM10" || ser.Name == "PM1" || ser.Name == "HCHO" || ser.Name == "TVOC"
		out = append(out, Readout{Label: ser.Name, Value: val, Zone: zone, NoTranslate: nt})
	}
	return out
}

func cardStats(series []Series, overlay bool) []Stat {
	if overlay || len(series) <= 1 {
		var pts []Sample
		unit := ""
		if len(series) > 0 {
			pts = series[0].Points
			unit = series[0].Unit
		}
		return summary(pts, unit)
	}
	out := make([]Stat, 0, len(series))
	for _, ser := range series {
		out = append(out, Stat{Label: ser.Name, Value: seriesRange(ser.Points, ser.Unit)})
	}
	return out
}

func seriesRange(pts []Sample, unit string) string {
	if len(pts) == 0 {
		return "—"
	}
	minV, maxV, sum := pts[0].V, pts[0].V, 0.0
	for _, p := range pts {
		sum += p.V
		lo, hi := p.V, p.V
		if p.Range {
			lo, hi = p.Min, p.Max
		}
		if lo < minV {
			minV = lo
		}
		if hi > maxV {
			maxV = hi
		}
	}
	avgV := sum / float64(len(pts))
	u := ""
	if unit != "" {
		u = " " + unit
	}
	if trim(minV) == trim(maxV) {
		return trim(avgV) + u
	}
	return trim(minV) + "–" + trim(maxV) + u + " (avg " + trim(avgV) + ")"
}

func summary(pts []Sample, unit string) []Stat {
	if len(pts) == 0 {
		return []Stat{{"Min", "—"}, {"Avg", "—"}, {"Peak", "—"}}
	}
	minV, maxV, sum := pts[0].V, pts[0].V, 0.0
	for _, p := range pts {
		sum += p.V
		lo, hi := p.V, p.V
		if p.Range {
			lo, hi = p.Min, p.Max
		}
		if lo < minV {
			minV = lo
		}
		if hi > maxV {
			maxV = hi
		}
	}
	u := ""
	if unit != "" {
		u = " " + unit
	}
	return []Stat{
		{"Min", trim(minV) + u},
		{"Avg", trim(sum/float64(len(pts))) + u},
		{"Peak", trim(maxV) + u},
	}
}

func xTicks(pts []Sample, span string, loc *time.Location) []string {
	return xTicksN(pts, span, loc, 4)
}

func xTicksN(pts []Sample, span string, loc *time.Location, count int) []string {
	if len(pts) == 0 {
		return nil
	}
	if loc == nil {
		loc = time.UTC
	}
	layout := "15:04"
	if span == "7d" || span == "30d" {
		layout = "Jan 2"
	}
	n := count
	if n < 2 {
		n = 2
	}
	if len(pts) < n {
		n = len(pts)
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		idx := 0
		if n > 1 {
			idx = i * (len(pts) - 1) / (n - 1)
		}
		out[i] = pts[idx].TS.In(loc).Format(layout)
	}
	return out
}

func svgOverlay(series []Series, projs [][]Pt, fillID string, h float64) template.HTML {
	var b strings.Builder
	writeSVGOpen(&b, h, fillID, true, "var(--ok)")
	for i := range series {
		if i >= len(projs) {
			break
		}
		writeSeries(&b, projs[i], series[i], fillID, "", h)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

func svgOne(proj []Pt, ser Series, fillID string, h float64) template.HTML {
	var b strings.Builder
	writeSVGOpen(&b, h, fillID, ser.Fill, seriesColorVar(ser.Class))
	writeSeries(&b, proj, ser, fillID, "", h)
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

func svgBand(proj []Pt, ser Series, fillID, strokeID string, h, minY, maxY float64, stops []GradStop, marks []ThresholdMark) template.HTML {
	var b strings.Builder
	escFill := template.HTMLEscapeString(fillID)
	escStroke := template.HTMLEscapeString(strokeID)
	fmt.Fprintf(&b, `<svg class="chart-svg" data-fill-id="%s" data-stroke-id="%s" viewBox="0 0 640 %.0f" preserveAspectRatio="none" aria-hidden="true">`, escFill, escStroke, h)
	b.WriteString(`<defs>`)
	fmt.Fprintf(&b, `<linearGradient id="%s" gradientUnits="userSpaceOnUse" x1="0" y1="0" x2="0" y2="%.0f">`, escStroke, h)
	for _, st := range stops {
		fmt.Fprintf(&b, `<stop offset="%.2f%%" stop-color="%s"/>`, st.Offset*100, st.Color)
	}
	b.WriteString(`</linearGradient>`)
	if ser.Fill {
		fmt.Fprintf(&b, `<linearGradient id="%s" gradientUnits="userSpaceOnUse" x1="0" y1="0" x2="0" y2="%.0f">`, escFill, h)
		for _, st := range stops {
			op := 0.22*(1-st.Offset) + 0.02
			fmt.Fprintf(&b, `<stop offset="%.2f%%" stop-color="%s" stop-opacity="%.3f"/>`, st.Offset*100, st.Color, op)
		}
		b.WriteString(`</linearGradient>`)
	}
	b.WriteString(`</defs>`)
	for i := 0; i < 4; i++ {
		y := 8 + (h-16)*float64(i)/3
		fmt.Fprintf(&b, `<line class="grid" x1="0" y1="%s" x2="640" y2="%s"/>`, num(y), num(y))
	}
	if maxY > minY {
		inner := h - 16
		for _, m := range marks {
			y := 8 + (maxY-m.Value)/(maxY-minY)*inner
			fmt.Fprintf(&b, `<line class="threshold-line %s" x1="0" y1="%s" x2="640" y2="%s"/>`, template.HTMLEscapeString(m.Class), num(y), num(y))
		}
	}
	writeSeries(&b, proj, ser, fillID, strokeID, h)
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

func writeSVGOpen(b *strings.Builder, h float64, fillID string, fill bool, stopColor string) {
	escID := template.HTMLEscapeString(fillID)
	if stopColor == "" {
		stopColor = "var(--ok)"
	}
	fmt.Fprintf(b, `<svg class="chart-svg" data-fill-id="%s" viewBox="0 0 640 %.0f" preserveAspectRatio="none" aria-hidden="true">`, escID, h)
	if fill {
		fmt.Fprintf(b, `<defs><linearGradient id="%s" x1="0" y1="0" x2="0" y2="1"><stop offset="0%%" stop-color="%s" stop-opacity="0.24"/><stop offset="100%%" stop-color="%s" stop-opacity="0"/></linearGradient></defs>`, escID, stopColor, stopColor)
	}
	for i := 0; i < 4; i++ {
		y := 8 + (h-16)*float64(i)/3
		fmt.Fprintf(b, `<line class="grid" x1="0" y1="%s" x2="640" y2="%s"/>`, num(y), num(y))
	}
}

func writeSeries(b *strings.Builder, proj []Pt, ser Series, fillID, strokeID string, h float64) {
	class := ser.Class
	if class == "" {
		class = "series-a"
	}
	escID := template.HTMLEscapeString(fillID)
	escStroke := template.HTMLEscapeString(strokeID)
	for _, seg := range splitSegs(proj) {
		if seg.D == "" {
			continue
		}
		if ser.Fill && seg.N >= 2 {
			fmt.Fprintf(b, `<path class="area" d="%s L %s,%.0f L %s,%.0f Z" fill="url(#%s)"/>`, template.HTMLEscapeString(seg.D), num(seg.X1), h, num(seg.X0), h, escID)
		}
		if strokeID != "" {
			fmt.Fprintf(b, `<path class="series series-band" d="%s" stroke="url(#%s)"/>`, template.HTMLEscapeString(seg.D), escStroke)
		} else {
			fmt.Fprintf(b, `<path class="series %s" d="%s"/>`, template.HTMLEscapeString(class), template.HTMLEscapeString(seg.D))
		}
	}
}

func formatRows(rows []Act, now time.Time, loc *time.Location, kind string) []Row {
	var out []Row
	for _, r := range rows {
		k := kindLabel(r.Kind)
		if kind == "commands" && r.Kind != "command" {
			continue
		}
		if kind == "events" && r.Kind == "command" {
			continue
		}
		res := r.Result
		if res == "" {
			res = "—"
		}
		if r.MS > 0 && r.Result != "" {
			res = r.Result + " " + strconv.FormatInt(r.MS, 10) + " ms"
		}
		out = append(out, Row{
			When: formatWhen(r.TS, now, loc), Kind: k, Target: r.Target, Detail: r.Detail, Result: res,
			Filter: strings.ToLower(k + " " + r.Target + " " + r.Detail + " " + res),
		})
	}
	return out
}

func kindLabel(k string) string {
	switch k {
	case "command":
		return "Command"
	case "ota":
		return "OTA"
	case "presence":
		return "Presence"
	case "auth":
		return "Auth"
	case "map":
		return "Map"
	case "ai":
		return "AI"
	default:
		if k == "" {
			return "Event"
		}
		return strings.ToUpper(k[:1]) + k[1:]
	}
}

// FormatWhen renders Today, Yesterday, or a calendar day in loc.
func FormatWhen(ts, now time.Time, loc *time.Location) string { return formatWhen(ts, now, loc) }

func formatWhen(ts, now time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	local := ts.In(loc)
	n := now.In(loc)
	ld := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	nd := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
	switch {
	case ld.Equal(nd):
		return "Today " + local.Format("15:04:05")
	case ld.Equal(nd.AddDate(0, 0, -1)):
		return "Yesterday " + local.Format("15:04")
	default:
		return local.Format("2006-01-02 15:04")
	}
}

// FormatTick renders a chart tick in loc.
func FormatTick(ts time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	return ts.In(loc).Format("15:04")
}

func pairs(in [][2]string) []Pair {
	var out []Pair
	for _, p := range in {
		mono := monoLabel(p[0])
		out = append(out, Pair{Label: p[0], Value: html(p[1]), Mono: mono})
	}
	return out
}

func monoLabel(s string) bool {
	switch s {
	case "Model", "Codename", "DID", "Serial", "MAC", "Firmware", "Build", "LAN IP", "Gateway", "BSSID", "Broker":
		return true
	default:
		return false
	}
}

func otaClass(s string) string {
	switch strings.ToLower(s) {
	case "", "idle", "unknown":
		return "muted"
	case "downloading", "dowloaded", "installing":
		return "warn"
	case "failed", "error":
		return "danger"
	default:
		return "ok"
	}
}

func otaBadge(stateLabel, stateClass string, hasUpdate, checked bool) (string, string) {
	if stateClass == "warn" || stateClass == "danger" {
		return stateLabel, stateClass
	}
	if hasUpdate {
		return "Update available", "warn"
	}
	if checked {
		return "Up to date", "ok"
	}
	return stateLabel, stateClass
}

func otaLabel(s string) string {
	switch s {
	case "dowloaded":
		return "Downloaded"
	case "":
		return "Unknown"
	default:
		return strings.ToUpper(s[:1]) + s[1:]
	}
}

func dashNum(v float64, ok bool, frac int) string {
	if !ok {
		return "—"
	}
	if frac == 0 {
		return strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', frac, 64)
}

func trim(v float64) string {
	av := math.Abs(v)
	if av < 0.0005 {
		return "0"
	}
	if av < 1 {
		s := strconv.FormatFloat(v, 'f', 3, 64)
		return strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if math.Abs(v-math.Round(v)) < 0.05 {
		return strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func html(s string) template.HTML { return template.HTML(template.HTMLEscapeString(s)) }

func nb(v, unit string) template.HTML {
	if v == "" || v == "—" {
		return "—"
	}
	return template.HTML(template.HTMLEscapeString(v) + "\u00a0" + template.HTMLEscapeString(unit))
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
