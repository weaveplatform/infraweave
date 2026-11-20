package harness

import (
	"strings"
	"testing"
)

func TestUnitPolicyRuntimeVersion(t *testing.T) {
	const module = "module example.test/runtime\nrequire github.com/open-policy-agent/opa v1.22.4\n"
	const current = `{"openPolicyAgentVersion":"v1.22.4"}`
	for _, tc := range []struct {
		name, module, body, want string
	}{
		{"release pin", module, current, "v1.22.4"},
		{"upgrade follows pin", strings.ReplaceAll(module, "v1.22.4", "v1.22.5"), strings.ReplaceAll(current, "v1.22.4", "v1.22.5"), "v1.22.5"},
		{"versioned fork", module + "replace github.com/open-policy-agent/opa => example.test/opa v1.23.0\n", `{"openPolicyAgentVersion":"v1.23.0"}`, "v1.23.0"},
		{"specific replacement wins", module + "replace github.com/open-policy-agent/opa => example.test/opa v1.20.0\nreplace github.com/open-policy-agent/opa v1.22.4 => example.test/opa v1.23.0\n", `{"openPolicyAgentVersion":"v1.23.0"}`, "v1.23.0"},
		{"other version replacement ignored", module + "replace github.com/open-policy-agent/opa v1.20.0 => example.test/opa v1.23.0\n", current, "v1.22.4"},
		{"other module ignored", module + "replace example.test/other => example.test/fork v1.23.0\n", current, "v1.22.4"},
		{"no release pin", "module example.test/runtime\n", current, ""},
		{"invalid module", "require (", current, ""},
		{"local fork", module + "replace github.com/open-policy-agent/opa => ../opa\n", current, ""},
		{"malformed response", module, `{`, ""},
		{"missing field", module, `{}`, ""},
		{"null response", module, `null`, ""},
		{"null version", module, `{"openPolicyAgentVersion":null}`, ""},
		{"wrong type", module, `{"openPolicyAgentVersion":123}`, ""},
		{"historical upstream constant", module, `{"openPolicyAgentVersion":"v1.17.1"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := policyRuntimeVersion([]byte(tc.body), []byte(tc.module))
			if tc.want == "" {
				if err == nil || got != "" {
					t.Fatalf("invalid response/pin accepted: %q, %v", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("version=%q, %v; want%q", got, err, tc.want)
			}
		})
	}
}
