package server

// Admission diagnostics remain in the administrator audit surface. Public
// errors retain their existing codes and never expose candidate inventory.
type jevCandidateRejection struct {
	CandidateID string `json:"candidate_id,omitempty"`
	RouteID     string `json:"route_id,omitempty"`
	jevCompatibility
}

func (s *Server) recordJevAdmission(resourceID string, rejections []jevCandidateRejection) {
	if len(rejections) == 0 {
		return
	}
	s.store.RecordAuditEvent(AuditEvent{
		Action: "routing.jev_admission", ResourceType: "gateway_request", ResourceID: resourceID,
		Status: "candidates_rejected", Message: "Jev candidate compatibility check",
		AfterSnapshot: auditSnapshotJSON(map[string]any{"rejections": rejections}),
	})
}
