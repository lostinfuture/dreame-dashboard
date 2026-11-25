package web

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Pt is a chart sample in data space. Gap breaks the stroke before this point.
type Pt struct {
	X   float64
	Y   float64
	Gap bool
}

// Bands defines good / moderate / high threshold crossings and a normalized Y range.
type Bands struct {
	TwoSided  bool    `json:"two_sided,omitempty"`
	LowDanger float64 `json:"low_danger,omitempty"`
	LowWarn   float64 `json:"low_warn,omitempty"`
	Warn      float64 `json:"warn"`
	High      float64 `json:"high"`
	NormMin   float64 `json:"norm_min"`
	NormMax   float64 `json:"norm_max"`
	FloorZero bool    `json:"floor_zero,omitempty"`
	ClampMax  float64 `json:"clamp_max,omitempty"`
	Legend    string  `json:"legend,omitempty"`
}

// GradStop is one vertical SVG linearGradient stop (offset in [0, 1]).
type GradStop struct {
	Offset float64
	Color  string
}

// ThresholdMark is one horizontal threshold line drawn inside the plot.
type ThresholdMark struct {
	Value float64
	Class string
	Label string
}

// MetricMeaning returns a brief plain-English explanation of what a sensor chart measures.
func MetricMeaning(id, unit string) string {
	switch id {
	case "pm25":
		return "Fine dust and smoke small enough to reach deep into lungs"
	case "pm10":
		return "Coarser dust, pollen, and pet dander in the air"
	case "pm1":
		return "Ultrafine particles from cooking, candles, and traffic exhaust"
	case "aq":
		return "Purifier's combined pollution score (lower is cleaner)"
	case "t":
		if strings.Contains(strings.ToUpper(unit), "F") {
			return "Room temperature (comfort range 66–77 °F)"
		}
		return "Room temperature (comfort range 19–25 °C)"
	case "rh":
		return "Relative moisture in the air (comfort range 35–55%)"
	case "hcho":
		return "Formaldehyde gas from furniture, paint, and building materials"
	case "tvoc":
		return "Organic vapours from cooking, cleaning products, and sprays"
	default:
		return ""
	}
}

// BandsFor returns indoor air-quality or comfort bands normalized across low/mid/high ranges.
func BandsFor(id, unit string) *Bands {
	switch id {
	case "pm25":
		return &Bands{
			Warn: 9.1, High: 35.5, NormMin: 0, NormMax: 40, FloorZero: true,
			Legend: "Good < 9.1 · Moderate 9.1–35.5 · High ≥ 35.5 µg/m³",
		}
	case "pm10":
		return &Bands{
			Warn: 20, High: 50, NormMin: 0, NormMax: 60, FloorZero: true,
			Legend: "Good < 20 · Moderate 20–50 · High ≥ 50 µg/m³",
		}
	case "pm1":
		return &Bands{
			Warn: 10, High: 25, NormMin: 0, NormMax: 32, FloorZero: true,
			Legend: "Good < 10 · Moderate 10–25 · High ≥ 25 µg/m³",
		}
	case "aq":
		return &Bands{
			Warn: 30, High: 60, NormMin: 0, NormMax: 80, FloorZero: true, ClampMax: 100,
			Legend: "Low < 30% · Moderate 30–60% · High ≥ 60%",
		}
	case "t":
		if strings.Contains(strings.ToUpper(unit), "F") {
			return &Bands{
				TwoSided: true, LowDanger: 61, LowWarn: 66, Warn: 77, High: 82, NormMin: 56, NormMax: 88,
				Legend: "Comfort 66–77 °F · Cool/Warm 61–66 / 77–82 °F · Extreme < 61 / ≥ 82 °F",
			}
		}
		return &Bands{
			TwoSided: true, LowDanger: 16, LowWarn: 19, Warn: 25, High: 28, NormMin: 14, NormMax: 30,
			Legend: "Comfort 19–25 °C · Cool/Warm 16–19 / 25–28 °C · Extreme < 16 / ≥ 28 °C",
		}
	case "rh":
		return &Bands{
			TwoSided: true, LowDanger: 25, LowWarn: 35, Warn: 55, High: 65, NormMin: 15, NormMax: 75, ClampMax: 100,
			Legend: "Comfort 35–55% · Dry/Humid 25–35 / 55–65% · Extreme < 25 / ≥ 65%",
		}
	case "hcho":
		return &Bands{
			Warn: 0.05, High: 0.08, NormMin: 0, NormMax: 0.12, FloorZero: true,
			Legend: "Good < 0.05 · Moderate 0.05–0.08 · High ≥ 0.08 mg/m³",
		}
	case "tvoc":
		return &Bands{
			Warn: 200, High: 500, NormMin: 0, NormMax: 600, FloorZero: true,
			Legend: "Good < 200 · Moderate 200–500 · High ≥ 500 µg/m³",
		}
	default:
		return nil
	}
}

