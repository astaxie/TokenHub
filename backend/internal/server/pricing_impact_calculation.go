package server

import (
	"encoding/json"
	"math"
	"math/big"
	"sort"

	"tokenhub/backend/internal/metering"
)

type impactSum struct {
	current, candidate, cost *big.Rat
	requests, losses         int
	names                    []string
}

func newImpactSum() *impactSum {
	return &impactSum{current: new(big.Rat), candidate: new(big.Rat), cost: new(big.Rat)}
}
func impactPercent(n, d *big.Rat) *string {
	if d.Sign() == 0 {
		return nil
	}
	return statementString(new(big.Rat).Mul(new(big.Rat).Quo(n, d), big.NewRat(100, 1)).FloatString(2))
}
func (s *impactSum) add(current, candidate, cost *big.Rat) {
	s.current.Add(s.current, current)
	s.candidate.Add(s.candidate, candidate)
	s.cost.Add(s.cost, cost)
	s.requests++
	if current.Cmp(cost) < 0 || candidate.Cmp(cost) < 0 {
		s.losses++
	}
}
func (s *impactSum) amounts() pricingImpactAmounts {
	before := new(big.Rat).Sub(s.current, s.cost)
	after := new(big.Rat).Sub(s.candidate, s.cost)
	return pricingImpactAmounts{Current: s.current.FloatString(12), Candidate: s.candidate.FloatString(12), Cost: s.cost.FloatString(12), Delta: new(big.Rat).Sub(s.candidate, s.current).FloatString(12), CurrentMargin: before.FloatString(12), CandidateMargin: after.FloatString(12), CurrentMarginRate: impactPercent(before, s.current), CandidateMarginRate: impactPercent(after, s.candidate)}
}
func replayUsage(units metering.Units) (Usage, bool) {
	var input int64
	for _, v := range []int64{units.Input, units.CacheRead, units.CacheWrite, units.CacheWrite5m, units.CacheWrite1h} {
		if v < 0 || v > math.MaxInt64-input {
			return Usage{}, false
		}
		input += v
	}
	if units.Output < 0 || units.Output > math.MaxInt64-input {
		return Usage{}, false
	}
	return Usage{PromptTokens: input, CompletionTokens: units.Output, TotalTokens: input + units.Output, CachedInputTokens: units.CacheRead, CacheWriteInputTokens: units.CacheWrite + units.CacheWrite5m + units.CacheWrite1h, CacheWrite5mInputTokens: units.CacheWrite5m, CacheWrite1hInputTokens: units.CacheWrite1h}, true
}
func replayEvidenceKnown(e *usageEvidence, rates metering.Rates, units metering.Units) bool {
	if e == nil || e.Protocol == "legacy" || e.invalid() || e.StreamComplete != nil && !*e.StreamComplete {
		return false
	}
	known := func(name string) bool {
		f := e.Fields[name]
		return f.Value != nil && (f.State == "reported" || f.State == "derived" || f.State == "estimated")
	}
	if !known("input_total") || !known("output") {
		return false
	}
	// If missing subdivisions all use the same price, their allocation does not
	// affect the total. Otherwise missing subdivisions cannot be assumed zero.
	if !known("cache_read") && rates.CacheRead != rates.Input {
		return false
	}
	if !known("cache_write_total") && (rates.CacheWrite != rates.Input || rates.CacheWrite5m != rates.Input || rates.CacheWrite1h != rates.Input) {
		return false
	}
	writes := units.CacheWrite + units.CacheWrite5m + units.CacheWrite1h
	if writes > 0 && (!known("cache_write_5m") && rates.CacheWrite5m != rates.CacheWrite || !known("cache_write_1h") && rates.CacheWrite1h != rates.CacheWrite) {
		return false
	}
	return true
}
func calculatePricingImpact(out *pricingImpactReport, current, candidate Model, rows []statementRow) {
	attempts := map[string][]statementRow{}
	tenants := []statementRow{}
	for _, row := range rows {
		if row.Source == "tenant" {
			tenants = append(tenants, row)
		} else if row.Source == "provider_estimate" {
			attempts[row.RequestID] = append(attempts[row.RequestID], row)
		}
	}
	total := newImpactSum()
	groups := map[string]*impactSum{}
	knownCharges := new(big.Rat)
	coveredCharges := new(big.Rat)
	matched := map[int]bool{}
	for _, tenant := range tenants {
		out.Requests++
		var historical *big.Rat
		knownCharge := false
		if tenant.USD != nil {
			historical, knownCharge = statementAmount(*tenant.USD)
		}
		if knownCharge {
			knownCharges.Add(knownCharges, historical)
		} else {
			out.UnknownCharges++
		}
		usage, valid := replayUsage(tenant.Units)
		if !valid || tenant.Status != "estimated" || !replayEvidenceKnown(tenant.Evidence, legacyMeteringPrice(current, tenant.At, false).Rates, tenant.Units) || !replayEvidenceKnown(tenant.Evidence, legacyMeteringPrice(candidate, tenant.At, false).Rates, tenant.Units) {
			out.Excluded["usage_incomplete"]++
			continue
		}
		if current.Modality == "embedding" && (usage.CompletionTokens > 0 || usage.CachedInputTokens > 0 || usage.CacheWriteInputTokens > 0) {
			out.Excluded["usage_incomplete"]++
			continue
		}
		cost := new(big.Rat)
		costKnown := len(attempts[tenant.RequestID]) > 0
		names := map[string]string{}
		for _, attempt := range attempts[tenant.RequestID] {
			keyBytes, _ := json.Marshal([]string{attempt.ProviderID, attempt.ResourceID})
			key := string(keyBytes)
			name := attempt.ProviderName
			if name == "" {
				name = attempt.ProviderID
			}
			if attempt.ResourceName != "" {
				name += " / " + attempt.ResourceName
			}
			names[key] = name
			var amount *big.Rat
			ok := false
			if attempt.USD != nil {
				amount, ok = statementAmount(*attempt.USD)
			}
			if !ok || attempt.Status != "estimated" || attempt.Price == nil || !replayEvidenceKnown(attempt.Evidence, attempt.Price.Rates, attempt.Units) {
				costKnown = false
				continue
			}
			cost.Add(cost, amount)
		}
		if !costKnown {
			out.Excluded["cost_incomplete"]++
			continue
		}
		before, okBefore := statementAmount(billingAmount(priceUsageAt(current, usage, tenant.At).CostUSD))
		after, okAfter := statementAmount(billingAmount(priceUsageAt(candidate, usage, tenant.At).CostUSD))
		if !okBefore || !okAfter {
			out.Excluded["calculation_unavailable"]++
			continue
		}
		total.add(before, after, cost)
		if knownCharge {
			coveredCharges.Add(coveredCharges, historical)
		}
		for i, p := range candidate.PricingPeriods {
			if pricingPeriodMatches(p, tenant.At) {
				matched[i] = true
				break
			}
		}
		keys := make([]string, 0, len(names))
		for key := range names {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		encoded, _ := json.Marshal(keys)
		key := string(encoded)
		group := groups[key]
		if group == nil {
			group = newImpactSum()
			for _, id := range keys {
				group.names = append(group.names, names[id])
			}
			groups[key] = group
		}
		group.add(before, after, cost)
	}
	out.RecordedCharges = knownCharges.FloatString(12)
	out.Computable = total.requests
	out.LossRequests = total.losses
	out.RequestCoverage = impactPercent(big.NewRat(int64(out.Computable), 1), big.NewRat(int64(out.Requests), 1))
	out.ChargeCoverage = impactPercent(coveredCharges, knownCharges)
	if total.requests > 0 {
		amounts := total.amounts()
		out.ComputableAmounts = &amounts
		if out.Computable == out.Requests {
			out.Overall = &amounts
		}
	}
	if out.Requests == 0 {
		out.Risks = append(out.Risks, "no_history")
	}
	if out.Computable < out.Requests {
		out.Risks = append(out.Risks, "partial_evidence")
	}
	if out.LossRequests > 0 {
		out.Risks = append(out.Risks, "losses")
	}
	for i, p := range candidate.PricingPeriods {
		if !matched[i] {
			out.UntestedPeriods = append(out.UntestedPeriods, p.Name)
		}
	}
	if len(out.UntestedPeriods) > 0 {
		out.Risks = append(out.Risks, "unexercised_rules")
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		g := groups[key]
		out.Groups = append(out.Groups, pricingImpactGroup{ID: key, Names: g.names, Requests: g.requests, LossRequests: g.losses, Amounts: g.amounts()})
	}
}
