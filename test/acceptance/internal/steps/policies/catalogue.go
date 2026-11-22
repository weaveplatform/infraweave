package policies

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

type catalogueCapture struct {
	Pages []struct {
		Response struct {
			Data struct {
				Templates struct {
					Edges []struct {
						Node struct {
							ID   string `json:"id"`
							Slug string `json:"slug"`
						} `json:"node"`
					} `json:"edges"`
				} `json:"searchPolicyTemplates"`
			} `json:"data"`
		} `json:"response"`
	} `json:"pages"`
}

func recordedTemplates() (map[string]string, error) {
	raw, err := os.ReadFile(filepath.Join(harness.RepoRoot, "test", "fixtures", "captures", "C54", "response.json"))
	if err != nil {
		return nil, fmt.Errorf("read captured catalogue: %w", err)
	}
	var capture catalogueCapture
	if err := json.Unmarshal(raw, &capture); err != nil {
		return nil, fmt.Errorf("decode captured catalogue: %w", err)
	}
	rows := map[string]string{}
	for _, page := range capture.Pages {
		for _, edge := range page.Response.Data.Templates.Edges {
			rows[edge.Node.Slug] = edge.Node.ID
		}
	}
	if len(rows) != 62 {
		return nil, fmt.Errorf("%w: captured catalogue has %d members", harness.ErrStep, len(rows))
	}
	return rows, nil
}

func registerCatalogue(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^token "(\w+)" pages all captured policy templates through ([AB])$`, func(ctx context.Context, token, replica string) error {
		expected, err := recordedTemplates()
		if err != nil {
			return err
		}
		ids := map[string]bool{}
		for _, id := range expected {
			ids[id] = true
		}
		seen := map[string]bool{}
		cursor := ""
		for page := 0; page < 10; page++ {
			response, err := w.Do(ctx, replica, token, http.MethodPost, "/api/v1/policy-templates/search", map[string]any{"first": 7, "after": cursor})
			if err != nil {
				return err
			}
			var out api.SearchPolicyTemplatesOutput
			if response.Status != 200 || json.Unmarshal(response.Body, &out) != nil {
				return fmt.Errorf("%w: catalogue page returned %d", harness.ErrStep, response.Status)
			}
			for _, edge := range out.Edges {
				if !ids[edge.Node.Id] || seen[edge.Node.Id] {
					return fmt.Errorf("%w: unserved or repeated template %s", harness.ErrStep, edge.Node.Id)
				}
				seen[edge.Node.Id] = true
			}
			if !out.PageInfo.HasNextPage {
				break
			}
			if len(out.Edges) != 7 || out.PageInfo.EndCursor == "" {
				return fmt.Errorf("%w: incomplete catalogue page/cursor", harness.ErrStep)
			}
			cursor = out.PageInfo.EndCursor
		}
		if len(seen) != 62 {
			return fmt.Errorf("%w: catalogue paging lost %d captured members", harness.ErrStep, 62-len(seen))
		}
		return nil
	})
	sc.Step(`^catalogue searches and suggestions agree for supported types and labels through ([AB])$`, func(ctx context.Context, replica string) error {
		for _, test := range []struct {
			field, kind string
			value       string
			want        int
		}{{"type", "enumEquals", "PLAN", 28}, {"type", "enumEquals", "LOGIN", 0}, {"label", "stringEquals", "private", 1}} {
			in := map[string]any{"predicates": []any{map[string]any{"field": test.field, "constraint": map[string]any{test.kind: []string{test.value}}}}}
			in["first"] = 100
			response, err := w.Do(ctx, replica, "h", http.MethodPost, "/api/v1/policy-templates/search", in)
			if err != nil {
				return err
			}
			var page api.SearchPolicyTemplatesOutput
			if response.Status != 200 || json.Unmarshal(response.Body, &page) != nil || len(page.Edges) != test.want {
				return fmt.Errorf("%w: catalogue %s=%s page mismatch: %d", harness.ErrStep, test.field, test.value, response.Status)
			}
			delete(in, "first")
			response, err = w.Do(ctx, replica, "h", http.MethodPost, "/api/v1/policy-templates/search/suggestions", in)
			if err != nil {
				return err
			}
			var suggestions api.SearchSuggestionsOutput
			if response.Status != 200 || json.Unmarshal(response.Body, &suggestions) != nil || int(suggestions.FilteredCount) != test.want {
				return fmt.Errorf("%w: catalogue facet count differs: %d", harness.ErrStep, response.Status)
			}
		}
		return nil
	})
	sc.Step(
		`^template "([^"]+)" is imported and simulated with '([^']*)' returning "([^"]+)" as '([^']*)' through ([AB])$`,
		func(ctx context.Context, slug, input, field, want, replica string) error {
			rows, err := recordedTemplates()
			if err != nil {
				return err
			}
			id, ok := rows[slug]
			if !ok {
				return fmt.Errorf("%w: unrecorded template slug %s", harness.ErrStep, slug)
			}
			response, err := w.Do(ctx, replica, "h", http.MethodGet, "/api/v1/policy-templates/"+id, nil)
			if err != nil {
				return err
			}
			var template api.PolicyTemplate
			if response.Status != 200 || json.Unmarshal(response.Body, &template) != nil {
				return fmt.Errorf("%w: template read returned %d", harness.ErrStep, response.Status)
			}
			create := map[string]any{
				"name":               "Imported " + slug,
				"type":               template.Type,
				"body":               template.Body,
				"engineType":         template.EngineType,
				"description":        template.Description,
				"labels":             template.Labels,
				"space":              "legacy",
				"policyTemplateULID": id,
			}
			response, err = w.Do(ctx, replica, "h", http.MethodPost, "/api/v1/policies", create)
			if err != nil {
				return err
			}
			var stored api.Policy
			if response.Status != 201 || json.Unmarshal(response.Body, &stored) != nil || stored.Body != template.Body || stored.EngineType != template.EngineType ||
				stored.Type != template.Type {
				return fmt.Errorf("%w: template import lost source/dialect/type: %d", harness.ErrStep, response.Status)
			}
			var origin string
			if err := w.Row(
				ctx,
				`SELECT template_origin FROM policies WHERE account_id=$1 AND id=$2`,
				[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(stored.Id)},
				&origin,
			); err != nil {
				return err
			}
			if origin != id {
				return fmt.Errorf("%w: template origin not retained", harness.ErrStep)
			}
			simulation := api.PolicySimulateInput{Type: stored.Type, Body: stored.Body, EngineType: &stored.EngineType, Input: input}
			response, err = w.Do(ctx, replica, "h", http.MethodPost, "/api/v1/policies/simulate", simulation)
			if err != nil {
				return err
			}
			var encoded string
			var decision map[string]json.RawMessage
			var got, expected any
			if response.Status != 200 || json.Unmarshal(response.Body, &encoded) != nil || json.Unmarshal([]byte(encoded), &decision) != nil ||
				json.Unmarshal(decision[field], &got) != nil ||
				json.Unmarshal([]byte(want), &expected) != nil ||
				!reflect.DeepEqual(got, expected) {
				return fmt.Errorf("%w: imported template %s simulation field %s disagrees: %d", harness.ErrStep, slug, field, response.Status)
			}
			return nil
		},
	)
}
