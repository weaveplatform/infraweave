package port

import "github.com/weaveplatform/infraweave/internal/kernel"

// PushFeedback is a no-run decision's public check content. It contains no
// policy source, input, print output, credentials or private sample handoff.
type PushFeedback struct {
	Account                      kernel.AccountID
	Delivery, Integration, Stack kernel.ULID
	Commit                       string
	Type                         kernel.RunType
	Failed                       bool
	Messages, Policies           []string
}
