package store

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"time"
)

// CommandRecord is one device write.
type CommandRecord struct {
	TS     int64  `json:"ts"`
	Target string `json:"target"`
	Detail string `json:"detail"`
	Result string `json:"result"`
	Code   int    `json:"code"`
	MS     int64  `json:"ms"`
}

// EventRecord is one non-command event.
type EventRecord struct {
	TS     int64  `json:"ts"`
	Kind   string `json:"kind"`
	Target string `json:"target,omitempty"`
	Detail string `json:"detail"`
}

// Activity is a merged row for the activity page.
type Activity struct {
	TS     time.Time
	Kind   string
	Target string
	Detail string
	Result string
	MS     int64
	Code   int
}

// AppendCommand writes commands.jsonl.
func (s *Store) AppendCommand(r CommandRecord) error {
	if r.TS == 0 {
		r.TS = time.Now().UTC().Unix()
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	return appendJSON(s.path("commands.jsonl"), r)
}

// AppendEvent writes events.jsonl.
func (s *Store) AppendEvent(r EventRecord) error {
	if r.TS == 0 {
		r.TS = time.Now().UTC().Unix()
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	return appendJSON(s.path("events.jsonl"), r)
}

// ActivitySince merges commands and events at or after since.
func (s *Store) ActivitySince(since time.Time) []Activity {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	cut := since.UTC().Unix()
	var out []Activity
	_ = readJSONL(s.path("commands.jsonl"), func(line []byte) {
		var r CommandRecord
		if json.Unmarshal(line, &r) != nil || r.TS < cut {
			return
		}
		out = append(out, Activity{TS: time.Unix(r.TS, 0).UTC(), Kind: "command", Target: r.Target, Detail: r.Detail, Result: r.Result, MS: r.MS, Code: r.Code})
	})
	_ = readJSONL(s.path("events.jsonl"), func(line []byte) {
		var r EventRecord
		if json.Unmarshal(line, &r) != nil || r.TS < cut {
			return
		}
		out = append(out, Activity{TS: time.Unix(r.TS, 0).UTC(), Kind: r.Kind, Target: r.Target, Detail: r.Detail})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].TS.After(out[j].TS) })
	return out
}

func appendJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return appendFile(path, b)
}

func readJSONL(path string, fn func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		fn(append([]byte(nil), sc.Bytes()...))
	}
	return sc.Err()
}
