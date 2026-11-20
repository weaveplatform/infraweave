package combine

import (
	"encoding/json"
	"strings"

	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

// Login resolves raw grants before the access caller applies owner safeguards.
func Login(receipts []policyapi.Receipt) policyapi.Evaluation {
	out := evaluation(policyapi.Login, receipts)
	if !out.HasPolicies {
		return out
	}
	out.Login = &policyapi.LoginDecision{
		Messages:   []string{},
		Teams:      []string{},
		Roles:      map[string][]string{},
		SpaceAdmin: []string{},
		SpaceWrite: []string{},
		SpaceRead:  []string{},
	}
	l := out.Login
	for i := range out.Receipts {
		r := &out.Receipts[i]
		var d inputs.LoginDecision
		if r.Failure == nil &&
			(r.Type != policyapi.Login || decode(r.Decision, &d) != nil || anyNull(d.Admin, d.Allow, d.Deny, d.DenyAdmin, d.DenyMessage, d.Roles, d.Team, d.SpaceAdmin, d.SpaceRead, d.SpaceWrite) || !common(r, d.Flag, d.ResetFlag, d.Sample, true)) {
			invalid(r, policyapi.Login)
		}
		if r.Failure != nil {
			failed(&out, r, policyapi.Deny)
			l.Deny = true
			l.Messages = append(l.Messages, r.Failure.Message)
			continue
		}
		l.Allow = l.Allow || value(d.Allow) || value(d.Admin)
		l.Admin = l.Admin || value(d.Admin)
		l.Deny = l.Deny || value(d.Deny)
		l.DenyAdmin = l.DenyAdmin || value(d.DenyAdmin)
		l.Messages = append(l.Messages, value(d.DenyMessage)...)
		if teams := value(d.Team); len(teams) > 0 {
			l.Teams = unique(teams)
		}
		for space, roles := range value(d.Roles) {
			if slugs := roleSlugs(roles); len(slugs) > 0 {
				l.Roles[space] = append(l.Roles[space], slugs...)
			}
		}
		l.SpaceAdmin = append(l.SpaceAdmin, value(d.SpaceAdmin)...)
		l.SpaceWrite = append(l.SpaceWrite, value(d.SpaceWrite)...)
		l.SpaceRead = append(l.SpaceRead, value(d.SpaceRead)...)
		r.Outcome = policyapi.Deny
		if !value(d.Deny) && (value(d.Allow) || value(d.Admin)) {
			r.Outcome = policyapi.Allow
		}
	}
	l.Messages = unique(l.Messages)
	l.Deny = l.Deny || !l.Allow
	l.Allow = l.Allow && !l.Deny
	l.Admin = l.Admin && !l.Deny && !l.DenyAdmin
	for space, roles := range l.Roles {
		l.Roles[space] = unique(roles)
	}
	l.SpaceAdmin, l.SpaceWrite, l.SpaceRead = unique(l.SpaceAdmin), unique(l.SpaceWrite), unique(l.SpaceRead)
	out.Outcome = policyapi.Allow
	if l.Deny {
		out.Outcome = policyapi.Deny
		message := []rune(strings.Join(l.Messages, ";"))
		if len(message) > 1024 {
			message = append(message[:1021], '.', '.', '.')
		}
		l.Message = string(message)
	}
	return out
}

func roleSlugs(roles inputs.RoleSet) []string {
	raw, _ := roles.MarshalJSON()
	var list []string
	if len(raw) > 0 && raw[0] == '[' {
		_ = json.Unmarshal(raw, &list)
		return list
	}
	var grants map[string]bool
	_ = json.Unmarshal(raw, &grants)
	for role, granted := range grants {
		if granted {
			list = append(list, role)
		}
	}
	return list
}
