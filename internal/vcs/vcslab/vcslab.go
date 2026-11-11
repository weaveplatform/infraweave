package vcslab

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// The lab history's refs and commits. Base is no ref's tip; Tag is an
// annotated tag of Tagged; Head is Branch's head and Dev is DevBranch's,
// one commit on from Head. seed.sh's header draws the history.
const (
	Branch    = "main"
	DevBranch = "dev"
	Tag       = "v1"

	Base   = "a82986037786a1fbed414ff64bf61eb8043bb0c6"
	Tagged = "87e64426352f1562e4cc55651f47693d5f24b8e8"
	Head   = "78868ef45ef5734560f520ba30bae54487b303e5"
	Dev    = "b3749a273800a0a80bc6c325c6f3f0db1e16e233"

	// File holds Content at Head.
	File    = "main.tf"
	Content = "# head\n"
)

// Changed are the paths Base and Head differ in, sorted.
func Changed() []string { return []string{"main.tf", "modules/net/main.tf"} }

// The variables through which the vcs-lab job names one provider's lab.
const (
	// EnvHost is the API host, or Azure DevOps's organization URL.
	EnvHost = "INFRAWEAVE_TEST_VCS_HOST"
	// EnvUser is the user the token belongs to: the Atlassian account's
	// e-mail on Bitbucket Cloud, the user on Bitbucket Data Center, the
	// App's ID on GitHub.
	EnvUser = "INFRAWEAVE_TEST_VCS_USER"
	// EnvToken is the integration's credential: an access token, or the
	// GitHub App's private key.
	EnvToken = "INFRAWEAVE_TEST_VCS_TOKEN"
	// EnvNamespace holds both repositories: an organization, group,
	// workspace or project.
	EnvNamespace = "INFRAWEAVE_TEST_VCS_NAMESPACE"
	// EnvRepository is the lab repository, and EnvOther a second one the
	// integration reaches and a run's token must not.
	EnvRepository = "INFRAWEAVE_TEST_VCS_REPOSITORY"
	EnvOther      = "INFRAWEAVE_TEST_VCS_OTHER"
	// EnvCAFile is a PEM file of the certificates the host's chain ends
	// in, for a host the system does not trust; it is optional.
	EnvCAFile = "INFRAWEAVE_TEST_VCS_CA_FILE"
)

// Lab is one provider's lab, as the variables name it.
type Lab struct {
	Host, User, Token, Namespace, Repository, Other, CAFile string
}

// ErrNotConfigured reports a required variable that is unset.
var ErrNotConfigured = errors.New("vcslab: the vcs-lab lab is not configured")

// FromEnv reads the lab, failing with ErrNotConfigured when any of need,
// which are Env names, is unset.
func FromEnv(need ...string) (Lab, error) {
	var missing []string
	for _, v := range need {
		if os.Getenv(v) == "" {
			missing = append(missing, v)
		}
	}
	if len(missing) > 0 {
		return Lab{}, fmt.Errorf("%w: %s unset", ErrNotConfigured, strings.Join(missing, ", "))
	}
	return Lab{
		Host:       os.Getenv(EnvHost),
		User:       os.Getenv(EnvUser),
		Token:      os.Getenv(EnvToken),
		Namespace:  os.Getenv(EnvNamespace),
		Repository: os.Getenv(EnvRepository),
		Other:      os.Getenv(EnvOther),
		CAFile:     os.Getenv(EnvCAFile),
	}, nil
}
