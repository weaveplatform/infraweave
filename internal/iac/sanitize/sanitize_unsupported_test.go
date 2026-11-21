package sanitize_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/sanitize"
)

// Authored released-shape payloads demonstrate the privacy boundary. They are
// not tool-produced fixtures and do not settle their masks or policy projection.
func TestUnitUnsupportedPlanFamilies(t *testing.T) {
	const secret = "dummy-unsupported-642"
	families := map[string]string{
		"deferred_action_invocations": `[{"reason":"resource_config_unknown","future_values":{"token":"` + secret + `"},"action_invocation":{"address":"action.example.send","config_values":{"token":"` + secret + `"},"config_sensitive":{"token":true}}}]`,
	}
	for name, payload := range families {
		for _, envelope := range []bool{false, true} {
			raw := `{"format_version":"1.2","` + name + `":` + payload + `}`
			if envelope {
				raw = `{"units":[{"path":".","plan":{"format_version":"1.2"}},{"path":"child","plan":` + raw + `}]}`
			}
			for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
				var out bytes.Buffer
				err := sanitize.Plan(&out, strings.NewReader(raw), mode)
				if !errors.Is(err, sanitize.ErrUnsupported) || out.Len() != 0 || strings.Contains(err.Error(), secret) {
					t.Fatalf("%s/%s did not fail closed without values: %v, %d bytes", name, mode, err, out.Len())
				}
			}
		}
		for _, payload := range []string{`[]`, `null`} {
			raw := `{"format_version":"1.2","` + name + `":` + payload + `}`
			for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
				var out bytes.Buffer
				if err := sanitize.Plan(&out, strings.NewReader(raw), mode); err != nil || !json.Valid(out.Bytes()) {
					t.Fatalf("empty %s was refused: %v", name, err)
				}
			}
		}
		for _, payload := range []string{`"` + secret + `"`, `{"token":"` + secret + `"}`, `[null]`} {
			raw := `{"format_version":"1.2","` + name + `":` + payload + `}`
			for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
				var out bytes.Buffer
				if err := sanitize.Plan(&out, strings.NewReader(raw), mode); !errors.Is(err, sanitize.ErrMalformed) || out.Len() != 0 || strings.Contains(err.Error(), secret) {
					t.Fatalf("malformed %s/%s leaked or passed: %v", name, mode, err)
				}
			}
		}
	}
}

// The owned Terragrunt wrapper must not divert the full-document transform
// while preserving an extra plaintext tree alongside units or unit metadata.
func TestUnitOwnedUnitsEnvelope(t *testing.T) {
	for _, raw := range []string{
		`{"units":[],"format_version":"1.2","variables":{"x":{"value":"dummy-unsupported-642"}}}`,
		`{"units":[{"path":".","plan":{"format_version":"1.2"},"extra":{"token":"dummy-unsupported-642"}}]}`,
	} {
		for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
			var out bytes.Buffer
			if err := sanitize.Plan(
				&out,
				strings.NewReader(raw),
				mode,
			); !errors.Is(err, sanitize.ErrMalformed) || out.Len() != 0 ||
				strings.Contains(err.Error(), "dummy-unsupported-642") {
				t.Fatalf("ambiguous wrapper accepted: %v", err)
			}
		}
	}
}
