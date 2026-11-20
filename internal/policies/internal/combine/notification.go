package combine

import (
	"encoding/json"

	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

// Notification validates typed routes but leaves all provider work to P18.
func Notification(receipts []policyapi.Receipt) policyapi.Evaluation {
	out := evaluation(policyapi.Notification, receipts)
	if !out.HasPolicies {
		return out
	}
	out.Notification = &policyapi.NotificationDecision{
		Inbox:       []policyapi.InboxMessage{},
		Slack:       []policyapi.SlackMessage{},
		Webhook:     []policyapi.WebhookMessage{},
		PullRequest: []policyapi.PullRequestMessage{},
	}
	fatal := false
	for i := range out.Receipts {
		r := &out.Receipts[i]
		var d inputs.NotificationDecision
		var routes policyapi.NotificationDecision
		if r.Failure == nil &&
			(r.Type != policyapi.Notification || decode(r.Decision, &d) != nil || anyNull(d.Inbox, d.Slack, d.Webhook, d.PullRequest) || !common(r, d.Flag, d.ResetFlag, d.Sample, false) || !notificationRoutes(d, &routes)) {
			invalid(r, policyapi.Notification)
		}
		if r.Failure != nil {
			failed(&out, r, policyapi.Ignore)
			fatal = true
			continue
		}
		n := out.Notification
		n.Inbox = append(n.Inbox, routes.Inbox...)
		n.Slack = append(n.Slack, routes.Slack...)
		n.Webhook = append(n.Webhook, routes.Webhook...)
		n.PullRequest = append(n.PullRequest, routes.PullRequest...)
		r.Outcome = policyapi.Ignore
		if routeCount(routes) > 0 {
			r.Outcome = policyapi.RouteNotification
		}
	}
	if fatal {
		*out.Notification = policyapi.NotificationDecision{
			Inbox:       []policyapi.InboxMessage{},
			Slack:       []policyapi.SlackMessage{},
			Webhook:     []policyapi.WebhookMessage{},
			PullRequest: []policyapi.PullRequestMessage{},
		}
	}
	out.Outcome = policyapi.Ignore
	if routeCount(*out.Notification) > 0 {
		out.Outcome = policyapi.RouteNotification
	}
	return out
}

func routeCount(d policyapi.NotificationDecision) int {
	return len(d.Inbox) + len(d.Slack) + len(d.Webhook) + len(d.PullRequest)
}

func optional[T any](f inputs.Field[T]) *T {
	v, ok := f.Get()
	if !ok {
		return nil
	}
	return &v
}

func notificationRoutes(d inputs.NotificationDecision, out *policyapi.NotificationDecision) bool {
	for _, v := range value(d.Inbox) {
		if anyNull(v.Title, v.Body, v.Severity) {
			return false
		}
		out.Inbox = append(out.Inbox, policyapi.InboxMessage{Title: optional(v.Title), Body: optional(v.Body), Severity: optional(v.Severity)})
	}
	for _, v := range value(d.Slack) {
		_, known := v.ChannelID.Get()
		if !known || anyNull(v.ChannelID, v.Message, v.MentionUsers, v.MentionGroups, v.ThreadKey) {
			return false
		}
		out.Slack = append(
			out.Slack,
			policyapi.SlackMessage{
				ChannelID:     value(v.ChannelID),
				Message:       optional(v.Message),
				MentionUsers:  value(v.MentionUsers),
				MentionGroups: value(v.MentionGroups),
				ThreadKey:     optional(v.ThreadKey),
			},
		)
	}
	for _, v := range value(d.Webhook) {
		_, known := v.EndpointID.Get()
		if !known || anyNull(v.EndpointID, v.Headers, v.Method) {
			return false
		}
		payload := value(v.Payload)
		if v.Payload.IsNull() {
			payload = json.RawMessage("null")
		}
		out.Webhook = append(out.Webhook, policyapi.WebhookMessage{EndpointID: value(v.EndpointID), Headers: value(v.Headers), Payload: payload, Method: optional(v.Method)})
	}
	for _, v := range value(d.PullRequest) {
		if anyNull(v.ID, v.Commit, v.Branch, v.Body, v.DeduplicationKey, v.TruncateBody) {
			return false
		}
		out.PullRequest = append(
			out.PullRequest,
			policyapi.PullRequestMessage{
				ID:               optional(v.ID),
				Commit:           optional(v.Commit),
				Branch:           optional(v.Branch),
				Body:             optional(v.Body),
				DeduplicationKey: optional(v.DeduplicationKey),
				TruncateBody:     optional(v.TruncateBody),
			},
		)
	}
	return true
}
