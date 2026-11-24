package configuration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

type runtimeSource struct {
	Element map[string]any `json:"element"`
	Context map[string]any `json:"context"`
}

type runtimeStack struct {
	ID            string           `json:"id"`
	Config        []map[string]any `json:"config"`
	RuntimeConfig []runtimeSource  `json:"runtimeConfig"`
}

// runtimeStack finds a full stack in a direct, list or search response.
func (c *configSteps) runtimeStack(slug string) (runtimeStack, error) {
	var body struct {
		runtimeStack
		Items []runtimeStack `json:"items"`
		Edges []struct {
			Node runtimeStack `json:"node"`
		} `json:"edges"`
	}
	r := c.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &body) != nil {
		return runtimeStack{}, fmt.Errorf("%w: not a stack response: %d %s", harness.ErrStep, r.Status, r.Body)
	}
	views := append([]runtimeStack{body.runtimeStack}, body.Items...)
	for _, edge := range body.Edges {
		views = append(views, edge.Node)
	}
	for _, view := range views {
		if view.ID == slug {
			return view, nil
		}
	}
	return runtimeStack{}, fmt.Errorf("%w: the answer has no full stack %s", harness.ErrStep, slug)
}

// runtimeLists checks every name exactly once in its public order, and
// checks the metadata shared by computed and stored rows: a computed
// token, the in-run API token or the OIDC federation token, is
// write-only.
func (c *configSteps) runtimeLists(slug, names string) error {
	view, err := c.runtimeStack(slug)
	if err != nil {
		return err
	}
	got := make([]string, len(view.RuntimeConfig))
	for i, source := range view.RuntimeConfig {
		e := source.Element
		id, _ := e["id"].(string)
		got[i] = id
		runtime, ok := e["runtime"].(bool)
		if !ok {
			return fmt.Errorf("%w: %s has no runtime flag", harness.ErrStep, id)
		}
		if runtime {
			if e["type"] != "ENVIRONMENT_VARIABLE" || e["category"] != "INFRAWEAVE_ENVIRONMENT" || e["value"] != nil ||
				e["createdAt"] != nil || e["fileMode"] != nil || e["checksum"] != "" ||
				e["description"] != "" || e["writeOnly"] != (id == "INFRAWEAVE_API_TOKEN" || id == "INFRAWEAVE_OIDC_TOKEN") ||
				source.Context != nil {
				return fmt.Errorf("%w: computed metadata of %s: %+v", harness.ErrStep, id, source)
			}
		} else if e["category"] != nil || e["writeOnly"] == true && e["value"] != nil {
			return fmt.Errorf("%w: stored metadata or write-only value of %s: %+v", harness.ErrStep, id, source)
		}
	}
	if want := strings.Split(names, ", "); !reflect.DeepEqual(got, want) {
		return fmt.Errorf("%w: runtime IDs are %v, want %v", harness.ErrStep, got, want)
	}
	return nil
}

// runtimeSources checks selected values and their winning attachments.
// A JSON null accepts both an omitted nullable member and explicit null.
func (c *configSteps) runtimeSources(slug string, table *godog.Table) error {
	view, err := c.runtimeStack(slug)
	if err != nil {
		return err
	}
	byID := map[string]runtimeSource{}
	for _, source := range view.RuntimeConfig {
		id, _ := source.Element["id"].(string)
		byID[id] = source
	}
	for _, row := range table.Rows {
		if len(row.Cells) != 3 {
			return fmt.Errorf("%w: a runtime row needs ID, JSON and source", harness.ErrStep)
		}
		id, expected, origin := row.Cells[0].Value, row.Cells[1].Value, row.Cells[2].Value
		source, ok := byID[id]
		if !ok {
			return fmt.Errorf("%w: runtime config has no %s", harness.ErrStep, id)
		}
		var fields map[string]any
		if err := json.Unmarshal([]byte(c.w.Resolve(expected)), &fields); err != nil {
			return fmt.Errorf("%w: runtime expectation for %s: %w", harness.ErrStep, id, err)
		}
		for name, want := range fields {
			if !reflect.DeepEqual(source.Element[name], want) {
				return fmt.Errorf("%w: %s.%s is %#v, want %#v", harness.ErrStep, id, name, source.Element[name], want)
			}
		}
		if origin == "stack" || origin == "computed" {
			if source.Context != nil {
				return fmt.Errorf("%w: %s unexpectedly has an attachment", harness.ErrStep, id)
			}
			continue
		}
		auto := strings.HasPrefix(origin, "auto:")
		contextID := strings.TrimPrefix(origin, "auto:")
		attachmentID, _ := source.Context["id"].(string)
		if source.Context == nil || attachmentID == "" || source.Context["contextId"] != contextID ||
			source.Context["isAutoattached"] != auto {
			return fmt.Errorf("%w: source of %s is %+v, want %s", harness.ErrStep, id, source.Context, origin)
		}
	}
	return nil
}

func (c *configSteps) keepRuntime(slug, name string) error {
	view, err := c.runtimeStack(slug)
	if err != nil {
		return err
	}
	if c.runtimeViews == nil {
		c.runtimeViews = map[string][]runtimeSource{}
	}
	c.runtimeViews[name] = view.RuntimeConfig
	data, _ := json.Marshal(view.RuntimeConfig)
	fmt.Printf("acceptance: stack %s runtime configuration: %s\n", slug, data)
	return nil
}

func (c *configSteps) sameRuntime(slug, name string) error {
	view, err := c.runtimeStack(slug)
	if err != nil {
		return err
	}
	want, ok := c.runtimeViews[name]
	if !ok || !reflect.DeepEqual(view.RuntimeConfig, want) {
		return fmt.Errorf("%w: stack %s runtime config differs from %s", harness.ErrStep, slug, name)
	}
	return nil
}

func (c *configSteps) storedLists(slug, names string) error {
	view, err := c.runtimeStack(slug)
	if err != nil {
		return err
	}
	got := make([]string, len(view.Config))
	merged := map[string]runtimeSource{}
	for _, source := range view.RuntimeConfig {
		id, _ := source.Element["id"].(string)
		merged[id] = source
	}
	for i, e := range view.Config {
		got[i], _ = e["id"].(string)
		if e["runtime"] != false || e["category"] != nil ||
			!reflect.DeepEqual(e, merged[got[i]].Element) || merged[got[i]].Context != nil {
			return fmt.Errorf("%w: own config has runtime metadata: %+v", harness.ErrStep, e)
		}
	}
	if want := strings.Split(names, ", "); !reflect.DeepEqual(got, want) {
		return fmt.Errorf("%w: own config IDs are %v, want %v", harness.ErrStep, got, want)
	}
	return nil
}
