package http

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestUnitProviderPatchDefaults(t *testing.T) {
	for _, tt := range []struct{ name, patch, want string }{
		{"omitted", `{}`, `{}`},
		{
			"description and labels", `{"customInput":{"description":null,"labels":null}}`,
			`{"customInput":{"description":"","labels":[]}}`,
		},
		{
			"options member", `{"customInput":{"vcsCheckOptions":{"individualFailuresOnly":null}}}`,
			`{"customInput":{"vcsCheckOptions":{"individualFailuresOnly":false}}}`,
		},
		{
			"whole options", `{"customInput":{"vcsCheckOptions":null}}`,
			`{"customInput":{"vcsCheckOptions":{"individualFailuresOnly":false,"individualSkipUnchanged":false}}}`,
		},
		{
			"mode and checkout", `{"customInput":{"vcsChecks":null,"useGitCheckout":null}}`,
			`{"customInput":{"vcsChecks":"INDIVIDUAL","useGitCheckout":false}}`,
		},
		{"whole custom input", `{"customInput":null}`, `{"customInput":{"description":"","labels":[],"useGitCheckout":false,"vcsChecks":"INDIVIDUAL","vcsCheckOptions":{"individualFailuresOnly":false,"individualSkipUnchanged":false}}}`},
		{"secrets remain removal", `{"privateKey":null,"webhookSecret":null,"privateToken":null,"apiToken":null,"accessToken":null,"personalAccessToken":null}`, `{"privateKey":null,"webhookSecret":null,"privateToken":null,"apiToken":null,"accessToken":null,"personalAccessToken":null}`},
		{
			"named values unchanged", `{"customInput":{"description":"ours","useGitCheckout":true}}`,
			`{"customInput":{"description":"ours","useGitCheckout":true}}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var patch, before, want map[string]any
			for _, input := range []struct {
				raw    string
				target *map[string]any
			}{{tt.patch, &patch}, {tt.want, &want}, {tt.patch, &before}} {
				if err := json.Unmarshal([]byte(input.raw), input.target); err != nil {
					t.Fatal(err)
				}
			}
			if diff := cmp.Diff(want, providerPatch(patch)); diff != "" {
				t.Errorf("defaults (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(before, patch); diff != "" {
				t.Errorf("changed the request (-before +after):\n%s", diff)
			}
		})
	}
}
