package server

import "sort"

// Admission diagnostics remain in the administrator audit surface. Public
// errors retain their existing codes and never expose candidate inventory.
type jevCandidateRejection struct {
	CandidateID string `json:"candidate_id,omitempty"`
	RouteID     string `json:"route_id,omitempty"`
	jevCompatibility
	Missing []jevParameterRejection `json:"missing,omitempty"`
}

// jevAdmissionReasonLimit caps one public rejection summary. The audit snapshot
// keeps the full per-candidate detail either way.
const jevAdmissionReasonLimit = 10

// jevAdmissionReason aggregates one rejection across candidates. It carries no
// candidate, provider, or model identities and no request values.
type jevAdmissionReason struct {
	Reason         string `json:"reason"`
	Parameter      string `json:"parameter,omitempty"`
	CandidateCount int    `json:"candidate_count"`
}

// summarizeJevRejections collapses per-candidate rejections into one entry per
// (reason, parameter) pair, ordered by parameter and then reason, and reports
// whether more distinct reasons were dropped by the limit.
func summarizeJevRejections(rejections []jevCandidateRejection) ([]jevAdmissionReason, bool) {
	type key struct{ reason, parameter string }
	counts := map[key]int{}
	order := make([]key, 0, len(rejections))
	add := func(reason, parameter string) {
		pair := key{reason: reason, parameter: parameter}
		if _, seen := counts[pair]; !seen {
			order = append(order, pair)
		}
		counts[pair]++
	}
	for _, rejection := range rejections {
		if len(rejection.Missing) == 0 {
			if rejection.Reason != "" {
				add(rejection.Reason, rejection.Parameter)
			}
			continue
		}
		seen := make(map[key]bool, len(rejection.Missing))
		for _, missing := range rejection.Missing {
			pair := key{reason: missing.Reason, parameter: missing.Parameter}
			if seen[pair] {
				continue
			}
			seen[pair] = true
			add(missing.Reason, missing.Parameter)
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].parameter != order[j].parameter {
			return order[i].parameter < order[j].parameter
		}
		return order[i].reason < order[j].reason
	})
	truncated := len(order) > jevAdmissionReasonLimit
	if truncated {
		order = order[:jevAdmissionReasonLimit]
	}
	reasons := make([]jevAdmissionReason, 0, len(order))
	for _, pair := range order {
		reasons = append(reasons, jevAdmissionReason{
			Reason:         pair.reason,
			Parameter:      pair.parameter,
			CandidateCount: counts[pair],
		})
	}
	return reasons, truncated
}

func (s *Server) recordJevAdmission(resourceID string, rejections []jevCandidateRejection) {
	if len(rejections) == 0 {
		return
	}
	snapshot := map[string]any{"rejections": rejections}
	if reasons, truncated := summarizeJevRejections(rejections); len(reasons) > 0 {
		snapshot["reasons"] = reasons
		snapshot["truncated"] = truncated
	}
	s.store.RecordAuditEvent(AuditEvent{
		Action: "routing.jev_admission", ResourceType: "gateway_request", ResourceID: resourceID,
		Status: "candidates_rejected", Message: "Jev candidate compatibility check",
		AfterSnapshot: auditSnapshotJSON(snapshot),
	})
}
