// Cloud plan readers adapted from Magpie (MIT), pinned in THIRD_PARTY_NOTICES.
package quotasource

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func volcReadCodingPlan(result json.RawMessage) []QuotaWindow {
	var r struct {
		QuotaUsage []struct {
			Level          string `json:"Level"`
			Percent        any    `json:"Percent"`
			ResetTimestamp any    `json:"ResetTimestamp"`
		} `json:"QuotaUsage"`
	}
	if json.Unmarshal(result, &r) != nil {
		return nil
	}
	var out []QuotaWindow
	for _, u := range r.QuotaUsage {
		w, ok := volcWindow(u.Level)
		if !ok {
			continue
		}
		used, ok := volcNum(u.Percent)
		if !ok {
			continue
		}
		w.Used, w.ResetsAt = used, volcTime(u.ResetTimestamp)
		out = append(out, w)
	}
	return out
}

func volcReadAFP(result json.RawMessage) ([]QuotaWindow, string) {
	type bucket struct {
		Used, Quota, Total, Percent, ResetTime, ResetTimestamp any
	}
	var r struct {
		Tier        string  `json:"Tier"`
		AFPFiveHour *bucket `json:"AFPFiveHour"`
		AFPWeekly   *bucket `json:"AFPWeekly"`
		AFPMonthly  *bucket `json:"AFPMonthly"`
	}
	if json.Unmarshal(result, &r) != nil {
		return nil, ""
	}
	var out []QuotaWindow
	for _, b := range []struct {
		level string
		b     *bucket
	}{{"session", r.AFPFiveHour}, {"weekly", r.AFPWeekly}, {"monthly", r.AFPMonthly}} {
		if b.b == nil {
			continue
		}
		w, _ := volcWindow(b.level)
		used, ok := volcNum(b.b.Percent)
		if !ok {
			n, nok := volcNum(b.b.Used)
			of, ook := volcNum(b.b.Quota)
			if !ook {
				of, ook = volcNum(b.b.Total)
			}
			if !nok || !ook || of <= 0 {
				continue // no quota: not on this plan
			}
			used = n / of * 100
			w.Amount, w.Limit = n, of
		}
		w.Used = used
		if w.ResetsAt = volcTime(b.b.ResetTime); w.ResetsAt == nil {
			w.ResetsAt = volcTime(b.b.ResetTimestamp)
		}
		out = append(out, w)
	}
	return out, strings.TrimSpace(r.Tier)
}

func volcWindow(level string) (QuotaWindow, bool) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "session":
		return QuotaWindow{Name: "5 hours", Span: 5 * time.Hour}, true
	case "weekly":
		return QuotaWindow{Name: "Weekly", Span: 7 * 24 * time.Hour}, true
	case "monthly":
		return QuotaWindow{Name: "Month", Span: 30 * 24 * time.Hour}, true
	}
	return QuotaWindow{}, false
}

func volcNum(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

func volcTime(v any) *time.Time {
	if s, ok := v.(string); ok {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(s)); err == nil {
			return &t
		}
	}
	n, ok := volcNum(v)
	if !ok || n <= 0 {
		return nil
	}
	// 1e11 seconds is the year 5138, 1e11 ms 1973: no reset is on the
	// wrong side of it
	var t time.Time
	if n > 1e11 {
		t = time.UnixMilli(int64(n))
	} else {
		t = time.Unix(int64(n), 0)
	}
	return &t
}

type tencentPlan struct {
	TeamID      string `json:"TeamId"`
	Name        string `json:"Name"`
	Status      string `json:"Status"`
	StopReason  string `json:"StopReason"`
	ProductType string `json:"ProductType"`
	PackageInfo *struct {
		TotalQuota   any    `json:"TotalQuota"`
		TotalUsed    any    `json:"TotalUsed"`
		CycleQuota   any    `json:"CycleQuota"`
		CycleUnit    string `json:"CycleUnit"`
		CurrentCycle any    `json:"CurrentCycle"`
		StartTime    string `json:"StartTime"`
		ExpireTime   string `json:"ExpireTime"`
	} `json:"PackageInfo"`
	TokenSummary *struct {
		CycleStartTime string `json:"CycleStartTime"`
		CycleEndTime   string `json:"CycleEndTime"`
	} `json:"TokenSummary"`
}

