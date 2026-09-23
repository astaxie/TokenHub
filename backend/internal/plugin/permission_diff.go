package plugin

import (
	"net/url"
	"sort"
	"strings"
)

type PermissionDiffVerdict string

const (
	PermissionDiffVerdictAllow            PermissionDiffVerdict = "allow"
	PermissionDiffVerdictReviewRequired   PermissionDiffVerdict = "review_required"
	PermissionDiffVerdictApprovalRequired PermissionDiffVerdict = "approval_required"
)

type PermissionDiffReasonCode string

const (
	PermissionDiffReasonUnchanged                PermissionDiffReasonCode = "unchanged"
	PermissionDiffReasonPermissionReduced        PermissionDiffReasonCode = "permission_reduced"
	PermissionDiffReasonPermissionAdded          PermissionDiffReasonCode = "permission_added"
	PermissionDiffReasonSensitivePermissionAdded PermissionDiffReasonCode = "sensitive_permission_added"
	PermissionDiffReasonSecretPermissionAdded    PermissionDiffReasonCode = "secret_permission_added"
)

type PermissionDiff struct {
	Available          bool                     `json:"available"`
	Verdict            PermissionDiffVerdict    `json:"verdict"`
	ReasonCode         PermissionDiffReasonCode `json:"reason_code"`
	HighestSensitivity PermissionSensitivity    `json:"highest_sensitivity"`
	Summary            PermissionDiffSummary    `json:"summary"`
	Added              []PermissionChange       `json:"added"`
	Removed            []PermissionChange       `json:"removed"`
	Unchanged          []PermissionChange       `json:"unchanged"`
	ChangedSensitivity []PermissionChange       `json:"changed_sensitivity"`
}

type PermissionDiffSummary struct {
	Added              int `json:"added"`
	Removed            int `json:"removed"`
	Unchanged          int `json:"unchanged"`
	ChangedSensitivity int `json:"changed_sensitivity"`
}

type PermissionChange struct {
	Kind                 PermissionKind        `json:"kind"`
	Name                 string                `json:"name"`
	Access               PermissionAccess      `json:"access"`
	Sensitivity          PermissionSensitivity `json:"sensitivity"`
	PreviousSensitivity  PermissionSensitivity `json:"previous_sensitivity,omitempty"`
	CandidateSensitivity PermissionSensitivity `json:"candidate_sensitivity,omitempty"`
}

func DiffPermissions(previous []PermissionDescriptor, candidate []PermissionDescriptor) PermissionDiff {
	previousByKey := permissionDiffMap(previous)
	candidateByKey := permissionDiffMap(candidate)
	diff := PermissionDiff{
		Available:          true,
		Verdict:            PermissionDiffVerdictAllow,
		ReasonCode:         PermissionDiffReasonUnchanged,
		HighestSensitivity: PermissionSensitivityPublic,
	}
	for key, candidatePermission := range candidateByKey {
		previousPermission, ok := previousByKey[key]
		switch {
		case !ok:
			diff.Added = append(diff.Added, permissionChange(candidatePermission))
			diff.HighestSensitivity = maxPermissionSensitivity(diff.HighestSensitivity, candidatePermission.Sensitivity)
		case previousPermission.Sensitivity != candidatePermission.Sensitivity:
			diff.ChangedSensitivity = append(diff.ChangedSensitivity, PermissionChange{
				Kind:                 candidatePermission.Kind,
				Name:                 candidatePermission.Name,
				Access:               candidatePermission.Access,
				Sensitivity:          candidatePermission.Sensitivity,
				PreviousSensitivity:  previousPermission.Sensitivity,
				CandidateSensitivity: candidatePermission.Sensitivity,
			})
			diff.HighestSensitivity = maxPermissionSensitivity(diff.HighestSensitivity, candidatePermission.Sensitivity)
		default:
			diff.Unchanged = append(diff.Unchanged, permissionChange(candidatePermission))
		}
	}
	for key, previousPermission := range previousByKey {
		if _, ok := candidateByKey[key]; !ok {
			diff.Removed = append(diff.Removed, permissionChange(previousPermission))
			diff.HighestSensitivity = maxPermissionSensitivity(diff.HighestSensitivity, previousPermission.Sensitivity)
		}
	}
	sortPermissionChanges(diff.Added)
	sortPermissionChanges(diff.Removed)
	sortPermissionChanges(diff.Unchanged)
	sortPermissionChanges(diff.ChangedSensitivity)
	diff.Summary = PermissionDiffSummary{
		Added:              len(diff.Added),
		Removed:            len(diff.Removed),
		Unchanged:          len(diff.Unchanged),
		ChangedSensitivity: len(diff.ChangedSensitivity),
	}
	diff.Verdict, diff.ReasonCode = permissionDiffVerdict(diff)
	return diff
}

