package core

import (
	"encoding/json"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Side is which phases an attachment's credentials serve: Read for
// planning phases and proposed runs, Write for applying, tasks, destroy
// runs and module test runs.
type Side string

const (
	Read  Side = "read"
	Write Side = "write"
)

// ExternalID is the external ID role assumption passes for integration
// on the stack or module whose slug target is, for side, in account's
// account: <account slug>@<integration ULID>@<target slug>@<side>. Its
// segments are what a trust policy scopes on, so it is fixed, not
// configuration.
//
// ref: decision 0006 "AWS external ID and session name"
func ExternalID(account string, integration kernel.ULID, target string, side Side) string {
	return account + "@" + integration.String() + "@" + target + "@" + string(side)
}

// LegacyExternalID is the external ID of a stack's legacy AWS role, for
// both sides: <account slug>@<stack slug>.
//
// ref: decision 0006 "AWS external ID and session name"
func LegacyExternalID(account, stack string) string { return account + "@" + stack }

// MaxSessionName is the longest RoleSessionName STS accepts.
const MaxSessionName = 64

// SessionName is the RoleSessionName of an assumption for run on the
// stack or module whose slug target is, in account's account:
// <run ULID>@<target slug>@<account slug>, cut to MaxSessionName. CloudTrail
// shows it as the session's user, so the run leads.
//
// ref: decision 0006 "AWS external ID and session name"
func SessionName(run kernel.ULID, target, account string) string {
	name := run.String() + "@" + target + "@" + account
	if len(name) > MaxSessionName {
		name = name[:MaxSessionName]
	}
	return name
}

// statement is an IAM trust policy statement.
type statement struct {
	Effect    string                    `json:"Effect"`    //nolint:tagliatelle // IAM's spelling
	Principal map[string]string         `json:"Principal"` //nolint:tagliatelle // as above
	Action    string                    `json:"Action"`    //nolint:tagliatelle // as above
	Condition map[string]map[string]any `json:"Condition"` //nolint:tagliatelle // as above
}

// PolicyStatement is the trust policy statement that lets the AWS account
// installation assume a role with one of externalIDs, as indented JSON:
// one ID's condition is a string, several IDs' a list.
func PolicyStatement(installation string, externalIDs ...string) string {
	var id any = externalIDs
	if len(externalIDs) == 1 {
		id = externalIDs[0]
	}
	// Every member is a string, a map of strings or a list of strings, so
	// the statement always encodes.
	b, _ := json.MarshalIndent(statement{
		Effect:    "Allow",
		Principal: map[string]string{"AWS": installation},
		Action:    "sts:AssumeRole",
		Condition: map[string]map[string]any{"StringEquals": {"sts:ExternalId": id}},
	}, "", "  ")
	return string(b)
}