// ZoneOf classifies value v against b into "ok", "warn", or "danger".
func ZoneOf(v float64, b *Bands) string {
	if b == nil {
		return "ok"
	}
	if b.TwoSided {
		if v < b.LowDanger || v >= b.High {
			return "danger"
		}
		if v < b.LowWarn || v > b.Warn {
			return "warn"
		}
		return "ok"
	}
	if v >= b.High {
		return "danger"
	}
	if v >= b.Warn {
		return "warn"
	}
	return "ok"
}

func niceTop(v float64) float64 {
	if v <= 0 {
		return 1
	}
	var step float64
	switch {
	case v <= 0.2:
		step = 0.04
	case v <= 1:
		step = 0.2
	case v <= 20:
		step = 4
	case v <= 100:
		step = 8
	case v <= 400:
		step = 40
	case v <= 1000:
		step = 100
	default:
		step = 200
	}
	return math.Ceil(v/step) * step
}

func defaultBandRange(b *Bands) (float64, float64) {
	if b == nil {
		return 0, 1
	}
	if b.NormMax > b.NormMin {
		return b.NormMin, b.NormMax
	}
	return 0, 1
}

func bandBounds(values []Pt, b *Bands) (minX, maxX, minY, maxY float64, ok bool) {
	minX, maxX, minY, maxY, ok = dataBounds(values)
	if !ok || b == nil {
		return minX, maxX, minY, maxY, ok
	}
	rawMin, rawMax := values[0].Y, values[0].Y
	for _, p := range values {
		if p.Y < rawMin {
			rawMin = p.Y
		}
		if p.Y > rawMax {
			rawMax = p.Y
		}
	}
	minY, maxY = defaultBandRange(b)
	if b.FloorZero {
		minY = 0
		if rawMax > maxY*0.95 {
			maxY = niceTop(rawMax * 1.1)
		}
	} else {
		if rawMin < minY+1 {
			minY = math.Floor((rawMin-2)/4) * 4
		}
		if rawMax > maxY-1 {
			maxY = math.Ceil((rawMax+2)/4) * 4
		}
		if b.ClampMax > 0 && minY < 0 {
			minY = 0
		}
	}
	if b.ClampMax > 0 && maxY > b.ClampMax {
		maxY = b.ClampMax
	}
	if maxY <= minY {
		maxY = minY + 1
	}
	return minX, maxX, minY, maxY, true
}

func bandThresholds(b *Bands, minY, maxY float64) []ThresholdMark {
	if b == nil {
		return nil
	}
	var raw []ThresholdMark
	if b.TwoSided {
		raw = []ThresholdMark{
			{Value: b.High, Class: "threshold-danger", Label: trimNum(b.High)},
			{Value: b.Warn, Class: "threshold-warn", Label: trimNum(b.Warn)},
			{Value: b.LowWarn, Class: "threshold-warn", Label: trimNum(b.LowWarn)},
			{Value: b.LowDanger, Class: "threshold-danger", Label: trimNum(b.LowDanger)},
		}
	} else {
		raw = []ThresholdMark{
			{Value: b.High, Class: "threshold-danger", Label: trimNum(b.High)},
			{Value: b.Warn, Class: "threshold-warn", Label: trimNum(b.Warn)},
		}
	}
	var out []ThresholdMark
	for _, m := range raw {
		if m.Value > minY && m.Value < maxY {
			out = append(out, m)
		}
	}
	return out
}

