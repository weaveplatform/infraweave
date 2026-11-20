package vcs

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func (s *eventSteps) policyFork() error {
	c := s.comments
	c.sourceNamespace, c.sourceRepository = "contributor", "infra-fork"
	var repo checkRepository
	peerSeed := false
	switch c.fixture.provider {
	case "GitHub":
		c.github.fake.SetPublicRepository(c.sourceNamespace, c.sourceRepository, true)
		repo = c.github.fake.Repo(c.sourceNamespace, c.sourceRepository)
	case "GitLab":
		c.gitlab.fake.AddMember(c.sourceNamespace, c.sourceRepository, "comments", gitlabfake.Developer)
		repo = c.gitlab.fake.Repo(c.sourceNamespace, c.sourceRepository)
	case "Bitbucket Cloud":
		peerSeed = true
		c.peers.cloud.fake.Grant(c.sourceNamespace, c.sourceRepository, "checks@example.com", bitbucketcloudfake.Read)
		repo = c.peers.cloud.fake.Repo(c.sourceNamespace, c.sourceRepository)
	case "Bitbucket DC":
		peerSeed = true
		c.peers.dc.fake.Grant(c.sourceNamespace, c.sourceRepository, "checks", bitbucketdcfake.Read)
		repo = c.peers.dc.fake.Repo(c.sourceNamespace, c.sourceRepository)
	case "Azure DevOps":
		peerSeed = true
		c.peers.azure.fake.Grant(c.sourceNamespace, "checks", azuredevopsfake.Reader)
		repo = c.peers.azure.fake.Repo(c.sourceNamespace, c.sourceRepository)
	default:
		return fmt.Errorf("%w: fork provider is unavailable", harness.ErrStep)
	}
	// A fork can share its tip with its parent. Reproduce the identical Git
	// history in a separately identified repository; this tests the policy's
	// identity boundary without claiming a unique fork-tip worker checkout.
	if peerSeed {
		repo.Commit("main", "commit check seed", deploymentFiles("one"))
	}
	repo.Commit("main", "comment base", deploymentFiles("base"))
	if head := repo.Commit("main", "comment seed", deploymentFiles("one")); head != c.w.IDs["seed"] {
		return fmt.Errorf("%w: fork does not contain the original seed commit", harness.ErrStep)
	}
	repo.Branch("feature", c.w.IDs["seed"])
	return nil
}

func (s *eventSteps) nativeFork(ctx context.Context, name string) error {
	var frozen bool
	err := s.comments.w.Row(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM vcs_prepared_events p JOIN vcs_deliveries d ON d.account_id=p.account_id AND d.id=p.delivery_id WHERE d.account_id=$1 AND d.integration_id=$2 AND d.delivery_id=$3 AND p.event#>>'{request,snapshot,request,fork}'='true' AND p.event#>>'{request,snapshot,headRepository,id}'<>p.event#>>'{request,snapshot,baseRepository,id}' AND p.event#>>'{request,snapshot,head}'=$4)`,
		[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(s.comments.fixture.integration), s.deliveries[name], s.comments.w.IDs["seed"]},
		&frozen,
	)
	if err != nil {
		return err
	}
	if !frozen {
		return fmt.Errorf("%w: native fork identities were not frozen before evaluation", harness.ErrStep)
	}
	return nil
}
