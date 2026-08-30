package plugin

import "testing"

func TestDiffPermissionsReportsAddedRemovedUnchanged(t *testing.T) {
	diff := DiffPermissions(
		[]PermissionDescriptor{
			{Kind: PermissionKindData, Name: string(DataAudit), Access: PermissionAccessWrite},
			{Kind: PermissionKindData, Name: string(DataUsage), Access: PermissionAccessRead},
		},
		[]PermissionDescriptor{
			{Kind: PermissionKindData, Name: string(DataUsage), Access: PermissionAccessRead},
			{Kind: PermissionKindNetwork, Name: "*.example.com", Access: PermissionAccessConnect},
		},
	)
	if diff.Verdict != PermissionDiffVerdictReviewRequired || diff.ReasonCode != PermissionDiffReasonPermissionAdded {
		t.Fatalf("verdict = %s/%s, want review_required/permission_added", diff.Verdict, diff.ReasonCode)
	}
	if diff.Summary.Added != 1 || diff.Summary.Removed != 1 || diff.Summary.Unchanged != 1 || diff.Summary.ChangedSensitivity != 0 {
		t.Fatalf("summary = %+v, want 1 added, 1 removed, 1 unchanged", diff.Summary)
	}
	if diff.Added[0].Kind != PermissionKindNetwork || diff.Removed[0].Name != string(DataAudit) || diff.Unchanged[0].Name != string(DataUsage) {
		t.Fatalf("diff changes = added=%+v removed=%+v unchanged=%+v", diff.Added, diff.Removed, diff.Unchanged)
	}
}

func TestDiffPermissionsClassifiesSensitiveAndSecretAdditions(t *testing.T) {
	sensitive := DiffPermissions(nil, []PermissionDescriptor{
		{Kind: PermissionKindData, Name: string(DataRequestBody), Access: PermissionAccessRead},
	})
	if sensitive.Verdict != PermissionDiffVerdictApprovalRequired ||
		sensitive.ReasonCode != PermissionDiffReasonSensitivePermissionAdded ||
		sensitive.HighestSensitivity != PermissionSensitivitySensitive {
		t.Fatalf("sensitive diff = %+v, want approval_required sensitive addition", sensitive)
	}
	secret := DiffPermissions(nil, []PermissionDescriptor{
		{Kind: PermissionKindData, Name: string(DataProviderCredentials), Access: PermissionAccessRead},
	})
	if secret.Verdict != PermissionDiffVerdictApprovalRequired ||
		secret.ReasonCode != PermissionDiffReasonSecretPermissionAdded ||
		secret.HighestSensitivity != PermissionSensitivitySecret {
		t.Fatalf("secret diff = %+v, want approval_required secret addition", secret)
	}
}

func TestDiffPermissionsReportsSensitivityChanges(t *testing.T) {
	diff := DiffPermissions(
		[]PermissionDescriptor{{Kind: PermissionKindNetwork, Name: "api.example.com", Access: PermissionAccessConnect, Sensitivity: PermissionSensitivityInternal}},
		[]PermissionDescriptor{{Kind: PermissionKindNetwork, Name: "api.example.com", Access: PermissionAccessConnect, Sensitivity: PermissionSensitivitySensitive}},
	)
	if diff.Summary.ChangedSensitivity != 1 || diff.Verdict != PermissionDiffVerdictApprovalRequired ||
		diff.ReasonCode != PermissionDiffReasonSensitivePermissionAdded {
		t.Fatalf("diff = %+v, want one sensitive change requiring approval", diff)
	}
	change := diff.ChangedSensitivity[0]
	if change.PreviousSensitivity != PermissionSensitivityInternal || change.CandidateSensitivity != PermissionSensitivitySensitive {
		t.Fatalf("change = %+v, want previous internal and candidate sensitive", change)
	}

	reduced := DiffPermissions(
		[]PermissionDescriptor{{Kind: PermissionKindNetwork, Name: "api.example.com", Access: PermissionAccessConnect, Sensitivity: PermissionSensitivitySecret}},
		[]PermissionDescriptor{{Kind: PermissionKindNetwork, Name: "api.example.com", Access: PermissionAccessConnect, Sensitivity: PermissionSensitivityInternal}},
	)
	if reduced.Verdict != PermissionDiffVerdictAllow || reduced.ReasonCode != PermissionDiffReasonPermissionReduced {
		t.Fatalf("reduced diff = %+v, want allow permission_reduced", reduced)
	}
}

func TestDiffPermissionsNormalizesDuplicatesOrderingAndNetworkNames(t *testing.T) {
	diff := DiffPermissions(nil, []PermissionDescriptor{
		{Kind: PermissionKindNetwork, Name: "https://user:pass@api.example.com/v1?token=secret#frag", Access: PermissionAccessConnect, Sensitivity: PermissionSensitivityInternal},
		{Kind: PermissionKindData, Name: " " + string(DataUsage) + " ", Access: PermissionAccessRead},
		{Kind: PermissionKindData, Name: string(DataUsage), Access: PermissionAccessRead},
	})
	if diff.Summary.Added != 2 {
		t.Fatalf("summary = %+v, want duplicate data permission collapsed", diff.Summary)
	}
	if diff.Added[0].Name != string(DataUsage) {
		t.Fatalf("first added permission = %+v, want data usage sorted before network", diff.Added[0])
	}
	if diff.Added[1].Name != "https://api.example.com/v1" {
		t.Fatalf("network permission name = %q, want sanitized URL without userinfo/query/fragment", diff.Added[1].Name)
	}
}
