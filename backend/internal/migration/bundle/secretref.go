package bundle

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// SecretRef points at a secret that will be resolved at apply
// time. Bundles carry SecretRefs instead of plaintext secrets.
//
// The JSON representation is deliberately minimal:
//
//	{"$secretRef": "ENV_NAME"}
type SecretRef struct {
	// Ref is the opaque handle passed to a SecretResolver.
	Ref string
}

const secretRefField = "$secretRef"

// MarshalJSON emits the {"$secretRef": "..."} shape.
func (s SecretRef) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{secretRefField: s.Ref})
}

// UnmarshalJSON accepts either the object form or a bare string.
func (s *SecretRef) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var raw string
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("bundle: SecretRef: %w", err)
		}
		s.Ref = raw
		return nil
	}
	var obj map[string]string
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("bundle: SecretRef: %w", err)
	}
	ref, ok := obj[secretRefField]
	if !ok {
		return fmt.Errorf("bundle: SecretRef: missing %s field", secretRefField)
	}
	s.Ref = ref
	return nil
}

// IsZero reports whether the SecretRef is empty.
func (s SecretRef) IsZero() bool { return s.Ref == "" }

// SecretResolver turns a SecretRef into a plaintext secret at apply
// time. Implementations should NOT log or persist the returned value.
type SecretResolver interface {
	Resolve(ref SecretRef) (string, error)
}

// EnvResolver resolves SecretRefs from process environment variables.
type EnvResolver struct{}

// Resolve implements SecretResolver.
func (EnvResolver) Resolve(ref SecretRef) (string, error) {
	if ref.IsZero() {
		return "", fmt.Errorf("bundle: empty SecretRef")
	}
	v, ok := os.LookupEnv(ref.Ref)
	if !ok {
		return "", fmt.Errorf("bundle: env var %q not set", ref.Ref)
	}
	return v, nil
}

// FileResolver resolves SecretRefs from a key=value file. Lines
// starting with # are ignored. Whitespace around the key and value
// is trimmed but not inside the value.
type FileResolver struct {
	entries map[string]string
}

// NewFileResolver loads a key=value file.
func NewFileResolver(path string) (*FileResolver, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("bundle: file resolver: %w", err)
	}
	defer f.Close()
	entries := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		entries[key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("bundle: file resolver: %w", err)
	}
	return &FileResolver{entries: entries}, nil
}

// Resolve implements SecretResolver.
func (r *FileResolver) Resolve(ref SecretRef) (string, error) {
	if ref.IsZero() {
		return "", fmt.Errorf("bundle: empty SecretRef")
	}
	v, ok := r.entries[ref.Ref]
	if !ok {
		return "", fmt.Errorf("bundle: secret %q not found in file", ref.Ref)
	}
	return v, nil
}

// StaticResolver resolves SecretRefs from an in-memory map. Intended
// for tests only.
type StaticResolver map[string]string

// Resolve implements SecretResolver.
func (r StaticResolver) Resolve(ref SecretRef) (string, error) {
	if ref.IsZero() {
		return "", fmt.Errorf("bundle: empty SecretRef")
	}
	v, ok := r[ref.Ref]
	if !ok {
		return "", fmt.Errorf("bundle: secret %q not provided", ref.Ref)
	}
	return v, nil
}

// RedactedBundle returns a deep copy of the bundle safe for logging.
// All SecretRefs are preserved (they are already opaque handles)
// but the raw JSON marshal path is used so downstream code can log
// the returned value verbatim.
func RedactedBundle(b *CanonicalMigrationBundle) (*CanonicalMigrationBundle, error) {
	if b == nil {
		return nil, nil
	}
	data, err := json.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("bundle: redact: %w", err)
	}
	var out CanonicalMigrationBundle
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("bundle: redact: %w", err)
	}
	return &out, nil
}
