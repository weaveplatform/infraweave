package app

import (
	"errors"
	"reflect"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
)

func TestUnitOperationMultipleOptionValues(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ext     map[string]any
		invalid bool
	}{
		{"both check modes", map[string]any{"values": []any{"AGGREGATED", "ALL"}}, false},
		{"empty values", map[string]any{"values": []any{}}, true},
		{"scalar values", map[string]any{"values": "ALL"}, true},
		{"nonstring item", map[string]any{"values": []any{"ALL", true}}, true},
		{"empty item", map[string]any{"values": []any{""}}, true},
		{"ambiguous conditions", map[string]any{"value": "ALL", "values": []any{"AGGREGATED"}}, true},
		{"nonstring singular", map[string]any{"value": true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.ext["feature"] = "aggregated-vcs-checks"
			prop := openapi3.NewStringSchema()
			prop.Extensions = map[string]any{"x-infraweave-feature": tc.ext}
			schema := openapi3.NewObjectSchema().WithProperty("vcsChecks", prop)
			doc := &openapi3.T{Paths: openapi3.NewPaths(openapi3.WithPath("/integrations", &openapi3.PathItem{Post: &openapi3.Operation{
				Extensions: map[string]any{
					"x-infraweave-feature": "vcs-integrations",
				},
				RequestBody: &openapi3.RequestBodyRef{Value: openapi3.NewRequestBody().WithJSONSchema(schema)},
			}}))}
			ops, err := operationsOf(doc, "", false)
			if tc.invalid {
				if !errors.Is(err, errFeature) {
					t.Fatal("invalid condition mark silently weakened its gate", err)
				}
				return
			}
			want := []featuresapi.OptionGate{
				{Pointer: "/vcsChecks", Value: "AGGREGATED", Feature: "aggregated-vcs-checks"},
				{Pointer: "/vcsChecks", Value: "ALL", Feature: "aggregated-vcs-checks"},
			}
			if err != nil || !reflect.DeepEqual(ops["POST /integrations"].Gate.Options, want) {
				t.Fatal("both gated values were not retained", ops, err)
			}
		})
	}
}

func TestUnitIntegrationCheckModeControls(t *testing.T) {
	ops := apiOperations()
	want := []featuresapi.OptionGate{
		{Pointer: "/customInput/vcsChecks", Feature: "aggregated-vcs-checks", Value: "AGGREGATED"},
		{Pointer: "/customInput/vcsChecks", Feature: "aggregated-vcs-checks", Value: "ALL"},
	}
	for _, path := range []string{
		"/gitlab-integrations/{gitlabIntegrationId}",
		"/github-enterprise-integrations/{githubEnterpriseIntegrationId}",
		"/bitbucket-cloud-integrations/{bitbucketCloudIntegrationId}",
		"/bitbucket-datacenter-integrations/{bitbucketDatacenterIntegrationId}",
		"/azure-dev-ops-repo-integrations/{azureDevOpsRepoIntegrationId}",
	} {
		for _, method := range []string{"PUT", "PATCH"} {
			key := method + " " + BaseURL + path
			op, ok := ops[key]
			if !ok || !reflect.DeepEqual(op.Gate.Options, want) {
				t.Fatal("provider update bypassed one aggregate mode", key, op.Gate.Options)
			}
		}
	}
}