// gradientStops returns top-to-bottom SVG stops matching threshold crossings.
func gradientStops(minY, maxY, h, pad float64, b *Bands) []GradStop {
	if b == nil || maxY <= minY || h <= 0 {
		return []GradStop{{0, "var(--ok)"}, {1, "var(--ok)"}}
	}
	inner := h - 2*pad
	if inner < 1 {
		inner = h
		pad = 0
	}
	off := func(v float64) float64 {
		y := pad + (maxY-v)/(maxY-minY)*inner
		o := y / h
		if o < 0 {
			return 0
		}
		if o > 1 {
			return 1
		}
		return o
	}
	type region struct {
		lo, hi float64
		color  string
	}
	inf := math.Inf(1)
	var regions []region
	if b.TwoSided {
		regions = []region{
			{b.High, inf, "var(--danger)"},
			{b.Warn, b.High, "var(--warn)"},
			{b.LowWarn, b.Warn, "var(--ok)"},
			{b.LowDanger, b.LowWarn, "var(--warn)"},
			{-inf, b.LowDanger, "var(--danger)"},
		}
	} else {
		regions = []region{
			{b.High, inf, "var(--danger)"},
			{b.Warn, b.High, "var(--warn)"},
			{-inf, b.Warn, "var(--ok)"},
		}
	}
	var stops []GradStop
	for _, r := range regions {
		if r.hi <= minY || r.lo >= maxY {
			continue
		}
		topOff := 0.0
		if !math.IsInf(r.hi, 1) && r.hi < maxY {
			topOff = off(r.hi)
		}
		botOff := 1.0
		if !math.IsInf(r.lo, -1) && r.lo > minY {
			botOff = off(r.lo)
		}
		stops = append(stops, GradStop{topOff, r.color}, GradStop{botOff, r.color})
	}
	if len(stops) == 0 {
		return []GradStop{{0, "var(--ok)"}, {1, "var(--ok)"}}
	}
	return stops
}

func yTicksBounds(minV, maxV float64, count int) []string {
	if count < 2 {
		count = 2
	}
	if maxV <= minV {
		maxV = minV + 1
	}
	steps := count - 1
	out := make([]string, 0, count)
	for i := steps; i >= 0; i-- {
		out = append(out, trimNum(minV+(maxV-minV)*float64(i)/float64(steps)))
	}
	return out
}

// SampleMonotone evaluates the Fritsch–Carlson spline at several steps per segment.
func SampleMonotone(xs, ys []float64, steps int) []float64 {
	if len(xs) == 0 || len(xs) != len(ys) {
		return nil
	}
	if steps < 1 {
		steps = 1
	}
	if len(xs) == 1 {
		return []float64{ys[0]}
	}
	m := tangents(xs, ys)
	var out []float64
	for i := 0; i < len(xs)-1; i++ {
		dx := xs[i+1] - xs[i]
		for s := 0; s < steps; s++ {
			t := float64(s) / float64(steps)
			out = append(out, bezier(ys[i], ys[i]+m[i]*dx/3, ys[i+1]-m[i+1]*dx/3, ys[i+1], t))
		}
	}
	out = append(out, ys[len(ys)-1])
	return out
}

func bezier(p0, p1, p2, p3, t float64) float64 {
	u := 1 - t
	return u*u*u*p0 + 3*u*u*t*p1 + 3*u*t*t*p2 + t*t*t*p3
}

func tangents(xs, ys []float64) []float64 {
	n := len(xs)
	d := make([]float64, n-1)
	dx := make([]float64, n-1)
	for i := 0; i < n-1; i++ {
		dx[i] = xs[i+1] - xs[i]
		if dx[i] == 0 {
			d[i] = 0
		} else {
			d[i] = (ys[i+1] - ys[i]) / dx[i]
		}
	}
	m := make([]float64, n)
	m[0] = d[0]
	for i := 1; i < n-1; i++ {
		if d[i-1]*d[i] <= 0 {
			m[i] = 0
		} else {
			m[i] = (d[i-1] + d[i]) / 2
		}
	}
	m[n-1] = d[n-2]
	for i := 0; i < n-1; i++ {
		if d[i] == 0 {
			m[i] = 0
			m[i+1] = 0
			continue
		}
		alpha := m[i] / d[i]
		beta := m[i+1] / d[i]
		s := alpha*alpha + beta*beta
		if s > 9 {
			tau := 3 / math.Sqrt(s)
			m[i] = tau * alpha * d[i]
			m[i+1] = tau * beta * d[i]
		}
	}
	return m
}

// PathSeg is one contiguous SVG run and the x span used to close its area.
type PathSeg struct {
	D  string
	X0 float64
	X1 float64
	N  int
}

// SplitPaths returns one SVG path per contiguous run. Gaps stay blank.
func SplitPaths(pts []Pt) []string {
	var paths []string
	for _, seg := range splitSegs(pts) {
		if seg.D != "" {
			paths = append(paths, seg.D)
		}
	}
	return paths
}

