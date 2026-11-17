//go:build unix

package exec_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/buildinfo"
)

// TestUnitComputedEnvironmentNames checks the runner's names against the
// shared view and observes their actual values in an initialization hook.
func TestUnitComputedEnvironmentNames(t *testing.T) {
	names := []string{kernel.APIEndpointVariable, kernel.RunStateVariable, kernel.APITokenVariable}
	for _, name := range names {
		if !slices.Contains(kernel.ComputedEnvironmentNames(), name) {
			t.Errorf("runner variable %s is absent from the computed view", name)
		}
	}
	w := newWorld(t)
	w.h.Endpoint = "https://relay/worker/v1"
	w.h.Spec.Hooks["before_init"] = []string{hook("computed", names...)}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(w.traced(), "computed https://relay/api/v1 INITIALIZING api-token") {
		t.Errorf("computed values in phase: %q", w.traced())
	}
}

func TestUnitExecRunEnvironment(t *testing.T) {
	for _, c := range []struct {
		name      string
		download  bool
		sensitive bool
		overrides bool
	}{
		{name: "image binary and no sensitive upload"},
		{name: "cached binary and sensitive upload", download: true, sensitive: true},
		{name: "stack variables replace defaults", download: true, sensitive: true, overrides: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.w.autodeploy = true
			w.h.Endpoint = "https://relay/p/worker/v1"
			w.h.Spec.Tool.Download = c.download
			w.h.SensitiveOutputUpload = c.sensitive
			w.h.MaxRequestsPerSecond, w.h.MaxRequestsBurst = 21, 210
			w.h.Spec.StateBackend = &wire.RunSpecStateBackend{Path: "/state/v1/stacks/infra"}
			names := []string{
				"TF_APPEND_USER_AGENT", "TF_VAR_infraweave_api_endpoint",
				"INFRAWEAVE_MAX_REQUESTS_PER_SECOND", "INFRAWEAVE_MAX_REQUESTS_BURST",
				"INFRAWEAVE_SENSITIVE_OUTPUT_UPLOAD_ENABLED", "INFRAWEAVE_VENDOR_BINARY_CACHED",
				"INFRAWEAVE_API_TOKEN", "TF_HTTP_USERNAME", "TF_HTTP_PASSWORD",
			}
			values := []string{
				"infraweave/" + buildinfo.Version(), "https://relay/p/api/v1", "21", "210",
				strconv.FormatBool(c.sensitive), strconv.FormatBool(c.download),
				"api-token", "run", "run-token",
			}
			for i, name := range names[:6] {
				w.environ = append(w.environ, name+"=container")
				if c.overrides {
					value := "stack-" + name
					w.h.Spec.Environment = append(w.h.Spec.Environment,
						wire.RunSpecVariable{Name: name, Value: &value})
					values[i] = value
				}
			}
			for _, name := range []string{"INFRAWEAVE_API_TOKEN", "TF_HTTP_USERNAME", "TF_HTTP_PASSWORD"} {
				forged := "forged"
				w.h.Spec.Environment = append(w.h.Spec.Environment,
					wire.RunSpecVariable{Name: name, Value: &forged})
			}
			want := "environment " + strings.Join(values, " ")
			for _, phase := range []string{"before_init", "before_plan", "before_apply", "after_run"} {
				w.h.Spec.Hooks[phase] = []string{hook("environment", names...)}
			}
			if err := w.run(); err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, line := range w.traced() {
				if strings.HasPrefix(line, "environment ") {
					got = append(got, line)
				}
			}
			if !slices.Equal(got, []string{want, want, want, want}) {
				t.Errorf("phase environments: %q; want four copies of %q", got, want)
			}
		})
	}
}
