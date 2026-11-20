package push

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
)

type presentPushSelection struct{}

func (presentPushSelection) HasPolicies(context.Context, policyapi.SelectionRequest) (bool, error) {
	return true, nil
}

func TestUnitInitialRequestPreparationKeepsNativeErrorsPrivate(t *testing.T) {
	w := newEvaluation(t)
	w.s.d.Stacks = &repositoryStacks{rows: []stacksapi.Stack{{ID: kernel.NewULID()}}}
	w.s.d.PolicySelection = presentPushSelection{}
	w.s.d.Requests = failedRequestSource{cause: fmt.Errorf("private-source-marker: %w", context.Canceled)}
	_, err := w.s.prepareRequest(t.Context(), intake.Delivery{Account: w.input.Account}, vcsapi.Event{Type: "comment", Comment: &vcsapi.Comment{PullRequest: 12}}, stacksapi.Use{})
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "private-source-marker") {
		t.Fatal("native preparation leaked into queue errors", err)
	}
}
