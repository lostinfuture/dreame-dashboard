package store

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Reading is one compact sensor snapshot.
type Reading struct {
	TS   int64    `json:"ts"`
	Pwr  *int     `json:"pwr,omitempty"`
	Mode *int     `json:"mode,omitempty"`
	Fan  *int     `json:"fan,omitempty"`
	AQ   *float64 `json:"aq,omitempty"`
	AQL  *int     `json:"aql,omitempty"`
	PM1  *float64 `json:"pm1,omitempty"`
	PM25 *float64 `json:"pm25,omitempty"`
	PM10 *float64 `json:"pm10,omitempty"`
	T    *float64 `json:"t,omitempty"`
	RH   *float64 `json:"rh,omitempty"`
	HCHO *float64 `json:"hcho,omitempty"`
	TVOC *float64 `json:"tvoc,omitempty"`
	HEPA *float64 `json:"hepa,omitempty"`
}

// Point is one chart sample. Gap means the line must break before this point.
type Point struct {
	TS    time.Time
	V     float64
	Min   float64
	Max   float64
	Range bool
	Gap   bool
}

// AppendReading writes one JSONL snapshot for the UTC day of the timestamp.
func (s *Store) AppendReading(r Reading) error {
	if r.TS == 0 {
		return nil
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	day := time.Unix(r.TS, 0).UTC().Format("2006-01-02")
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	return appendFile(filepath.Join(s.root, "readings", day+".jsonl"), b)
}

// History returns one series for a metric over 6h, 24h, 7d, or 30d.
func (s *Store) History(now time.Time, span, metric string, rawGap time.Duration) []Point {
	return s.HistoryMulti(now, span, []string{metric}, rawGap)[metric]
}

// HistoryMulti reads multiple series over 6h, 24h, 7d, or 30d in a single disk scan.
func (s *Store) HistoryMulti(now time.Time, span string, metrics []string, rawGap time.Duration) map[string][]Point {
	out := make(map[string][]Point, len(metrics))
	if len(metrics) == 0 {
		return out
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	now = now.UTC()
	window := spanDuration(span)
	from := now.Add(-window)
	if span == "7d" || span == "30d" {
		return s.rollupSeriesMulti(from, now, metrics)
	}
	rows := s.readingsBetween(from, now)
	by := make(map[string][]Point, len(metrics))
	for _, r := range rows {
		ts := time.Unix(r.TS, 0).UTC()
		for _, m := range metrics {
			if v, ok := metricOf(r, m); ok {
				by[m] = append(by[m], Point{TS: ts, V: v, Min: v, Max: v})
			}
		}
	}
	for _, m := range metrics {
		pts := by[m]
		sort.Slice(pts, func(i, j int) bool { return pts[i].TS.Before(pts[j].TS) })
		out[m] = downsample5m(pts, rawGap)
	}
	return out
}

func spanDuration(span string) time.Duration {
	switch span {
	case "24h":
		return 24 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	default:
		return 6 * time.Hour
	}
}

func (s *Store) readingsBetween(from, to time.Time) []Reading {
	var out []Reading
	startDay := from.UTC().Truncate(24 * time.Hour)
	endDay := to.UTC().Truncate(24 * time.Hour)
	for d := startDay; !d.After(endDay); d = d.Add(24 * time.Hour) {
		f, err := os.Open(filepath.Join(s.root, "readings", d.Format("2006-01-02")+".jsonl"))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			var r Reading
			if json.Unmarshal(sc.Bytes(), &r) != nil || r.TS == 0 {
				continue
			}
			ts := time.Unix(r.TS, 0).UTC()
			if ts.Before(from) || ts.After(to) {
				continue
			}
			out = append(out, r)
		}
		f.Close()
	}
	return out
}

func standbySensorOff(r Reading, metric string) bool {
	switch metric {
	case "pm25", "pm10", "pm1", "t", "rh", "hcho":
		if r.Pwr != nil && *r.Pwr == 2 {
			return true
		}
		// Immediately after waking from standby, the sensor module reports 0 C and 0% RH
		// for one poll while warming up.
		if r.T != nil && r.RH != nil && *r.T == 0 && *r.RH == 0 {
			return true
		}
	}
	return false
}

func metricOf(r Reading, metric string) (float64, bool) {
	if standbySensorOff(r, metric) {
		return 0, false
	}
	switch metric {
	case "pm25":
		return ptr(r.PM25)
	case "pm10":
		return ptr(r.PM10)
	case "pm1":
		return ptr(r.PM1)
	case "t", "temp":
		return ptr(r.T)
	case "rh", "humidity":
		return ptr(r.RH)
	case "hcho":
		return ptr(r.HCHO)
	case "tvoc":
		return ptr(r.TVOC)
	case "aq":
		return ptr(r.AQ)
	case "hepa":
		return ptr(r.HEPA)
	default:
		return 0, false
	}
}

func ptr(p *float64) (float64, bool) {
	if p == nil {
		return 0, false
	}
	return *p, true
}

const chartBucket = 5 * time.Minute

func downsample5m(points []Point, gap time.Duration) []Point {
	if len(points) == 0 {
		return nil
	}
	runs := splitRuns(points, gap)
	var out []Point
	for i, run := range runs {
		part := bucketByDuration(run, chartBucket)
		if i > 0 && len(part) > 0 {
			part[0].Gap = true
		}
		out = append(out, part...)
	}
	return out
}

func bucketByDuration(points []Point, step time.Duration) []Point {
	if len(points) <= 1 || step <= 0 {
		out := append([]Point(nil), points...)
		for i := range out {
			out[i].Gap = false
		}
		return out
	}
	if points[len(points)-1].TS.Sub(points[0].TS) < step {
		out := append([]Point(nil), points...)
		for i := range out {
			out[i].Gap = false
		}
		return out
	}
	var out []Point
	var acc []Point
	curSlot := points[0].TS.UTC().Truncate(step)
	flush := func() {
		if len(acc) == 0 {
			return
		}
		out = append(out, averagePoints(acc))
		acc = nil
	}
	for _, p := range points {
		slot := p.TS.UTC().Truncate(step)
		if len(acc) > 0 && !slot.Equal(curSlot) {
			flush()
		}
		curSlot = slot
		acc = append(acc, p)
	}
	flush()
	for i := range out {
		out[i].Gap = false
	}
	return out
}

func splitRuns(points []Point, gap time.Duration) [][]Point {
	var runs [][]Point
	cur := []Point{points[0]}
	for i := 1; i < len(points); i++ {
		if gap > 0 && points[i].TS.Sub(points[i-1].TS) > gap {
			runs = append(runs, cur)
			cur = []Point{points[i]}
			continue
		}
		cur = append(cur, points[i])
	}
	return append(runs, cur)
}

func averagePoints(acc []Point) Point {
	p := acc[len(acc)-1]
	sum := 0.0
	minV := acc[0].V
	maxV := acc[0].V
	if acc[0].Range {
		minV, maxV = acc[0].Min, acc[0].Max
	}
	for _, a := range acc {
		sum += a.V
		lo, hi := a.V, a.V
		if a.Range {
			lo, hi = a.Min, a.Max
		}
		if lo < minV {
			minV = lo
		}
		if hi > maxV {
			maxV = hi
		}
	}
	p.V = sum / float64(len(acc))
	p.Min = minV
	p.Max = maxV
	p.Range = minV != maxV || acc[0].Range
	p.Gap = false
	return p
}

func markGaps(in []Point, gap time.Duration) []Point {
	if len(in) == 0 {
		return nil
	}
	out := make([]Point, len(in))
	copy(out, in)
	out[0].Gap = false
	for i := 1; i < len(out); i++ {
		if gap > 0 && out[i].TS.Sub(out[i-1].TS) > gap {
			out[i].Gap = true
		}
	}
	return out
}

func appendFile(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		return err
	}
	return f.Sync()
}