func permissionDiffMap(items []PermissionDescriptor) map[permissionKey]PermissionDescriptor {
	normalized := map[permissionKey]PermissionDescriptor{}
	for _, item := range NormalizePermissionDescriptors(items) {
		item.Name = sanitizePermissionDiffName(item)
		key := permissionKeyFor(item)
		if existing, ok := normalized[key]; ok && permissionSensitivityRank(existing.Sensitivity) >= permissionSensitivityRank(item.Sensitivity) {
			continue
		}
		normalized[key] = item
	}
	return normalized
}

func sanitizePermissionDiffName(permission PermissionDescriptor) string {
	name := strings.TrimSpace(permission.Name)
	if permission.Kind != PermissionKindNetwork {
		return name
	}
	if name == "*" || strings.HasPrefix(name, "*.") {
		return name
	}
	parsed, err := url.Parse(name)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		if name == "" {
			return "[redacted]"
		}
		return name
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	safe := strings.TrimSpace(parsed.String())
	if safe == "" {
		return "[redacted]"
	}
	return safe
}

func permissionChange(permission PermissionDescriptor) PermissionChange {
	return PermissionChange{
		Kind:        permission.Kind,
		Name:        permission.Name,
		Access:      permission.Access,
		Sensitivity: permission.Sensitivity,
	}
}

func sortPermissionChanges(changes []PermissionChange) {
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Kind != changes[j].Kind {
			return changes[i].Kind < changes[j].Kind
		}
		if changes[i].Name != changes[j].Name {
			return changes[i].Name < changes[j].Name
		}
		return changes[i].Access < changes[j].Access
	})
}

func permissionDiffVerdict(diff PermissionDiff) (PermissionDiffVerdict, PermissionDiffReasonCode) {
	risk := PermissionSensitivityPublic
	for _, change := range diff.Added {
		risk = maxPermissionSensitivity(risk, change.Sensitivity)
	}
	for _, change := range diff.ChangedSensitivity {
		if permissionSensitivityRank(change.CandidateSensitivity) > permissionSensitivityRank(change.PreviousSensitivity) {
			risk = maxPermissionSensitivity(risk, change.CandidateSensitivity)
		}
	}
	if len(diff.Added) == 0 && risk == PermissionSensitivityPublic {
		if len(diff.Removed) > 0 || len(diff.ChangedSensitivity) > 0 {
			return PermissionDiffVerdictAllow, PermissionDiffReasonPermissionReduced
		}
		return PermissionDiffVerdictAllow, PermissionDiffReasonUnchanged
	}
	if risk == PermissionSensitivitySecret {
		return PermissionDiffVerdictApprovalRequired, PermissionDiffReasonSecretPermissionAdded
	}
	if risk == PermissionSensitivitySensitive {
		return PermissionDiffVerdictApprovalRequired, PermissionDiffReasonSensitivePermissionAdded
	}
	return PermissionDiffVerdictReviewRequired, PermissionDiffReasonPermissionAdded
}

func maxPermissionSensitivity(left PermissionSensitivity, right PermissionSensitivity) PermissionSensitivity {
	if permissionSensitivityRank(right) > permissionSensitivityRank(left) {
		return right
	}
	return left
}

func permissionSensitivityRank(sensitivity PermissionSensitivity) int {
	switch sensitivity {
	case PermissionSensitivitySecret:
		return 3
	case PermissionSensitivitySensitive:
		return 2
	case PermissionSensitivityInternal:
		return 1
	case PermissionSensitivityPublic:
		return 0
	default:
		return 0
	}
}
