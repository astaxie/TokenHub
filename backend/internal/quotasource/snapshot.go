package quotasource

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"tokenhub/backend/internal/allowance"
)

type Result struct {
	Allowance allowance.Snapshot `json:"allowance"`
	Parts     []BalancePart      `json:"balance_parts,omitempty"`
}

// Parse applies public collection rules to a bounded response. Each configured
// mandatory window remains present when unreadable so routing cannot infer spare
// capacity from another, successfully parsed balance.
func Parse(body []byte, c Config, now time.Time) (Result, error) {
	if len(body) > MaxResponseBytes {
		return Result{}, errors.New("quota response exceeds size limit")
	}
	if err := c.Validate(); err != nil {
		return Result{}, err
	}
	s := allowance.Snapshot{Version: allowance.Version, FetchedAt: now.UTC(), CheckedAt: now.UTC(), Source: c.Preset}
	out := Result{Allowance: s}
	if c.PlanPath != "" {
		v, _ := ReadValue(body, c.PlanPath)
		out.Allowance.Plan, _ = v.(string)
	}
	out.Allowance.ExpiresAt = readTime(body, c.ExpiresPath)
	if c.Expression != "" {
		parts, err := ReadParts(body, c.Expression)
		if err != nil {
			return out, err
		}
		out.Parts = parts
		for i, p := range parts {
			w := allowance.Window{Name: p.Label, Unit: p.Unit, Unknown: p.Error != ""}
			if w.Name == "" {
				w.Name = fmt.Sprintf("Balance %d", i+1)
			}
			if p.Percent != nil {
				w.UsedPercent = p.Percent
			} else if p.Value != nil {
				w.Balance = p.Value
				if w.Unit == "" {
					w.Unit = c.Unit
				}
			} else {
				w.Informational = p.Error == ""
			}
			out.Allowance.Windows = append(out.Allowance.Windows, w)
		}
	}
	for _, r := range c.Windows {
		read := func(expr string) *float64 {
			if expr == "" {
				return nil
			}
			v, _ := ReadNumber(body, expr)
			return v
		}
		w := allowance.Window{Name: r.Name, UsedPercent: read(r.Used), Amount: read(r.Amount), Limit: read(r.Limit), Balance: read(r.Balance), Unit: r.Unit, Period: r.Period, PeriodSeconds: r.PeriodSeconds, ResetAt: readTime(body, r.ResetPath), ExpiresAt: readTime(body, r.ExpiresPath), Models: r.Models, ExcludedModels: r.ExcludedModels, Pool: r.Pool, Informational: r.Informational, Credits: r.Credits}
		if w.UsedPercent == nil && w.Amount != nil && w.Limit != nil && *w.Limit > 0 {
			used := *w.Amount / *w.Limit * 100
			w.UsedPercent = &used
		}
		w.Unknown = w.UsedPercent == nil && w.Balance == nil
		if r.Optional && w.Unknown {
			continue
		}
		out.Allowance.Windows = append(out.Allowance.Windows, w)
	}
	if len(out.Allowance.Windows) == 0 {
		return out, errors.New("quota response contains no readable allowance")
	}
	out.Allowance.Status = out.Allowance.For("", now, 0).Status
	return out, nil
}

func readTime(body []byte, path string) *time.Time {
	if path == "" {
		return nil
	}
	value, err := ReadValue(body, path)
	if err != nil {
		return nil
	}
	return Time(value)
}

// Time accepts RFC3339 and epoch seconds/milliseconds, without inventing resets.
func Time(value any) *time.Time {
	if s, ok := value.(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	var n float64
	switch v := value.(type) {
	case float64:
		n = v
	case string:
		n, _ = strconv.ParseFloat(strings.TrimSpace(v), 64)
	}
	if n > 1e11 {
		n /= 1000
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 || n > 253402300799 {
		return nil
	}
	t := time.Unix(int64(n), 0).UTC()
	return &t
}
