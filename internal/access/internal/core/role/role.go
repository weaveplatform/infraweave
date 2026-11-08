package role

import (
	"slices"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The system roles' slugs (decision 0007).
const (
	SpaceReader           = "space-reader"
	SpaceWriter           = "space-writer"
	SpaceAdmin            = "space-admin"
	WorkerPoolController  = "worker-pool-controller"
	DriftDetectionManager = "drift-detection-manager"
)

// Role is a named set of actions an account binds in its spaces. A system
// role is seeded with the account and never changes; its actions are not
// stored but computed from the catalogue, so a catalogue refresh changes
// every account's at once.
type Role struct {
	AccountID   kernel.AccountID
	ID          kernel.ULID
	Slug        string
	Name        string
	Description string
	System      bool
}

// systemRole is a system role's fixed slug, name and description, and
// which catalogue rows it holds.
type systemRole struct {
	slug, name, description string
	actions                 func(accessapi.ActionInfo) bool
}

// system is the system roles in seeding order.
var system = []systemRole{
	{
		SpaceReader, "Space reader", "Every assignable action the reader level grants.",
		within(accessapi.FallbackReader),
	},
	{
		SpaceWriter, "Space writer", "Every assignable action the writer level grants.",
		within(accessapi.FallbackWriter),
	},
	{
		SpaceAdmin, "Space admin", "Every assignable action.",
		within(accessapi.FallbackAdmin),
	},
	{
		WorkerPoolController,
		"Worker pool controller",
		"Reads the space and manages its worker pools.",
		named(accessapi.ActionSpaceRead, accessapi.ActionWorkerPoolCreate,
			accessapi.ActionWorkerPoolUpdate, accessapi.ActionWorkerPoolDelete),
	},
	{
		DriftDetectionManager,
		"Drift detection manager",
		"Reads the space and manages its drift detection integrations.",
		named(
			accessapi.ActionSpaceRead,
			accessapi.ActionDriftDetectionIntegrationCreate,
			accessapi.ActionDriftDetectionIntegrationUpdate,
			accessapi.ActionDriftDetectionIntegrationDelete,
		),
	},
}

func within(level accessapi.Fallback) func(accessapi.ActionInfo) bool {
	return func(a accessapi.ActionInfo) bool { return a.Fallback.Within(level) }
}

func named(actions ...accessapi.Action) func(accessapi.ActionInfo) bool {
	return func(a accessapi.ActionInfo) bool { return slices.Contains(actions, a.Action) }
}

// Seed is the system roles a new account starts with, each with a new ID.
func Seed(acct kernel.AccountID) []Role {
	out := make([]Role, 0, len(system))
	for _, r := range system {
		out = append(out, Role{
			AccountID: acct, ID: kernel.NewULID(), Slug: r.slug, Name: r.name,
			Description: r.description, System: true,
		})
	}
	return out
}

// SystemActions is the system role slug's actions in catalogue order,
// each assignable; ok is false for a slug no system role has.
func SystemActions(slug string) (actions []accessapi.Action, ok bool) {
	i := slices.IndexFunc(system, func(r systemRole) bool { return r.slug == slug })
	if i < 0 {
		return nil, false
	}
	for _, a := range accessapi.Actions() {
		if a.Assignable() && system[i].actions(a) {
			actions = append(actions, a.Action)
		}
	}
	return actions, true
}
