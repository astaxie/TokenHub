package litellm

import (
	"errors"
	"testing"
)

func TestValidateSupportedVersion(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		wantErr  bool
		wantSent error
	}{
		{name: "min supported", version: "1.52.0"},
		{name: "mid supported", version: "v1.68.3"},
		{name: "too old", version: "1.51.9", wantErr: true, wantSent: ErrVersionUnsupported},
		{name: "too new", version: "1.70.0", wantErr: true, wantSent: ErrVersionUnsupported},
		{name: "too new minor", version: "1.75.0", wantErr: true, wantSent: ErrVersionUnsupported},
		{name: "too new major", version: "2.0.0", wantErr: true, wantSent: ErrVersionUnsupported},
		{name: "invalid", version: "main", wantErr: true, wantSent: ErrVersionUnknown},
		{name: "empty", version: "", wantErr: true, wantSent: ErrVersionUnknown},
	}
	for _, test := range tests {
		err := ValidateSupportedVersion(test.version)
		if test.wantErr && err == nil {
			t.Fatalf("%s: expected error", test.name)
		}
		if !test.wantErr && err != nil {
			t.Fatalf("%s: unexpected error: %v", test.name, err)
		}
		if test.wantSent != nil && !errors.Is(err, test.wantSent) {
			t.Fatalf("%s: expected sentinel %v, got %v", test.name, test.wantSent, err)
		}
	}
}