// window is the plan's month: what it used of its cycle's quota, in
// credits (专业套餐, ProductType enterprise) or tokens (轻享套餐,
// enterprise-auto), and when the cycle ends; ok false when no quota is
// told.
func (t tencentPlan) window(now time.Time) (QuotaWindow, bool) {
	pk := t.PackageInfo
	if pk == nil {
		return QuotaWindow{}, false
	}
	limit, ok := volcNum(pk.CycleQuota)
	if !ok || limit <= 0 {
		limit, ok = volcNum(pk.TotalQuota)
	}
	used, uok := volcNum(pk.TotalUsed)
	if !ok || !uok || limit <= 0 {
		return QuotaWindow{}, false
	}
	w := QuotaWindow{Name: "Month", Span: 30 * 24 * time.Hour, Amount: used, Limit: limit, Unit: "credits",
		Used: max(0, min(100, used/limit*100))}
	if strings.EqualFold(t.ProductType, "enterprise-auto") {
		w.Unit = "tokens"
	}
	if t.TokenSummary != nil {
		if end, err := time.Parse(time.RFC3339, strings.TrimSpace(t.TokenSummary.CycleEndTime)); err == nil {
			w.ResetsAt = &end
		}
		if start, err := time.Parse(time.RFC3339, strings.TrimSpace(t.TokenSummary.CycleStartTime)); err == nil && w.ResetsAt != nil && w.ResetsAt.After(start) {
			w.Span = w.ResetsAt.Sub(start)
		}
	}
	if w.ResetsAt == nil {
		// the package's start and the cycle it is in: the cycle ends that
		// many months on, and never after the package does
		start, err := time.Parse(time.RFC3339, strings.TrimSpace(pk.StartTime))
		n, _ := volcNum(pk.CurrentCycle)
		if err == nil && (pk.CycleUnit == "" || strings.EqualFold(pk.CycleUnit, "month")) {
			end := start.AddDate(0, max(int(n), 1), 0)
			for !end.After(now) && n < 1200 {
				n++
				end = start.AddDate(0, int(n), 0)
			}
			if exp, err := time.Parse(time.RFC3339, strings.TrimSpace(pk.ExpireTime)); err == nil && exp.Before(end) {
				end = exp
			}
			w.ResetsAt = &end
		}
	}
	return w, true
}

var aliWindowField = regexp.MustCompile(`^per(\d+)(Hour|Day|Week|Month)(Percentage|ResetTime)$`)

func aliReadWindows(raw []byte) []QuotaWindow {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	type win struct {
		n    int
		unit string
		used *float64
		at   any
	}
	wins := map[string]*win{}
	for k, v := range m {
		g := aliWindowField.FindStringSubmatch(k)
		if g == nil {
			continue
		}
		n, _ := strconv.Atoi(g[1])
		key := g[1] + g[2]
		w := wins[key]
		if w == nil {
			w = &win{n: n, unit: g[2]}
			wins[key] = w
		}
		if g[3] == "ResetTime" {
			w.at = v
		} else if f, ok := volcNum(v); ok {
			w.used = &f
		}
	}
	hours := map[string]time.Duration{"Hour": time.Hour, "Day": 24 * time.Hour, "Week": 7 * 24 * time.Hour, "Month": 30 * 24 * time.Hour}
	var all []*win
	for _, w := range wins {
		if w.used != nil {
			all = append(all, w)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		return time.Duration(all[i].n)*hours[all[i].unit] < time.Duration(all[j].n)*hours[all[j].unit]
	})
	var out []QuotaWindow
	for _, w := range all {
		q := QuotaWindow{Used: *w.used * 100, Span: time.Duration(w.n) * hours[w.unit], ResetsAt: volcTime(w.at)}
		switch {
		case w.n == 5 && w.unit == "Hour":
			q.Name = "5 hours"
		case w.n == 1 && w.unit == "Week":
			q.Name = "Weekly"
		case w.n == 1 && w.unit == "Month":
			q.Name = "Month"
		case w.n == 1:
			q.Name = map[string]string{"Hour": "Hour", "Day": "Daily"}[w.unit]
		}
		if q.Name == "" {
			q.Name = fmt.Sprintf("%d %ss", w.n, strings.ToLower(w.unit))
		}
		out = append(out, q)
	}
	return out
}

func stepfunRate(v any) (float64, bool) {
	f, ok := number(v)
	if !ok {
		return 0, false
	}
	if f > 1 {
		f /= 100
	}
	return max(0, min(1, f)), true
}

func stepfunTime(v any) (time.Time, bool) {
	f, ok := number(v)
	if !ok || f <= 0 {
		return time.Time{}, false
	}
	if f > 1e12 {
		return time.UnixMilli(int64(f)), true
	}
	return time.Unix(int64(f), 0), true
}

func readStepPlan(b []byte) ([]QuotaWindow, error) {
	var r struct {
		FiveLeft    any `json:"five_hour_usage_left_rate"`
		FiveReset   any `json:"five_hour_usage_reset_time"`
		WeekLeft    any `json:"weekly_usage_left_rate"`
		WeekReset   any `json:"weekly_usage_reset_time"`
		CreditLimit struct {
			Left    any               `json:"subscription_credit_left_rate"`
			Reset   any               `json:"subscription_credit_reset_time"`
			Buckets []json.RawMessage `json:"credit_buckets"`
		} `json:"plan_credit_rate_limit"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	var out []QuotaWindow
	add := func(name string, span time.Duration, left, reset any, resetless bool) {
		at, ok := stepfunTime(reset)
		if !ok && !resetless {
			return
		}
		l, lok := stepfunRate(left)
		if !lok {
			return
		}
		w := QuotaWindow{Name: name, Span: span, Used: (1 - l) * 100}
		if ok {
			w.ResetsAt = &at
		}
		out = append(out, w)
	}
	add("5 hours", 5*time.Hour, r.FiveLeft, r.FiveReset, false)
	add("7 days", 7*24*time.Hour, r.WeekLeft, r.WeekReset, false)
	add("Credits", 0, r.CreditLimit.Left, r.CreditLimit.Reset, len(r.CreditLimit.Buckets) > 0)
	return out, nil
}
