package assembly

import vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"

// Feedback selects and enqueues VCS-owned jobs for the events sink.
func (d *Domain) Feedback() vcsapi.Feedback { return d.feedback }
