package port

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

var ErrCheckInput = errors.New("vcs: invalid commit check input")

// CheckPolicy is plain integration metadata; selecting feedback never opens
// the credential that a queued provider effect uses later.
type CheckPolicy struct {
	Mode          string `json:"mode"`
	FailuresOnly  bool   `json:"failuresOnly"`
	SkipUnchanged bool   `json:"skipUnchanged"`
}

// CheckState is the common state of a named commit check. Adapters map neutral,
// canceled and skipped to the closest successful or failed native state when
// their provider cannot represent them directly.
type CheckState string

const (
	CheckQueued     CheckState = "queued"
	CheckInProgress CheckState = "in_progress"
	CheckSuccess    CheckState = "success"
	CheckNeutral    CheckState = "neutral"
	CheckFailure    CheckState = "failure"
	CheckCanceled   CheckState = "canceled"
	CheckSkipped    CheckState = "skipped"
)

// CheckAction is a requested action on an individual check. Only GitHub uses
// these fields; its webhook carries Identifier back to the action consumer.
type CheckAction struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Identifier  string `json:"identifier"`
}

// CheckReport supplies a credential-free desired native check. NativeID is a
// previous provider identifier when recorded; adapters recover an uncertain
// create before posting again. ExternalID remains the stack ID on GitHub.
type CheckReport struct {
	Name         string        `json:"name"`
	State        CheckState    `json:"state"`
	Title        string        `json:"title"`
	Summary      string        `json:"summary"`
	URL          string        `json:"url"`
	ExternalID   string        `json:"externalId"`
	NativeID     string        `json:"nativeId,omitempty"`
	RecoveryID   string        `json:"recoveryId"`
	PullRequests []int         `json:"pullRequests,omitempty"`
	Actions      []CheckAction `json:"actions"`
}

// Checks sets one named commit check and returns its provider identifier when
// available. Only a queued VCS handler calls Set. Each adapter bounds native
// requests to thirty seconds and returns RateLimited without retrying locally.
type Checks interface {
	Set(ctx context.Context, repo Repository, sha string, check CheckReport) (string, error)
}

// CheckHost identifies the provider API's native key space without credentials
// or network calls. Shared status ownership spans integrations on that host.
type CheckHost interface {
	CheckHost(Settings) (string, error)
}

// CheckOrigin normalizes host aliases while preserving private installation
// paths. It rejects userinfo so metadata cannot retain a credential in a URL.
func CheckOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", ErrCheckInput
	}
	u.Host = strings.ToLower(u.Host)
	if u.Scheme == "https" && u.Port() == "443" || u.Scheme == "http" && u.Port() == "80" {
		host := u.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		u.Host = host
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawPath = strings.TrimSuffix(u.RawPath, "/")
	return u.String(), nil
}
