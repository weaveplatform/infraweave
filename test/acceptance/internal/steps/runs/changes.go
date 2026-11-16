package runs

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// plans is the directory of internal/iac's recorded plans, by tool.
const plans = "../../internal/iac/plan/testdata/"

// changeSteps commit a run's changes document as its worker does after
// planning: sealed with the run's artifact key, uploaded through a
// signed URL and committed through the worker API.
type changeSteps struct {
	w *harness.World
}

func (c *changeSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^token "(\w+)" commits the changes of the recorded (terraform|tofu) plan "(\w+)", `+
		`sealed with the artifact key "(\w+)", for the run "(\w+)" of the account "(\w+)" `+
		`in state version (\d+) `+
		`through ([AB])$`, c.commit)
	sc.Step(`^the answer lists the changes of the recorded (terraform|tofu) plan "(\w+)"$`,
		c.lists)
	sc.Step(`^the answer's edges are the changes "([^"]*)"$`, c.edges)
	sc.Step(`^the answer's resources are the changes "([^"]*)"$`, c.resources)
	sc.Step(`^the answer's endCursor is kept as "(\w+)"$`, c.endCursor)
}

// commit commits the changes of the recorded plan name of tool as the
// run kept as run's changes.json in its PLANNING state at state version
// version, with the run token kept as token, through
// harness.RecordArtifact: the account kept as acct is the one the run's
// specification names, and key keeps the artifact key in hex. The
// commit's answer is the last.
func (c *changeSteps) commit(ctx context.Context, token, tool, name, key, run, acct string, version int64, replica string) error {
	doc, err := changesDocument(tool, name)
	if err != nil {
		return err
	}
	artifact, err := hex.DecodeString(c.w.IDs[key])
	if err != nil {
		return fmt.Errorf("%w: the artifact key %q: %w", harness.ErrStep, key, err)
	}
	account, err := kernel.ParseAccountID(c.w.IDs[acct])
	if err != nil {
		return fmt.Errorf("%w: the account %q: %w", harness.ErrStep, acct, err)
	}
	id, err := kernel.ParseULID(c.w.IDs[run])
	if err != nil {
		return fmt.Errorf("%w: the run %q: %w", harness.ErrStep, run, err)
	}
	return c.w.RecordArtifact(ctx, replica, token, account, id, kernel.RunStatePlanning, version,
		"changes.json", artifact, doc)
}

// changesDocument is the changes document of the recorded plan name of
// tool, as a runner writes it from the plan.
func changesDocument(tool, name string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(plans, tool, name+".json"))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	p, err := plan.Parse(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%w: the plan %s/%s: %w", harness.ErrStep, tool, name, err)
	}
	var doc bytes.Buffer
	if err := plan.Write(&doc, p.Changes); err != nil {
		return nil, fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	return doc.Bytes(), nil
}

// v2 is a page of changes-v2 as a step reads it.
type v2 struct {
	Edges []struct {
		Node struct {
			Entity struct {
				ID      string `json:"id"`
				Address string `json:"address"`
				Name    string `json:"name"`
				Parent  string `json:"parent"`
				Type    string `json:"type"`
			} `json:"entity"`
			Phase string `json:"phase"`
			Type  string `json:"type"`
		} `json:"node"`
	} `json:"edges"`
	PageInfo struct {
		EndCursor   string `json:"endCursor"`
		HasNextPage bool   `json:"hasNextPage"`
	} `json:"pageInfo"`
}

// v3 is changes-v3's answer as a step reads it.
type v3 []struct {
	Resources []struct {
		Address          string            `json:"address"`
		PreviousAddress  string            `json:"previousAddress"`
		AttributeChanges []json.RawMessage `json:"attributeChanges"`
		Metadata         plan.Metadata     `json:"metadata"`
	} `json:"resources"`
}

