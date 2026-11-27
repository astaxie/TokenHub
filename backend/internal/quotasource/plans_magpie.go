// Plan response readers adapted from Magpie (MIT), pinned in THIRD_PARTY_NOTICES.
package quotasource

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

type QuotaWindow struct {
	Name                string
	Used, Amount, Limit float64
	Balance             *float64
	Unit                string
	Span                time.Duration
	ResetsAt            *time.Time
	Aside, Credits      bool
}

func camel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func readOpenCodeGo(b []byte) (string, []QuotaWindow, error) {
	type window struct {
		Percent  *float64 `json:"percent"`
		ResetsAt string   `json:"resetsAt"`
	}
	var r struct {
		Usage map[string]window `json:"usage"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", nil, err
	}
	out := []QuotaWindow{}
	for _, x := range []struct {
		key, name string
		span      time.Duration
	}{{"rolling", "5 hours", 5 * time.Hour}, {"weekly", "7 days", 7 * 24 * time.Hour}, {"monthly", "Month", 0}} {
		w, ok := r.Usage[x.key]
		if !ok || w.Percent == nil {
			continue
		}
		q := QuotaWindow{Name: x.name, Used: *w.Percent, Span: x.span}
		if t, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil && *w.Percent > 0 {
			q.ResetsAt = &t
		}
		out = append(out, q)
	}
	if len(out) == 0 {
		return "", nil, fmt.Errorf("OpenCode Go's reply has no rolling, weekly or monthly window: %s", shapeOf(b))
	}
	return "", out, nil
}

func shapeOf(b []byte) string {
	var top map[string]json.RawMessage
	if json.Unmarshal(b, &top) != nil {
		if len(b) == 0 {
			return "an empty reply"
		}
		return "a reply that isn't a JSON object"
	}
	var names []string
	for k, v := range top {
		var in map[string]json.RawMessage
		if json.Unmarshal(v, &in) == nil && len(in) > 0 {
			for k2 := range in {
				names = append(names, k+"."+k2)
			}
			continue
		}
		names = append(names, k)
	}
	if len(names) == 0 {
		return "an empty object"
	}
	slices.Sort(names)
	if len(names) > 12 {
		names = append(names[:12], "…")
	}
	return "fields " + strings.Join(names, ", ")
}

func readMiniMaxPlan(b []byte) (string, []QuotaWindow, error) {
	type bucket struct {
		Model      string   `json:"model_name"`
		Start      int64    `json:"start_time"`
		End        int64    `json:"end_time"`
		Left       *float64 `json:"current_interval_remaining_percent"`
		Status     int      `json:"current_interval_status"`
		Total      *float64 `json:"current_interval_total_count"`
		Count      *float64 `json:"current_interval_usage_count"`
		WeekStart  int64    `json:"weekly_start_time"`
		WeekEnd    int64    `json:"weekly_end_time"`
		WeekLeft   *float64 `json:"current_weekly_remaining_percent"`
		WeekStatus int      `json:"current_weekly_status"`
		WeekTotal  *float64 `json:"current_weekly_total_count"`
		WeekCount  *float64 `json:"current_weekly_usage_count"`
	}
	var r struct {
		Remains []bucket `json:"model_remains"`
		Base    *struct {
			Code int    `json:"status_code"`
			Msg  string `json:"status_msg"`
		} `json:"base_resp"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", nil, err
	}
	if r.Base == nil {
		return "", nil, fmt.Errorf("no plan in the reply")
	}
	if r.Base.Code != 0 {
		if r.Base.Msg != "" {
			return "", nil, fmt.Errorf("%s", r.Base.Msg)
		}
		return "", nil, fmt.Errorf("MiniMax said %d", r.Base.Code)
	}
	at := func(n int64) time.Time {
		if n < 1e12 { // seconds
			return time.Unix(n, 0)
		}
		return time.UnixMilli(n)
	}
	zero := func(f *float64) bool { return f != nil && *f == 0 }
	out := []QuotaWindow{}
	for _, k := range r.Remains {
		name := strings.TrimSpace(k.Model)
		if name == "" || k.Status == 3 && k.WeekStatus == 3 && zero(k.Total) && zero(k.WeekTotal) {
			continue // not in the plan
		}
		general := strings.EqualFold(name, "general")
		for _, x := range []struct {
			left         *float64
			status       int
			start, end   int64
			week         bool
			count, total *float64
		}{{k.Left, k.Status, k.Start, k.End, false, k.Count, k.Total}, {k.WeekLeft, k.WeekStatus, k.WeekStart, k.WeekEnd, true, k.WeekCount, k.WeekTotal}} {
			if x.status == 3 || x.left == nil && x.status != 2 {
				continue // unlimited, or nothing told
			}
			w := QuotaWindow{Used: 100}
			if x.status != 2 {
				w.Used = max(0, min(100, 100-*x.left))
			}
			if x.start > 0 && x.end > x.start {
				w.Span = at(x.end).Sub(at(x.start))
			} else if x.week {
				w.Span = 7 * 24 * time.Hour
			}
			if x.end > 0 {
				t := at(x.end)
				w.ResetsAt = &t
			}
			switch span := w.Span; {
			case x.week:
				w.Name = "7 days"
			case span > 24*time.Hour && span%(24*time.Hour) == 0:
				w.Name = fmt.Sprintf("%d days", span/(24*time.Hour))
			case span >= time.Hour && span%time.Hour == 0:
				w.Name = fmt.Sprintf("%d hours", span/time.Hour)
			case span > 0:
				w.Name = fmt.Sprintf("%d minutes", span/time.Minute)
			default:
				w.Name = "Allowance"
			}
			if used, total, ok := miniMaxCount(x.count, x.total, x.left); ok {
				w.Amount, w.Limit = used, total
				if x.status == 2 {
					w.Amount = total
				}
				if strings.EqualFold(name, "video") {
					w.Unit = "videos"
				}
			}
			if !general {
				w.Name = strings.ToUpper(name[:1]) + name[1:] + " · " + w.Name
				w.Aside = true
			}
			out = append(out, w)
		}
	}
	return "", out, nil
}

