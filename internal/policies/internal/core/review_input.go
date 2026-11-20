package core

import (
	"encoding/json"

	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

func (b *inputBuilder) reviews(reviews policyapi.ReviewsFacts) inputs.Reviews {
	return inputs.Reviews{Current: inputs.Value(b.reviewGroup(reviews.Current)), Older: inputs.Value(b.reviewGroup(reviews.Older))}
}

func (b *inputBuilder) reviewGroup(group policyapi.ReviewGroupFacts) inputs.ReviewGroup {
	convert := func(reviews []policyapi.ReviewFacts) []inputs.Review {
		out := make([]inputs.Review, 0, len(reviews))
		for _, review := range reviews {
			out = append(out, b.review(review))
		}
		return out
	}
	return inputs.ReviewGroup{Approvals: inputs.Value(convert(group.Approvals)), Rejections: inputs.Value(convert(group.Rejections))}
}

func (b *inputBuilder) review(review policyapi.ReviewFacts) inputs.Review {
	if review.Author == "" || review.State == "" || review.StateVersion < 1 {
		b.invalid = true
	}
	return inputs.Review{
		Author: inputs.Value(review.Author), State: inputs.Value(string(review.State)), StateVersion: inputs.Value(review.StateVersion),
		AuthorRoles: convertFact(b, review.AuthorRoles, func(roles []policyapi.RoleFacts) []inputs.Role {
			out := make([]inputs.Role, 0, len(roles))
			for _, role := range roles {
				out = append(out, inputs.Role{ID: inputs.Value(role.ID), Name: inputs.Value(role.Name), Slug: inputs.Value(role.Slug), ULID: inputs.Value(role.ULID)})
			}
			return out
		}),
		Identity: convertFact(b, review.Identity, b.identity), Note: fact(b, review.Note),
		Request: convertFact(b, review.Request, b.request), Session: convertFact(b, review.Session, b.session),
	}
}

func (b *inputBuilder) identity(identity policyapi.IdentityFacts) inputs.Identity {
	return inputs.Identity{
		ULID: fact(b, identity.ULID),
		Federations: convertFact(b, identity.Federations, func(federations map[string]policyapi.FederationFacts) map[string]inputs.Federation {
			out := make(map[string]inputs.Federation, len(federations))
			for name, federation := range federations {
				out[name] = inputs.Federation{ID: fact(b, federation.ID), Username: fact(b, federation.Username)}
			}
			return out
		}),
	}
}

func (b *inputBuilder) request(request policyapi.RequestFacts) inputs.Request {
	if request.At.IsZero() {
		b.invalid = true
	}
	return inputs.Request{TimestampNS: inputs.Value(request.At.UnixNano()), RemoteIP: fact(b, request.RemoteIP)}
}

func (b *inputBuilder) session(session policyapi.SessionFacts) inputs.Session {
	if session.Admin.Known && session.Admin.Value != nil && (session.Machine != (*session.Admin.Value == policyapi.SessionMachineAdmin)) {
		b.invalid = true
	}
	return inputs.Session{
		Machine: inputs.Value(session.Machine), Teams: fact(b, session.Teams),
		Admin: convertFact(b, session.Admin, b.admin), Login: fact(b, session.Login), Name: fact(b, session.Name),
		CreatorIP: fact(b, session.CreatorIP), IDPSubject: fact(b, session.IDPSubject), StackID: fact(b, session.StackID),
		Identity: convertFact(b, session.Identity, b.identity),
	}
}

func (b *inputBuilder) admin(admin policyapi.SessionAdmin) inputs.Admin {
	var raw []byte
	switch admin {
	case policyapi.SessionNotAdmin:
		raw = []byte("false")
	case policyapi.SessionIsAdmin:
		raw = []byte("true")
	case policyapi.SessionMachineAdmin:
		raw = []byte(`""`)
	default:
		b.invalid = true
		return inputs.Admin{}
	}
	var out inputs.Admin
	// Only the three literals accepted by the contract union reach this decoder.
	_ = json.Unmarshal(raw, &out)
	return out
}
