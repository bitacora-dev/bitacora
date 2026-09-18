package agentactions

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// LoadAllowlist reads the agent-local action configuration. A missing file is
// equivalent to an empty allowlist so fresh installations are disabled by
// default.
func LoadAllowlist(path string) (Allowlist, error) {
	if path == "" {
		return Allowlist{}, nil
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return Allowlist{}, nil
	}
	if err != nil {
		return Allowlist{}, fmt.Errorf("opening action configuration: %w", err)
	}
	defer f.Close()

	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	var allowlist Allowlist
	if err := decoder.Decode(&allowlist); err != nil {
		return Allowlist{}, fmt.Errorf("decoding action configuration: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Allowlist{}, fmt.Errorf("decoding action configuration: expected one JSON object")
	}
	return allowlist, nil
}
