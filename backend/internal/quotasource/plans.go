package quotasource

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"tokenhub/backend/internal/allowance"
)

// ParsePlan reads vendor-specific plan shapes. Explicit custom rules override
// presets; raw account payloads never enter quota history.
func ParsePlan(body []byte, c Config, now time.Time) (Result, error) {
	if len(body) > MaxResponseBytes {
		return Result{}, errors.New("quota response exceeds size limit")
	}
	if c.Preset == "sub2api" && c.Expression == "$remaining" && len(c.Windows) == 0 {
		return parseSub2API(body, c, now)
	}
	if c.Preset == "deepseek" && len(c.Windows) == 0 && strings.Contains(c.Expression, "balance_infos") {
		return parseDeepSeek(body, c, now)
	}
	if c.Expression != "" || len(c.Windows) > 0 {
		return Parse(body, c, now)
	}
	var plan string
	var ws []QuotaWindow
	var err error
	switch c.Preset {
	case "opencode-go":
		plan, ws, err = readOpenCodeGo(body)
	case "minimax":
		plan, ws, err = readMiniMaxPlan(body)
	case "kimi-code":
		plan, ws, err = readKimiCode(body)
	case "volcengine-coding", "volcengine-agent", "tencent", "alibaba", "stepfun-plan":
		return parseCloud(body, c, now)
	case "glm":
		return parseGLM(body, c, now)
	case "ollama":
		return parseOllama(body, c, now)
	case "commandcode":
		return parseCommand(body, c, now)
	case "mistral":
		return Result{}, errors.New("the Mistral quota requires the key kind returned by whoami")
	default:
		return Parse(body, c, now)
	}
	if err != nil {
		return Result{}, errors.New("provider plan response is unavailable or invalid")
	}
	return planResult(plan, ws, c, now)
}

func planResult(plan string, ws []QuotaWindow, c Config, now time.Time) (Result, error) {
	s := allowance.Snapshot{Version: allowance.Version, Plan: plan, Source: c.Preset, FetchedAt: now.UTC(), CheckedAt: now.UTC()}
	for _, q := range ws {
		if math.IsNaN(q.Used) || math.IsInf(q.Used, 0) || math.IsNaN(q.Amount) || math.IsInf(q.Amount, 0) || math.IsNaN(q.Limit) || math.IsInf(q.Limit, 0) || q.Balance != nil && (math.IsNaN(*q.Balance) || math.IsInf(*q.Balance, 0)) {
			return Result{}, errors.New("provider quota contains a non-finite amount")
		}
		if q.ResetsAt != nil && (q.ResetsAt.Year() < 1 || q.ResetsAt.Year() > 9999) {
			return Result{}, errors.New("provider quota reset time is outside the supported range")
		}

		used := q.Used
		w := allowance.Window{Name: q.Name, UsedPercent: &used, Balance: q.Balance, Unit: q.Unit, PeriodSeconds: int64(q.Span / time.Second), ResetAt: q.ResetsAt, Informational: q.Aside, Credits: q.Credits}
		if q.Limit > 0 {
			amount, limit := q.Amount, q.Limit
			w.Amount = &amount
			w.Limit = &limit
		}
		if q.Balance != nil {
			w.UsedPercent = nil
			if q.Aside && *q.Balance > 0 {
				b := *q.Balance
				s.Credits = &allowance.Credits{Balance: &b, Unit: q.Unit}
			}
		}
		s.Windows = append(s.Windows, w)
	}
	if len(s.Windows) == 0 {
		return Result{}, errors.New("provider response contains no plan windows")
	}
	s.Status = s.For("", now, 0).Status
	return Result{Allowance: s}, nil
}

