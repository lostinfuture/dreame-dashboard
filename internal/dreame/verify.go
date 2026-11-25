package dreame

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SweepRefs is siid 1..14 by piid 1..30.
func SweepRefs() []PropRef {
	refs := make([]PropRef, 0, 14*30)
	for s := 1; s <= 14; s++ {
		for p := 1; p <= 30; p++ {
			refs = append(refs, PropRef{SIID: s, PIID: p})
		}
	}
	return refs
}

// VerifyMap reads every sweep property and builds a read-sweep profile.
// Per-batch code=80001 timeouts are skipped while any baseline properties inside
// that chunk are probed individually so one hanging pair cannot block the sweep.
func VerifyMap(ctx context.Context, api interface {
	GetProperties(context.Context, string, []PropRef) ([]PropResult, error)
}, did string) (Profile, string, error) {
	refs := SweepRefs()
	by := map[string]PropResult{}
	var timedOut []string
	var lastErr error
	for i := 0; i < len(refs); i += 10 {
		if err := ctx.Err(); err != nil {
			return Profile{}, "", err
		}
		end := i + 10
		if end > len(refs) {
			end = len(refs)
		}
		chunk := refs[i:end]
		rows, err := api.GetProperties(ctx, did, chunk)
		if err == nil {
			for _, row := range rows {
				by[fmt.Sprintf("%d.%d", row.SIID, row.PIID)] = row
			}
			continue
		}
		code, isAPI := CodeOf(err)
		if !isAPI || code != 80001 {
			return Profile{}, "", err
		}
		lastErr = err
		probedBaseline := false
		for _, r := range chunk {
			if _, known := baseline[[2]int{r.SIID, r.PIID}]; !known {
				continue
			}
			probedBaseline = true
			if cerr := ctx.Err(); cerr != nil {
				return Profile{}, "", cerr
			}
			var one []PropResult
			var oerr error
			for attempt := 0; attempt < 2; attempt++ {
				one, oerr = api.GetProperties(ctx, did, []PropRef{r})
				if oerr == nil {
					break
				}
				ocode, oAPI := CodeOf(oerr)
				if !oAPI || ocode != 80001 {
					return Profile{}, "", oerr
				}
			}
			if oerr == nil {
				for _, row := range one {
					by[fmt.Sprintf("%d.%d", row.SIID, row.PIID)] = row
				}
				continue
			}
			lastErr = oerr
			timedOut = append(timedOut, r.Key())
		}
		if !probedBaseline {
			timedOut = append(timedOut, fmt.Sprintf("%s-%s", chunk[0].Key(), chunk[len(chunk)-1].Key()))
		}
	}
	p := Profile{DID: did, VerifiedBy: "read-sweep", UpdatedAt: time.Now().UTC()}
	var extra, missing []string
	seen := map[string]bool{}
	for _, ref := range refs {
		key := ref.Key()
		row, ok := by[key]
		realized := ok && row.Code != -4003 && (row.Code == 0 || row.Value != nil && !row.Has)
		if ok && row.Code == -4003 {
			realized = false
		}
		if !realized {
			continue
		}
		name, access := specFor(ref.SIID, ref.PIID)
		p.Properties = append(p.Properties, PropSpec{
			SIID: ref.SIID, PIID: ref.PIID, Key: name, JSONType: jsonType(row.Value),
			Access: access, Realized: true, Value: row.Value,
		})
		seen[key] = true
		if _, known := baseline[[2]int{ref.SIID, ref.PIID}]; !known {
			extra = append(extra, key+" "+name)
		}
	}
	if len(p.Properties) == 0 && lastErr != nil {
		return Profile{}, "", lastErr
	}
	for id, meta := range baseline {
		key := fmt.Sprintf("%d.%d", id[0], id[1])
		if !seen[key] {
			missing = append(missing, key+" "+meta.Key)
		}
	}
	sort.Strings(extra)
	sort.Strings(missing)
	sort.Strings(timedOut)
	var b strings.Builder
	fmt.Fprintf(&b, "realized=%d\n", len(p.Properties))
	for _, s := range p.Properties {
		fmt.Fprintf(&b, "%d.%d key=%s type=%s\n", s.SIID, s.PIID, s.Key, s.JSONType)
	}
	if len(missing) > 0 {
		fmt.Fprintf(&b, "missing=%s\n", strings.Join(missing, ","))
	}
	if len(extra) > 0 {
		fmt.Fprintf(&b, "extra=%s\n", strings.Join(extra, ","))
	}
	if len(timedOut) > 0 {
		fmt.Fprintf(&b, "timeout=%s\n", strings.Join(timedOut, ","))
	}
	return p, b.String(), nil
}
