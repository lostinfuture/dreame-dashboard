package store

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// Triple is min, avg, max.
type Triple [3]float64

// Rollup is one completed UTC hour.
type Rollup struct {
	Hour string  `json:"hour"`
	N    int     `json:"n"`
	PM25 *Triple `json:"pm25,omitempty"`
	PM10 *Triple `json:"pm10,omitempty"`
	PM1  *Triple `json:"pm1,omitempty"`
	AQ   *Triple `json:"aq,omitempty"`
	T    *Triple `json:"t,omitempty"`
	RH   *Triple `json:"rh,omitempty"`
	HCHO *Triple `json:"hcho,omitempty"`
	TVOC *Triple `json:"tvoc,omitempty"`
	HEPA *Triple `json:"hepa,omitempty"`
}

// Backfill writes every completed UTC hour newer than the last rollup.
func (s *Store) Backfill(now time.Time) error {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	now = now.UTC()
	end := now.Truncate(time.Hour)
	existing, err := s.loadRollups()
	if err != nil {
		return err
	}
	var start time.Time
	var rows []Reading
	if len(existing) == 0 {
		day, ok := s.earliestReadingDay()
		if !ok {
			return nil
		}
		rows = s.readingsBetween(day, end)
		if len(rows) == 0 {
			return nil
		}
		start = time.Unix(rows[0].TS, 0).UTC().Truncate(time.Hour)
		for _, r := range rows {
			h := time.Unix(r.TS, 0).UTC().Truncate(time.Hour)
			if h.Before(start) {
				start = h
			}
		}
	} else {
		last, err := time.Parse(time.RFC3339, existing[len(existing)-1].Hour)
		if err != nil {
			return err
		}
		start = last.Add(time.Hour)
		rows = s.readingsBetween(start, end)
	}
	byHour := map[time.Time][]Reading{}
	for _, r := range rows {
		h := time.Unix(r.TS, 0).UTC().Truncate(time.Hour)
		byHour[h] = append(byHour[h], r)
	}
	var add []Rollup
	for h := start; h.Before(end); h = h.Add(time.Hour) {
		row := rollupHour(byHour[h], h)
		if row.N == 0 {
			continue
		}
		add = append(add, row)
	}
	if len(add) == 0 {
		return nil
	}
	return s.appendRollups(add)
}

