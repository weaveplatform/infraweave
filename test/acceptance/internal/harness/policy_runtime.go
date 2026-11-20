package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"
)

const opaModulePath = "github.com/open-policy-agent/opa"

// PolicyRuntimeVersion checks a REST response against the repository's OPA
// dependency pin, including versioned replacements. A local replacement cannot
// stand in for a reported release, matching the server's build-info contract.
func PolicyRuntimeVersion(body []byte) (string, error) {
	module, err := os.ReadFile(filepath.Join(RepoRoot, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read runtime module pin: %w", err)
	}
	return policyRuntimeVersion(body, module)
}

func policyRuntimeVersion(body, source []byte) (string, error) {
	module, err := modfile.Parse("go.mod", source, nil)
	if err != nil {
		return "", fmt.Errorf("parse runtime module pin: %w", err)
	}
	expected := ""
	for _, dependency := range module.Require {
		if dependency.Mod.Path == opaModulePath {
			expected = dependency.Mod.Version
		}
	}
	if expected == "" {
		return "", fmt.Errorf("%w: missing OPA dependency pin", ErrStep)
	}
	var replacement *modfile.Replace
	for _, candidate := range module.Replace {
		if candidate.Old.Path != opaModulePath {
			continue
		}
		if candidate.Old.Version == expected {
			replacement = candidate
			break
		}
		if candidate.Old.Version == "" {
			replacement = candidate
		}
	}
	if replacement != nil {
		expected = replacement.New.Version
		if expected == "" {
			return "", fmt.Errorf("%w: OPA replacement has no release version", ErrStep)
		}
	}
	var response struct {
		Version string `json:"openPolicyAgentVersion"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("decode runtime version: %w", err)
	}
	if response.Version != expected {
		return "", fmt.Errorf("%w: runtime reports %q, linked pin %q", ErrStep, response.Version, expected)
	}
	return response.Version, nil
}
