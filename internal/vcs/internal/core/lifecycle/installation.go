package lifecycle

import (
	"time"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

// nextInstallation retains superseded IDs without letting their late jobs
// change the replacement installation or remove its repository map entries.
func nextInstallation(all []Installation, event vcsapi.Installation, at time.Time) (Installation, bool) {
	next := Installation{ID: event.ID, Owner: event.Owner, State: "active", Current: true, ObservedAt: at}
	known := false
	var current *Installation
	for _, in := range all {
		if in.ID == event.ID {
			if !in.Current {
				return Installation{}, false
			}
			if in.ObservedAt.After(at) {
				return in, true
			}
			next.State = in.State
			known = true
		}
		if in.Current && in.Owner.ID == event.Owner.ID {
			current = &in
		}
	}
	if !known && current != nil {
		if current.ObservedAt.After(at) {
			return Installation{}, false
		}
		switch event.Action {
		case "created", "unsuspend", "new_permissions_accepted":
		default:
			next.Current = false
		}
	}
	switch event.Action {
	case "created", "unsuspend", "new_permissions_accepted":
		next.State = "active"
	case "suspend":
		next.State = "suspended"
	case "deleted":
		next.State = "deleted"
	}
	return next, true
}
