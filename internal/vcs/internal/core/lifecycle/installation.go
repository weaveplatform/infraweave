package lifecycle

import (
	"time"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

// nextInstallation distinguishes an ID merely observed before activation from a
// genuinely superseded ID whose late jobs cannot affect the current owner.
func nextInstallation(all []Installation, event vcsapi.Installation, at time.Time) (Installation, bool) {
	next := Installation{ID: event.ID, Owner: event.Owner, State: "active", Current: true, ObservedAt: at}
	known, newer := false, false
	var current *Installation
	for _, in := range all {
		if in.ID == event.ID {
			if in.Superseded {
				return Installation{}, false
			}
			if in.Current && in.ObservedAt.After(at) {
				return in, true
			}
			next.Current, next.State = in.Current, in.State
			known = true
			if in.ObservedAt.After(at) {
				next.Owner, next.ObservedAt = in.Owner, in.ObservedAt
				newer = true
			}
		}
		if in.Current && in.Owner.ID == event.Owner.ID {
			current = &in
		}
	}
	activates := event.Action == "created" || event.Action == "unsuspend" || event.Action == "new_permissions_accepted"
	if !known || !next.Current {
		if current != nil && current.ObservedAt.After(next.ObservedAt) {
			return Installation{}, false
		}
		next.Current = activates || current == nil
	}
	if !newer {
		switch event.Action {
		case "created", "unsuspend", "new_permissions_accepted":
			next.State = "active"
		case "suspend":
			next.State = "suspended"
		case "deleted":
			next.State = "deleted"
		}
	}
	return next, true
}