func miniMaxCount(count, total, left *float64) (used, of float64, ok bool) {
	if count == nil || total == nil || left == nil || *total <= 0 || *count < 0 || *count > *total {
		return 0, 0, false
	}
	c, t := *count, *total
	asUsed := math.Abs((t-c)/t*100 - *left) // c is what is used: t-c remains
	asLeft := math.Abs(c/t*100 - *left)     // c is what remains
	switch {
	case min(asUsed, asLeft) > 1:
		return 0, 0, false
	case asUsed < asLeft:
		return c, t, true
	default:
		return t - c, t, true
	}
}

func readKimiCode(b []byte) (string, []QuotaWindow, error) {
	type row map[string]any
	var r struct {
		Usage   row            `json:"usage"`
		Usages  map[string]row `json:"usages"`
		Booster row            `json:"booster_wallet"`
		Camel   row            `json:"boosterWallet"`
		Limits  []struct {
			Window row `json:"window"`
			Detail row `json:"detail"`
		} `json:"limits"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", nil, err
	}
	num := func(v any) (float64, bool) {
		switch x := v.(type) {
		case float64:
			return x, true
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
			return f, err == nil
		}
		return 0, false
	}
	// a reset as RFC 3339 text or as epoch seconds or milliseconds, under
	// either spelling of its name
	resetOf := func(d row) *time.Time {
		for _, k := range []string{"resetTime", "resetAt", "reset_at", "reset_time"} {
			if s, _ := d[k].(string); s != "" {
				if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
					return &t
				}
			}
			if n, ok := num(d[k]); ok && n > 0 {
				t := time.Unix(int64(n), 0)
				if n > 1e11 {
					t = time.UnixMilli(int64(n))
				}
				return &t
			}
		}
		return nil
	}
	window := func(d row, name string, span time.Duration) (QuotaWindow, bool) {
		limit, ok := num(d["limit"])
		if !ok || limit <= 0 {
			return QuotaWindow{}, false
		}
		used, ok := num(d["used"])
		if !ok {
			left, ok := num(d["remaining"])
			if !ok {
				return QuotaWindow{}, false
			}
			used = limit - left
		}
		w := QuotaWindow{Name: name, Span: span, Used: max(0, min(100, used/limit*100))}
		w.ResetsAt = resetOf(d)
		return w, true
	}
	out := []QuotaWindow{}
	for _, l := range r.Limits {
		d := l.Detail
		if d == nil {
			d = l.Window
		}
		n, _ := num(l.Window["duration"])
		unit, _ := l.Window["timeUnit"].(string)
		var span time.Duration
		switch {
		case strings.Contains(unit, "MINUTE"):
			span = time.Duration(n) * time.Minute
		case strings.Contains(unit, "HOUR"):
			span = time.Duration(n) * time.Hour
		case strings.Contains(unit, "DAY"):
			span = time.Duration(n) * 24 * time.Hour
		case strings.Contains(unit, "WEEK"):
			span = time.Duration(n) * 7 * 24 * time.Hour
		case strings.Contains(unit, "MONTH"):
			span = time.Duration(n) * 30 * 24 * time.Hour
		}
		name := "Allowance"
		switch {
		case span == 30*24*time.Hour:
			name = "Month"
		case span > 24*time.Hour && span%(24*time.Hour) == 0:
			name = fmt.Sprintf("%d days", span/(24*time.Hour))
		case span >= time.Hour && span%time.Hour == 0:
			name = fmt.Sprintf("%d hours", span/time.Hour)
		case span > 0:
			name = fmt.Sprintf("%d minutes", span/time.Minute)
		}
		if w, ok := window(d, name, span); ok {
			out = append(out, w)
		}
	}
	if w, ok := window(r.Usage, "7 days", 7*24*time.Hour); ok {
		out = append(out, w)
	}
	shares := []QuotaWindow{}
	for _, k := range []struct {
		key, name string
		span      time.Duration
	}{{"limit_5h", "5 hours", 5 * time.Hour}, {"limit_7d", "7 days", 7 * 24 * time.Hour},
		{"limit_month_total", "Month", 30 * 24 * time.Hour}, {"limit_month_code", "Month", 30 * 24 * time.Hour}} {
		d := r.Usages[k.key]
		if d == nil {
			d = r.Usages[camel(k.key)]
		}
		ratio, ok := num(d["used_ratio"])
		if !ok {
			ratio, ok = num(d["usedRatio"])
		}
		if d == nil || !ok || math.IsNaN(ratio) || math.IsInf(ratio, 0) ||
			slices.ContainsFunc(shares, func(s QuotaWindow) bool { return s.Span == k.span }) {
			continue
		}
		shares = append(shares, QuotaWindow{Name: k.name, Span: k.span, Used: max(0, min(100, ratio*100)), ResetsAt: resetOf(d)})
	}
	if len(shares) > 0 {
		out = slices.DeleteFunc(out, func(w QuotaWindow) bool {
			return slices.ContainsFunc(shares, func(s QuotaWindow) bool { return s.Span == w.Span })
		})
		out = append(out, shares...)
		// shortest first, as the counted windows come; one of no known
		// length last
		span := func(w QuotaWindow) time.Duration {
			if w.Span == 0 {
				return math.MaxInt64
			}
			return w.Span
		}
		slices.SortStableFunc(out, func(a, b QuotaWindow) int {
			switch sa, sb := span(a), span(b); {
			case sa < sb:
				return -1
			case sa > sb:
				return 1
			}
			return 0
		})
	}
	if len(out) == 0 {
		return "", nil, fmt.Errorf("no usage in the reply")
	}
	wallet := r.Booster
	if len(wallet) == 0 {
		wallet = r.Camel
	}
	if left, unit, ok := kimiBooster(wallet, num); ok {
		if left > 0 {
			// Kimi goes on past the plan's windows on the booster (#5721):
			// one used up doesn't stop the key while it holds money
			for i := range out {
				out[i].Credits = true
			}
		}
		out = append(out, QuotaWindow{Name: "Extra usage", Balance: &left, Unit: unit, Aside: true})
	}
	return "", out, nil
}

func kimiBooster(wallet map[string]any, num func(any) (float64, bool)) (left float64, unit string, ok bool) {
	bal, _ := wallet["balance"].(map[string]any)
	if bal == nil || bal["type"] != "BOOSTER" {
		return 0, "", false
	}
	if amount, ok := num(bal["amount"]); !ok || amount <= 0 {
		return 0, "", false
	}
	if l, ok := num(bal["amountLeft"]); ok && l > 0 {
		left = math.Round(l/1e6) / 100
	}
	unit = "USD"
	for _, k := range []string{"monthlyChargeLimit", "monthlyUsed"} {
		if m, _ := wallet[k].(map[string]any); m != nil {
			if c, _ := m["currency"].(string); c != "" {
				unit = c
				break
			}
		}
	}
	return left, unit, true
}