// HourlySeries returns 1-hour min/avg/max buckets between from and to across the given metrics.
func (s *Store) HourlySeries(from, to time.Time, metrics []string) map[string][]Point {
	out := make(map[string][]Point, len(metrics))
	if len(metrics) == 0 || to.Before(from) {
		return out
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	fromUTC := from.UTC()
	toUTC := to.UTC()
	rows := s.readingsBetween(fromUTC, toUTC)
	if len(rows) == 0 {
		return out
	}
	byHour := map[time.Time][]Reading{}
	for _, r := range rows {
		h := time.Unix(r.TS, 0).UTC().Truncate(time.Hour)
		byHour[h] = append(byHour[h], r)
	}
	startHour := fromUTC.Truncate(time.Hour)
	endHour := toUTC.Truncate(time.Hour)
	for h := startHour; !h.After(endHour); h = h.Add(time.Hour) {
		bucket := byHour[h]
		if len(bucket) == 0 {
			continue
		}
		row := rollupHour(bucket, h)
		for _, m := range metrics {
			if p, ok := rollupPointAt(row, h, m); ok {
				out[m] = append(out[m], p)
			}
		}
	}
	for _, m := range metrics {
		out[m] = markGaps(out[m], 3*time.Hour)
	}
	return out
}

func (s *Store) rollupSeriesMulti(from, now time.Time, metrics []string) map[string][]Point {
	out := make(map[string][]Point, len(metrics))
	rows, _ := s.loadRollups()
	cur := now.UTC().Truncate(time.Hour)
	by := make(map[string][]Point, len(metrics))
	for _, row := range rows {
		h, err := time.Parse(time.RFC3339, row.Hour)
		if err != nil || h.Before(from) || !h.Before(cur) {
			continue
		}
		for _, metric := range metrics {
			if p, ok := rollupPointAt(row, h.UTC(), metric); ok {
				by[metric] = append(by[metric], p)
			}
		}
	}
	prov := rollupHour(s.readingsBetween(cur, now), cur)
	for _, metric := range metrics {
		if p, ok := rollupPointAt(prov, cur, metric); ok {
			by[metric] = append(by[metric], p)
		}
		out[metric] = markGaps(by[metric], 3*time.Hour)
	}
	return out
}

func rollupPointAt(row Rollup, ts time.Time, metric string) (Point, bool) {
	tr := tripleOf(row, metric)
	if tr == nil || row.N == 0 {
		return Point{}, false
	}
	return Point{TS: ts, V: tr[1], Min: tr[0], Max: tr[2], Range: true}, true
}

func tripleOf(row Rollup, metric string) *Triple {
	switch metric {
	case "pm25":
		return row.PM25
	case "pm10":
		return row.PM10
	case "pm1":
		return row.PM1
	case "t", "temp":
		return row.T
	case "rh", "humidity":
		return row.RH
	case "hcho":
		return row.HCHO
	case "tvoc":
		return row.TVOC
	case "aq":
		return row.AQ
	case "hepa":
		return row.HEPA
	default:
		return nil
	}
}

func rollupHour(rows []Reading, hour time.Time) Rollup {
	r := Rollup{Hour: hour.UTC().Format(time.RFC3339), N: len(rows)}
	if len(rows) == 0 {
		return r
	}
	r.PM25 = stat(rows, "pm25")
	r.PM10 = stat(rows, "pm10")
	r.PM1 = stat(rows, "pm1")
	r.AQ = stat(rows, "aq")
	r.T = stat(rows, "t")
	r.RH = stat(rows, "rh")
	r.HCHO = stat(rows, "hcho")
	r.TVOC = stat(rows, "tvoc")
	r.HEPA = stat(rows, "hepa")
	return r
}

func stat(rows []Reading, metric string) *Triple {
	var vals []float64
	for _, r := range rows {
		if v, ok := metricOf(r, metric); ok {
			vals = append(vals, v)
		}
	}
	if len(vals) == 0 {
		return nil
	}
	minV, maxV, sum := vals[0], vals[0], 0.0
	for _, v := range vals {
		sum += v
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	tr := Triple{minV, sum / float64(len(vals)), maxV}
	return &tr
}

func (s *Store) earliestReadingDay() (time.Time, bool) {
	entries, err := os.ReadDir(s.path("readings"))
	if err != nil {
		return time.Time{}, false
	}
	var earliest time.Time
	for _, e := range entries {
		day, err := time.Parse("2006-01-02", strings.TrimSuffix(e.Name(), ".jsonl"))
		if err != nil {
			continue
		}
		if earliest.IsZero() || day.Before(earliest) {
			earliest = day
		}
	}
	return earliest, !earliest.IsZero()
}

func (s *Store) loadRollups() ([]Rollup, error) {
	f, err := os.Open(s.path("rollups.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Rollup
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var r Rollup
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.Hour != "" {
			out = append(out, r)
		}
	}
	return out, sc.Err()
}

func (s *Store) appendRollups(rows []Rollup) error {
	var b []byte
	for _, r := range rows {
		line, err := json.Marshal(r)
		if err != nil {
			return err
		}
		b = append(b, line...)
		b = append(b, '\n')
	}
	return appendFile(s.path("rollups.jsonl"), b)
}

// Retain drops old readings, rollups, and logs.
func (s *Store) Retain(now time.Time, rollupDays int) error {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	now = now.UTC()
	if err := s.retainReadings(now); err != nil {
		return err
	}
	if err := s.retainRollups(now, rollupDays); err != nil {
		return err
	}
	cut := now.AddDate(0, 0, -180).Unix()
	if err := s.retainJSONL(s.path("commands.jsonl"), cut); err != nil {
		return err
	}
	return s.retainJSONL(s.path("events.jsonl"), cut)
}

func (s *Store) retainReadings(now time.Time) error {
	dir := s.path("readings")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	today := now.Truncate(24 * time.Hour)
	oldestCal := today.AddDate(0, 0, -2)
	for _, e := range entries {
		name := e.Name()
		if len(name) != len("2006-01-02.jsonl") || name[len(name)-6:] != ".jsonl" {
			continue
		}
		day, err := time.Parse("2006-01-02", name[:10])
		if err != nil {
			continue
		}
		ends := day.Add(24 * time.Hour)
		if day.Before(oldestCal) && !ends.After(now.Add(-48*time.Hour)) {
			if err := os.Remove(s.path("readings/" + name)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func (s *Store) retainRollups(now time.Time, days int) error {
	if days < 1 {
		days = 90
	}
	rows, err := s.loadRollups()
	if err != nil {
		return err
	}
	cut := now.AddDate(0, 0, -days)
	var keep []Rollup
	changed := false
	for _, r := range rows {
		h, err := time.Parse(time.RFC3339, r.Hour)
		if err != nil || !h.Before(cut) {
			keep = append(keep, r)
			continue
		}
		changed = true
	}
	if !changed {
		return nil
	}
	var b []byte
	for _, r := range keep {
		line, err := json.Marshal(r)
		if err != nil {
			return err
		}
		b = append(b, line...)
		b = append(b, '\n')
	}
	return writeAtomic(s.path("rollups.jsonl"), b)
}

func (s *Store) retainJSONL(path string, cutUnix int64) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	var keep [][]byte
	changed := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		var probe struct {
			TS int64 `json:"ts"`
		}
		if json.Unmarshal(line, &probe) == nil && probe.TS > 0 && probe.TS < cutUnix {
			changed = true
			continue
		}
		keep = append(keep, line)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if !changed {
		return nil
	}
	var b []byte
	for _, line := range keep {
		b = append(b, line...)
		b = append(b, '\n')
	}
	return writeAtomic(path, b)
}
