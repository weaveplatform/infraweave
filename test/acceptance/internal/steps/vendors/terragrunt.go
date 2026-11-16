package vendors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// terragruntSteps read a Terragrunt stack's vendor settings back, whose
// effective versions the catalogue resolves once its refresh has read
// Terragrunt's index, the last it reads.
type terragruntSteps struct{ w *harness.World }

func (t *terragruntSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^token "(\w+)" eventually reads the stack "([\w-]+)" through ([AB]) with the effective versions '([^']*)'$`,
		t.eventually,
	)
	sc.Step(`^the answer's vendorConfig's (\w+) is '([^']*)'$`, t.member)
}

// vendorConfig is the vendorConfig of the last answer, a stack.
func (t *terragruntSteps) vendorConfig() (map[string]json.RawMessage, error) {
	r := t.w.Last()
	var st struct {
		VendorConfig map[string]json.RawMessage `json:"vendorConfig"`
	}
	if r.Status != http.StatusOK && r.Status != http.StatusCreated ||
		json.Unmarshal(r.Body, &st) != nil || st.VendorConfig == nil {
		return nil, fmt.Errorf("%w: not a stack: %d %s", harness.ErrStep, r.Status, r.Body)
	}
	return st.VendorConfig, nil
}

// member checks the last answer's vendorConfig member name against want
// as JSON, with each {name} resolved.
func (t *terragruntSteps) member(name, want string) error {
	c, err := t.vendorConfig()
	if err != nil {
		return err
	}
	var g, w any
	if json.Unmarshal(c[name], &g) != nil ||
		json.Unmarshal([]byte(t.w.Resolve(want)), &w) != nil || !reflect.DeepEqual(g, w) {
		return fmt.Errorf("%w: vendorConfig's %s is %s, not %s", harness.ErrStep, name, c[name],
			want)
	}
	return nil
}

// eventually reads the stack slug with token name through replica until
// its vendorConfig's effectiveVersions are want.
func (t *terragruntSteps) eventually(ctx context.Context, name, slug, replica, want string) error {
	return deps.Eventually(ctx, "the stack's effective versions",
		func(ctx context.Context) (bool, error) {
			if _, err := t.w.Do(ctx, replica, name, http.MethodGet, "/api/v1/stacks/"+slug,
				nil); err != nil {
				return false, err
			}
			err := t.member("effectiveVersions", want)
			return err == nil, err
		})
}