// lists checks that the last answer, of changes-v2 or changes-v3, lists
// every change of the recorded plan name of tool, in its order, as the
// plan's: changes-v3 as one set, each with its previous address and no
// attribute changes, and changes-v2 on one page, each entity split from
// its address.
func (c *changeSteps) lists(tool, name string) error {
	doc, err := changesDocument(tool, name)
	if err != nil {
		return err
	}
	want, err := plan.Read(bytes.NewReader(doc))
	if err != nil {
		return fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	r := c.w.Last()
	if r.Status != http.StatusOK {
		return fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	if bytes.HasPrefix(bytes.TrimSpace(r.Body), []byte("[")) {
		return listsV3(r.Body, want)
	}
	var page v2
	if err := json.Unmarshal(r.Body, &page); err != nil || page.PageInfo.HasNextPage ||
		len(page.Edges) != len(want) {
		return fmt.Errorf("%w: answered %s, want %d changes on one page", harness.ErrStep,
			r.Body, len(want))
	}
	for i, ch := range want {
		n := page.Edges[i].Node
		e, ok := plan.EntityOf(ch.Address)
		if !ok || n.Entity.ID != ch.Address || n.Entity.Address != e.Local() ||
			n.Entity.Name != e.Name || n.Entity.Parent != e.Module || n.Entity.Type != e.Type ||
			n.Phase != "PLAN" || n.Type != string(ch.Metadata.Type) {
			return fmt.Errorf("%w: change %d is %+v, want %+v", harness.ErrStep, i, n, ch)
		}
	}
	return nil
}

// listsV3 checks that body, changes-v3's answer, is want as one set.
func listsV3(body []byte, want []plan.Change) error {
	var sets v3
	if err := json.Unmarshal(body, &sets); err != nil || len(sets) != 1 ||
		len(sets[0].Resources) != len(want) {
		return fmt.Errorf("%w: answered %s, want one set of %d changes", harness.ErrStep, body,
			len(want))
	}
	for i, ch := range want {
		got := sets[0].Resources[i]
		previous := ""
		if ch.PreviousAddress != nil {
			previous = *ch.PreviousAddress
		}
		if got.Address != ch.Address || got.PreviousAddress != previous ||
			got.Metadata != ch.Metadata || got.AttributeChanges == nil ||
			len(got.AttributeChanges) != 0 {
			return fmt.Errorf("%w: change %d is %+v, want %+v", harness.ErrStep, i, got, ch)
		}
	}
	return nil
}

// edges checks that the last answer, a page of changes-v2, lists the
// entities of the comma-separated addresses, in order.
func (c *changeSteps) edges(addresses string) error {
	var page v2
	r := c.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &page) != nil {
		return fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	got := make([]string, len(page.Edges))
	for i, e := range page.Edges {
		got[i] = e.Node.Entity.ID
	}
	want := []string{}
	for a := range strings.SplitSeq(addresses, ",") {
		if a = strings.TrimSpace(a); a != "" {
			want = append(want, a)
		}
	}
	if !slices.Equal(got, want) {
		return fmt.Errorf("%w: the edges are %v, want %v", harness.ErrStep, got, want)
	}
	return nil
}

// resources checks the resources of the last answer, of changes-v3, in
// its order, against want, comma-separated, each its address and its
// change's type, as "terraform_data.this ADD".
func (c *changeSteps) resources(want string) error {
	var sets v3
	r := c.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &sets) != nil {
		return fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	var got []string
	for _, set := range sets {
		for _, res := range set.Resources {
			got = append(got, res.Address+" "+string(res.Metadata.Type))
		}
	}
	if g := strings.Join(got, ", "); g != want {
		return fmt.Errorf("%w: the changes are %q, want %q", harness.ErrStep, g, want)
	}
	return nil
}

// endCursor keeps the last answer's pageInfo.endCursor, a page of
// changes-v2, as name, for a request to name as {name}.
func (c *changeSteps) endCursor(name string) error {
	var page v2
	if json.Unmarshal(c.w.Last().Body, &page) != nil || page.PageInfo.EndCursor == "" {
		return fmt.Errorf("%w: no endCursor in %s", harness.ErrStep, c.w.Last().Body)
	}
	c.w.IDs[name] = page.PageInfo.EndCursor
	return nil
}
