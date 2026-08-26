package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const packageStateFileName = "plugin.state.json"

type PackageState struct {
	Status Status `json:"status,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func readPackageState(dir string) (PackageState, error) {
	data, err := os.ReadFile(filepath.Join(dir, packageStateFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return PackageState{Status: StatusEnabled}, nil
		}
		return PackageState{}, err
	}
	var state PackageState
	if err := json.Unmarshal(data, &state); err != nil {
		return PackageState{}, err
	}
	state.Status = Status(strings.TrimSpace(string(state.Status)))
	state.Reason = strings.TrimSpace(state.Reason)
	if state.Status == "" {
		state.Status = StatusEnabled
	}
	if state.Status != StatusEnabled && state.Status != StatusDisabled {
		return PackageState{}, fmt.Errorf("unsupported plugin package status %q", state.Status)
	}
	return state, nil
}

func (s PackageState) Enabled() bool {
	return s.Status != StatusDisabled
}
