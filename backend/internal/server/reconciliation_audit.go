package server

import "tokenhub/backend/internal/reconciliation"

// recordScheduledReconciliationAudit is composition plumbing for scheduler
// output. The domain provides a redacted run projection; server persists it in
// the existing audit sink.
func (s *GormStore) recordScheduledReconciliationAudit(run reconciliation.Run) {
	status := "success"
	if run.Status != reconciliation.RunSucceeded {
		status = "failed"
	}
	s.RecordAuditEvent(AuditEvent{
		ActorUserID: "system", ActorName: "TokenHub Scheduler", ActorRole: "system",
		Action: "reconcile", ResourceType: "billing_reconciliation", ResourceID: run.ID,
		Status: status, Message: run.ErrorCode,
		AfterSnapshot: snapshotJSON(reconciliation.RunAuditSnapshot(run)),
	})
}
