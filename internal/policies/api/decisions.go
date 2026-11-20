//nolint:tagliatelle // Notification routes retain the published policy rule names.
package api

import "encoding/json"

// PushDecision retains directives for the VCS caller. FailedCheck requests the
// fail-closed status even though an evaluation error creates no run.
type PushDecision struct {
	Track, Propose, Ignore, IgnoreTrack, NoTrigger bool
	Notify, Fail, AllowFork, FailedCheck           bool
	Cancel, Messages, ExternalDependencies         []string
	Lock, Unlock, ModuleVersion, Priority          string
}

// TriggerDecision contains only requested targets; their account, reach and
// deduplication checks belong to the trigger consumer.
type TriggerDecision struct {
	Targets  []string
	Priority string
}

// LoginDecision contains grants before the access domain's owner/admin escape
// hatches. Teams is the last nonempty rewrite in immutable policy-ID order.
type LoginDecision struct {
	Allow, Admin, Deny, DenyAdmin     bool
	Message                           string
	Messages, Teams                   []string
	Roles                             map[string][]string
	SpaceAdmin, SpaceWrite, SpaceRead []string
}

// NegativeDecision carries deny reasons for TASK, INITIALIZATION and INTENT.
// Intent also retains allow reasons while an undecided operation awaits review.
type NegativeDecision struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
}

// NotificationDecision describes routes without delivering them. Optional
// values remain pointers so an omitted field does not turn into an explicit
// false, zero or empty customization at the later notification router.
type NotificationDecision struct {
	Inbox       []InboxMessage       `json:"inbox"`
	Slack       []SlackMessage       `json:"slack"`
	Webhook     []WebhookMessage     `json:"webhook"`
	PullRequest []PullRequestMessage `json:"pull_request"`
}

type InboxMessage struct {
	Title    *string `json:"title,omitempty"`
	Body     *string `json:"body,omitempty"`
	Severity *string `json:"severity,omitempty"`
}

type SlackMessage struct {
	ChannelID     string   `json:"channel_id"`
	Message       *string  `json:"message,omitempty"`
	MentionUsers  []string `json:"mention_users,omitempty"`
	MentionGroups []string `json:"mention_groups,omitempty"`
	ThreadKey     *string  `json:"thread_key,omitempty"`
}

type WebhookMessage struct {
	EndpointID string            `json:"endpoint_id"`
	Headers    map[string]string `json:"headers,omitempty"`
	Payload    json.RawMessage   `json:"payload,omitempty"`
	Method     *string           `json:"method,omitempty"`
}

type PullRequestMessage struct {
	ID               *int64  `json:"id,omitempty"`
	Commit           *string `json:"commit,omitempty"`
	Branch           *string `json:"branch,omitempty"`
	Body             *string `json:"body,omitempty"`
	DeduplicationKey *string `json:"deduplication_key,omitempty"`
	TruncateBody     *bool   `json:"truncate_body,omitempty"`
}