func parseGLM(body []byte, c Config, now time.Time) (Result, error) {
	var r struct {
		Success *bool `json:"success"`
		Data    *struct {
			Level  string `json:"level"`
			Limits []struct {
				Type         string `json:"type"`
				Unit, Number int
				Percentage   *float64 `json:"percentage"`
				Reset        any      `json:"nextResetTime"`
			} `json:"limits"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &r) != nil || r.Success != nil && !*r.Success || r.Data == nil {
		return Result{}, errors.New("GLM quota response is unavailable")
	}
	s := allowance.Snapshot{Version: allowance.Version, Plan: r.Data.Level, Source: c.Preset, FetchedAt: now.UTC(), CheckedAt: now.UTC()}
	for _, l := range r.Data.Limits {
		w := allowance.Window{Name: "Allowance", UsedPercent: l.Percentage, Unknown: l.Percentage == nil, ResetAt: Time(l.Reset)}
		switch {
		case strings.EqualFold(l.Type, "TIME_LIMIT"):
			w.Name = "MCP Month"
			w.Informational = true
		case l.Unit == 3:
			n := l.Number
			if n <= 0 {
				n = 5
			}
			w.Name = fmt.Sprintf("%d hours", n)
			w.PeriodSeconds = int64(n) * 3600
		case l.Unit == 6:
			w.Name = "Weekly"
			w.PeriodSeconds = 7 * 86400
		}
		s.Windows = append(s.Windows, w)
	}
	if len(s.Windows) == 0 {
		return Result{}, errors.New("GLM response contains no plan windows")
	}
	s.Status = s.For("", now, 0).Status
	return Result{Allowance: s}, nil
}

func parseOllama(body []byte, c Config, now time.Time) (Result, error) {
	c.Windows = []WindowRule{
		{Name: "Allowance", Amount: "included.allowance_usd - included.balance_usd", Limit: "included.allowance_usd", Unit: "USD", ResetPath: "included.period.until", Optional: true},
		{Name: "Session", Used: "100 - included.session.remaining_percent", ResetPath: "included.session.resets_at", Optional: true},
		{Name: "Weekly", Used: "100 - included.weekly.remaining_percent", ResetPath: "included.weekly.resets_at", PeriodSeconds: 604800, Optional: true},
		{Name: "Purchased credits", Balance: "purchased.balance_usd", Unit: "USD", Informational: true, Optional: true},
	}
	out, err := Parse(body, c, now)
	if v, _ := ReadNumber(body, "purchased.balance_usd"); v != nil {
		out.Allowance.Credits = &allowance.Credits{Balance: v, Unit: "USD"}
	}
	return out, err
}

func parseCommand(body []byte, c Config, now time.Time) (Result, error) {
	c.PlanPath = "credits.planId"
	for _, x := range []struct {
		key, name string
		seconds   int64
	}{{"fiveHour", "5 hours", 18000}, {"weekly", "Weekly", 604800}} {
		c.Windows = append(c.Windows, WindowRule{Name: x.name, Amount: "windowLimits." + x.key + ".used", Limit: "windowLimits." + x.key + ".cap", ResetPath: "windowLimits." + x.key + ".resetAt", PeriodSeconds: x.seconds, Optional: true})
	}
	// Usage-value credits must never be added to USD balances.
	c.Windows = append(c.Windows, WindowRule{Name: "Monthly credits", Balance: "credits.monthlyCredits", Unit: "credits", Optional: true}, WindowRule{Name: "Purchased credits", Balance: "credits.purchasedCredits + credits.freeCredits", Unit: "USD", Informational: true, Optional: true})
	return Parse(body, c, now)
}

// ParseMistral selects this key's budget. Using Vibe's budget for a Studio API
// key would duplicate an organization's allowance.
func ParseMistral(body []byte, kind string, now time.Time) (Result, error) {
	key := ""
	switch strings.ToLower(kind) {
	case "chat":
		key = "vibe_budget"
	case "api":
		key = "api_budget"
	default:
		return Result{}, errors.New("unsupported Mistral key kind")
	}
	unit, _ := ReadValue(body, key+".currency")
	currency, _ := unit.(string)
	c := Config{Preset: "mistral", Windows: []WindowRule{{Name: key, Used: key + ".usage_percentage", Limit: key + ".initial_budget", Amount: key + ".initial_budget * " + key + ".usage_percentage / 100", Unit: currency, Period: "month", ResetPath: key + ".reset_at"}}}
	if v, _ := ReadValue(body, key+".payg_enabled"); v == true {
		c.Windows[0].Informational = true
	}
	return Parse(body, c, now)
}
