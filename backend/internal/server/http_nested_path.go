package server

import (
	"net/url"
	"strings"
)

// splitEscapedAdminPath splits an admin path into segments on real separators
// only. It works on the escaped path, so a resource ID that encodes "/" as
// %2F stays a single segment instead of being torn into several — r.URL.Path
// is already decoded and cannot tell the two apart. Each segment is then
// decoded individually.
func splitEscapedAdminPath(escapedPath string, prefix string) []string {
	trimmed := strings.Trim(strings.TrimPrefix(escapedPath, prefix), "/")
	if trimmed == "" {
		return nil
	}
	segments := strings.Split(trimmed, "/")
	for index, segment := range segments {
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			return nil
		}
		segments[index] = decoded
	}
	return segments
}

// providerResourceActions and routingRuleActions list the recognized nested
// action suffixes per admin route. Resource IDs may contain slashes (e.g.
// IDs imported from external systems), so nested paths cannot be split
// naively on "/": the trailing segment is treated as an action only when it
// appears in the route's table. Actions must not contain "/" themselves.
var (
	providerResourceActions = []string{"quota/reset-credits", "quota/reset", "image-capability", "health", "test", "refresh-token", "quota"}
	routingRuleActions      = []string{"explain"}
)

// splitNestedAdminPath splits remainder into {id, action} when it ends with a
// known "/<action>" suffix, otherwise the whole remainder is the resource ID.
// An action may contain a slash when the complete nested suffix is explicitly
// listed, as with the quota reset endpoints.
func splitNestedAdminPath(remainder string, actions []string) []string {
	if remainder == "" {
		return nil
	}
	for _, action := range actions {
		if id, ok := strings.CutSuffix(remainder, "/"+action); ok {
			return []string{id, action}
		}
	}
	return []string{remainder}
}

// splitNestedEscapedAdminPath is splitNestedAdminPath over the escaped path.
// The suffix match runs before decoding so a real "/<action>" separator is
// told apart from an ID that only looks like one once decoded: a resource
// whose ID is "tenant/health" arrives as "tenant%2Fhealth" and must stay a
// single ID rather than becoming the "health" action on "tenant".
func splitNestedEscapedAdminPath(escapedPath string, prefix string, actions []string) []string {
	parts := splitNestedAdminPath(strings.Trim(strings.TrimPrefix(escapedPath, prefix), "/"), actions)
	if len(parts) == 0 {
		return nil
	}
	decoded, err := url.PathUnescape(parts[0])
	if err != nil {
		return nil
	}
	parts[0] = decoded
	return parts
}
