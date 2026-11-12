package acceptance

import (
	"os"
	"testing"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/steps/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/steps/auth"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/steps/authz"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/steps/cli"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/steps/configuration"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/steps/expressions"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/steps/organization"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/steps/platform"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/steps/runs"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/steps/stacks"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/steps/suite"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/steps/tiers"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/steps/vcs"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/steps/vendors"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/steps/workers"
)

// areas register each area's steps on every scenario. No step text
// matches the steps of two areas, so their order changes nothing.
var areas = []func(*godog.ScenarioContext, *harness.World){
	api.Register,
	auth.Register,
	authz.Register,
	organization.Register,
	tiers.Register,
	stacks.Register,
	configuration.Register,
	expressions.Register,
	vcs.Register,
	vendors.Register,
	runs.Register,
	workers.Register,
	platform.Register,
	cli.Register,
	suite.Register,
}

func TestMain(m *testing.M) { os.Exit(harness.MainWithBuild(m)) }

func TestAccFeatures(t *testing.T) { harness.Features(t, areas) }