func splitSegs(pts []Pt) []PathSeg {
	var segs []PathSeg
	var run []Pt
	flush := func() {
		if len(run) == 0 {
			return
		}
		segs = append(segs, PathSeg{D: monotonePath(run), X0: run[0].X, X1: run[len(run)-1].X, N: len(run)})
		run = nil
	}
	for _, p := range pts {
		if p.Gap {
			flush()
		}
		run = append(run, Pt{X: p.X, Y: p.Y})
	}
	flush()
	return segs
}

func monotonePath(pts []Pt) string {
	n := len(pts)
	if n == 0 {
		return ""
	}
	if n == 1 {
		return fmt.Sprintf("M %s,%s", num(pts[0].X), num(pts[0].Y))
	}
	xs := make([]float64, n)
	ys := make([]float64, n)
	for i, p := range pts {
		xs[i], ys[i] = p.X, p.Y
	}
	m := tangents(xs, ys)
	var b strings.Builder
	fmt.Fprintf(&b, "M %s,%s", num(pts[0].X), num(pts[0].Y))
	for i := 0; i < n-1; i++ {
		seg := (pts[i+1].X - pts[i].X) / 3
		cp1x := pts[i].X + seg
		cp1y := pts[i].Y + m[i]*seg
		cp2x := pts[i+1].X - seg
		cp2y := pts[i+1].Y - m[i+1]*seg
		fmt.Fprintf(&b, " C %s,%s %s,%s %s,%s", num(cp1x), num(cp1y), num(cp2x), num(cp2y), num(pts[i+1].X), num(pts[i+1].Y))
	}
	return b.String()
}

func num(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// Project maps data points into an SVG viewBox of the given pixel size.
func Project(values []Pt, width, height, pad float64) []Pt {
	minX, maxX, minY, maxY, ok := dataBounds(values)
	if !ok {
		return nil
	}
	return projectOnto(values, width, height, pad, minX, maxX, minY, maxY)
}

func dataBounds(values []Pt) (minX, maxX, minY, maxY float64, ok bool) {
	if len(values) == 0 {
		return 0, 0, 0, 0, false
	}
	minX, maxX = values[0].X, values[0].X
	minY, maxY = values[0].Y, values[0].Y
	for _, p := range values {
		if p.X < minX {
			minX = p.X
		}
		if p.X > maxX {
			maxX = p.X
		}
		if p.Y < minY {
			minY = p.Y
		}
		if p.Y > maxY {
			maxY = p.Y
		}
	}
	if maxY == minY {
		if minY > 0 && minY < 1 {
			step := math.Max(0.01, minY*0.25)
			minY = math.Max(0, minY-step)
			maxY = maxY + step
		} else {
			maxY = minY + 1
		}
	}
	if maxX == minX {
		maxX = minX + 1
	}
	return minX, maxX, minY, maxY, true
}

func projectOnto(values []Pt, width, height, pad, minX, maxX, minY, maxY float64) []Pt {
	if len(values) == 0 {
		return nil
	}
	spanX := maxX - minX
	spanY := maxY - minY
	if spanX == 0 {
		spanX = 1
	}
	if spanY == 0 {
		spanY = 1
	}
	innerH := height - 2*pad
	if innerH < 1 {
		innerH = height
		pad = 0
	}
	out := make([]Pt, len(values))
	for i, p := range values {
		out[i] = Pt{
			X:   (p.X - minX) / spanX * width,
			Y:   pad + (maxY-p.Y)/spanY*innerH,
			Gap: p.Gap,
		}
	}
	return out
}

// YTicks returns up to four labels from high to low across the data range.
func YTicks(values []float64) []string {
	return yTicksN(values, 4)
}

func yTicksN(values []float64, count int) []string {
	if len(values) == 0 {
		return []string{"1", "0"}
	}
	if count < 2 {
		count = 2
	}
	minV, maxV := values[0], values[0]
	for _, v := range values {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if maxV == minV {
		if minV > 0 && minV < 1 {
			step := math.Max(0.01, minV*0.25)
			minV = math.Max(0, minV-step)
			maxV = maxV + step
		} else {
			maxV = minV + 1
		}
	}
	steps := count - 1
	out := make([]string, 0, count)
	for i := steps; i >= 0; i-- {
		out = append(out, trimNum(minV+(maxV-minV)*float64(i)/float64(steps)))
	}
	return out
}

func trimNum(v float64) string {
	av := math.Abs(v)
	if av < 0.0005 {
		return "0"
	}
	if av < 1 {
		s := strconv.FormatFloat(v, 'f', 3, 64)
		return strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if av >= 100 || v == math.Trunc(v) {
		return strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}
