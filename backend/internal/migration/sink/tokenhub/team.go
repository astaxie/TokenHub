package tokenhub

import (
	"fmt"

	"tokenhub/backend/internal/migration/bundle"
	"tokenhub/backend/internal/server"
)

// teamAnchorField is the AdminResource.Fields key holding the bundle external
// ref of a migrated team. It is the idempotency anchor: a team carrying the
// anchor was created by an earlier run of the same migration.
const teamAnchorField = "migration_external_ref"

// teamResolution is the outcome of matching one bundle team against the teams
// already present on the target.
type teamResolution struct {
	// ID is the team ID to use on the target: the existing team's ID when
	// Exists is true, otherwise the ID the team will be created with.
	ID string
	// Exists reports whether a matching team is already on the target.
	Exists bool
}

// teamAnchor reads the migration anchor off an existing team, returning ""
// for teams that were not created by a migration.
func teamAnchor(team server.AdminResource) string {
	anchor, _ := team.Fields[teamAnchorField].(string)
	return anchor
}

// resolveTeam matches a bundle team against the teams already on the target.
//
// Matching is anchored on the bundle external ref and falls back to an exact
// ID match, never on the display name: two unrelated source teams may share a
// name, and merging them would silently widen team membership.
//
// A team whose ID matches but whose anchor belongs to a different external ref
// is a hard conflict rather than a reuse, because the store's CreateResource is
// an upsert and would otherwise overwrite an unrelated team. An inactive match
// is also an error: the target rejects inactive teams for user and project
// mutations, and activating one is an authorization change the migration must
// not make on its own.
func resolveTeam(existing []server.AdminResource, item bundle.TeamRef) (teamResolution, error) {
	for _, team := range existing {
		if teamAnchor(team) != item.ExternalRef.ID {
			continue
		}
		if err := requireActiveTeam(team, item); err != nil {
			return teamResolution{}, err
		}
		return teamResolution{ID: team.ID, Exists: true}, nil
	}
	for _, team := range existing {
		if team.ID != item.ID {
			continue
		}
		if anchor := teamAnchor(team); anchor != "" && anchor != item.ExternalRef.ID {
			return teamResolution{}, fmt.Errorf(
				"team %s on the target is already migrated from %s; refusing to reuse it for %s",
				team.ID, anchor, item.ExternalRef.ID)
		}
		if err := requireActiveTeam(team, item); err != nil {
			return teamResolution{}, err
		}
		return teamResolution{ID: team.ID, Exists: true}, nil
	}
	return teamResolution{ID: item.ID, Exists: false}, nil
}

func requireActiveTeam(team server.AdminResource, item bundle.TeamRef) error {
	if team.Status == "" || team.Status == server.StatusActive {
		return nil
	}
	return fmt.Errorf(
		"team %s on the target matches %s but is %s; activate it before migrating",
		team.ID, item.ExternalRef.ID, team.Status)
}

// planTeams resolves every bundle team against the target and reports the
// change an apply would make. Teams that would be created are accumulated so
// two bundle entries competing for the same target ID surface as the conflict
// apply would hit rather than as two independent creates.
func planTeams(existing []server.AdminResource, teams []bundle.TeamRef) (map[string]string, []Change, error) {
	prospective := append([]server.AdminResource(nil), existing...)
	ids := make(map[string]string, len(teams))
	changes := make([]Change, 0, len(teams))
	for _, item := range teams {
		resolution, err := resolveTeam(prospective, item)
		if err != nil {
			return nil, nil, err
		}
		ids[item.ExternalRef.ID] = resolution.ID
		action := ActionSkip
		if !resolution.Exists {
			action = ActionCreate
			prospective = append(prospective, desiredTeamResource(item))
		}
		changes = append(changes, Change{Resource: "team", ID: resolution.ID, Action: action})
	}
	return ids, changes, nil
}

// verifyTeams reports the bundle teams that are not on the target in a usable
// form. It mirrors resolveTeam so verification agrees with apply: a team that
// apply would still have to create, or would refuse to reuse, is drift and not
// a converged state. Only resolved teams enter the returned index, so the
// resources referencing an unresolved team are reported as missing too.
func verifyTeams(existing []server.AdminResource, teams []bundle.TeamRef) (map[string]string, []VerifyIssue) {
	ids := make(map[string]string, len(teams))
	var issues []VerifyIssue
	for _, item := range teams {
		resolution, err := resolveTeam(existing, item)
		switch {
		case err != nil:
			issues = append(issues, VerifyIssue{Resource: "team", Ref: item.ExternalRef.ID, Message: err.Error()})
		case !resolution.Exists:
			issues = append(issues, VerifyIssue{Resource: "team", Ref: item.ExternalRef.ID, Message: "team not found"})
		default:
			ids[item.ExternalRef.ID] = resolution.ID
		}
	}
	return ids, issues
}

// desiredTeamResource is the AdminResource a bundle team is created as.
func desiredTeamResource(item bundle.TeamRef) server.AdminResource {
	return server.AdminResource{
		ID:     item.ID,
		Name:   item.Name,
		Status: server.StatusActive,
		Fields: map[string]any{teamAnchorField: item.ExternalRef.ID},
	}
}
