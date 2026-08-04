package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// IDStrategy controls how MintID generates canonical IDs for
// resources synthesised from a migration bundle.
type IDStrategy string

const (
	IDStrategyStable   IDStrategy = "stable"
	IDStrategyPrefixed IDStrategy = "prefixed"
	IDStrategySource   IDStrategy = "source"
)

// Valid reports whether the strategy is one of the known values.
func (s IDStrategy) Valid() bool {
	switch s {
	case IDStrategyStable, IDStrategyPrefixed, IDStrategySource:
		return true
	}
	return false
}

// MintID produces a canonical ID for a resource according to the
// requested strategy. The stable and prefixed strategies never emit
// characters that are unsafe inside a URL path segment; the source
// strategy returns the external ID verbatim and offers no such guarantee.
func MintID(strategy IDStrategy, system, externalID string) (string, error) {
	system = strings.TrimSpace(system)
	externalID = strings.TrimSpace(externalID)
	if system == "" {
		return "", fmt.Errorf("bundle: MintID: system is required")
	}
	if externalID == "" {
		return "", fmt.Errorf("bundle: MintID: externalID is required")
	}
	switch strategy {
	case IDStrategyStable:
		sum := sha256.Sum256([]byte(system + "\x00" + externalID))
		return system + "-" + hex.EncodeToString(sum[:6]), nil
	case IDStrategyPrefixed:
		// External IDs may embed URLs or other path-hostile input. Keep a
		// sanitized readable slug for operators and append a short hash of
		// the original value so distinct inputs stay distinct.
		sum := sha256.Sum256([]byte(externalID))
		return system + ":" + sanitizeIDComponent(externalID) + "-" + hex.EncodeToString(sum[:4]), nil
	case IDStrategySource:
		return externalID, nil
	default:
		return "", fmt.Errorf("bundle: MintID: unknown strategy %q", strategy)
	}
}

const maxIDSlugLength = 48

// sanitizeIDComponent maps an arbitrary external ID onto a URL
// path-safe slug limited to letters, digits, '.', '_', and '-'.
func sanitizeIDComponent(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		safe := r == '.' || r == '_' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if safe {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > maxIDSlugLength {
		slug = strings.Trim(slug[:maxIDSlugLength], "-")
	}
	if slug == "" {
		slug = "id"
	}
	return slug
}
