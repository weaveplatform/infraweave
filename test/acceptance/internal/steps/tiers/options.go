package tiers

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

type optionTier struct {
	Name     string   `json:"name"`
	Features []string `json:"features"`
}

func (p *platformSteps) optionCatalogue(ctx context.Context, tok, replica string) ([]optionTier, error) {
	var tiers []optionTier
	path := "/operator/v1/tiers"
	for {
		r, err := p.ok(ctx, replica, tok, http.MethodGet, path, nil, http.StatusOK)
		if err != nil {
			return nil, err
		}
		var page struct {
			Items      []optionTier `json:"items"`
			NextCursor *string      `json:"nextCursor"`
		}
		if err := json.Unmarshal(r.Body, &page); err != nil {
			return nil, fmt.Errorf("%w: tier catalogue: %w", harness.ErrStep, err)
		}
		tiers = append(tiers, page.Items...)
		if page.NextCursor == nil {
			return tiers, nil
		}
		path = "/operator/v1/tiers?cursor=" + url.QueryEscape(*page.NextCursor)
	}
}

// chooseOptionTier uses the installation's actual seeded catalogue, so
// the scenario does not depend on one release's tier names.
func (p *platformSteps) chooseOptionTier(ctx context.Context, tok, capability, which, option, replica string) error {
	tiers, err := p.optionCatalogue(ctx, tok, replica)
	if err != nil {
		return err
	}
	var choice string
	for _, tier := range tiers {
		if slices.Contains(tier.Features, capability) && slices.Contains(tier.Features, option) == (which == "holding") {
			choice = tier.Name
			break
		}
	}
	if choice == "" {
		return fmt.Errorf("%w: no seeded tier with %s %s %s", harness.ErrStep, capability, which, option)
	}
	if err := p.setTier(ctx, tok, choice, replica); err != nil {
		return err
	}
	// Both replicas must observe the operator mutation before the next
	// create, so the assertion cannot depend on notification timing.
	for _, target := range []string{"A", "B"} {
		if err := deps.Eventually(ctx, "tier "+choice+" on "+target, func(ctx context.Context) (bool, error) {
			r, err := p.w.Do(ctx, target, "h", http.MethodGet, "/api/v1/tier", nil)
			var tier optionTier
			return r.Status == http.StatusOK && json.Unmarshal(r.Body, &tier) == nil && tier.Name == choice, err
		}); err != nil {
			return err
		}
	}
	fmt.Printf("acceptance: billed VCS tier %s %s %s observed on A and B\n", choice, which, option)
	return nil
}

func (p *platformSteps) optionProblemTiers(ctx context.Context, option string) error {
	r := p.w.Last()
	var refused struct {
		Feature string   `json:"feature"`
		Tiers   []string `json:"tiers"`
	}
	if r.Status != http.StatusForbidden || json.Unmarshal(r.Body, &refused) != nil || refused.Feature != option {
		return fmt.Errorf("%w: no %s option refusal: %d %s", harness.ErrStep, option, r.Status, r.Body)
	}
	tiers, err := p.optionCatalogue(ctx, "op", "A")
	if err != nil {
		return err
	}
	slices.SortFunc(tiers, func(a, b optionTier) int {
		return cmp.Or(cmp.Compare(len(a.Features), len(b.Features)), strings.Compare(a.Name, b.Name))
	})
	var want []string
	for _, tier := range tiers {
		if slices.Contains(tier.Features, option) {
			want = append(want, tier.Name)
		}
	}
	if len(want) == 0 || !slices.Equal(refused.Tiers, want) {
		return fmt.Errorf("%w: refusal tiers %v, catalogue tiers %v", harness.ErrStep, refused.Tiers, want)
	}
	fmt.Printf("acceptance: VCS option refusal %s\n", r.Body)
	return nil
}
