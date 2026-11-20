package rego

import (
	"fmt"
	"sort"
	"strings"
)

// Type selects the preamble and per-evaluation deadline.
type Type string

const (
	Access         Type = "ACCESS"
	Approval       Type = "APPROVAL"
	Push           Type = "GIT_PUSH"
	Initialization Type = "INITIALIZATION"
	Intent         Type = "INTENT"
	Login          Type = "LOGIN"
	Notification   Type = "NOTIFICATION"
	Plan           Type = "PLAN"
	Task           Type = "TASK"
	Trigger        Type = "TRIGGER"
)

// EngineType selects only the user's module dialect. Preambles always use v1.
type EngineType string

const (
	V0 EngineType = "REGO_V0"
	V1 EngineType = "REGO_V1"
)

// preambleVersion participates in every prepared-query identity.
const preambleVersion = "3"

type ruleKind uint8

const (
	booleanRule ruleKind = iota
	stringRule
	stringSet
	messageSet
	objectSet
	rolesRule
)

func rules(kind Type) map[string]ruleKind {
	out := map[string]ruleKind{"flag": stringSet, "reset_flag": stringSet, "sample": booleanRule}
	add := func(k ruleKind, names ...string) {
		for _, name := range names {
			out[name] = k
		}
	}
	switch kind {
	case Access:
		add(booleanRule, "read", "write", "deny", "deny_write")
	case Approval:
		add(booleanRule, "approve", "reject")
		add(messageSet, "approve_with_note", "reject_with_note")
	case Push:
		add(booleanRule, "allow_fork", "fail", "ignore", "ignore_track", "notify", "notrigger", "prioritize", "propose", "track")
		add(stringRule, "lock", "unlock", "module_version", "priority")
		add(stringSet, "cancel", "external_dependency")
		add(messageSet, "message")
	case Initialization, Task:
		add(messageSet, "deny")
	case Intent:
		add(messageSet, "allow", "deny")
	case Login:
		add(booleanRule, "admin", "allow", "deny", "deny_admin")
		add(messageSet, "deny_message")
		add(stringSet, "team", "space_admin", "space_write", "space_read")
		add(rolesRule, "roles")
	case Notification:
		add(objectSet, "inbox", "pull_request", "slack", "webhook")
	case Plan:
		add(messageSet, "deny", "warn", "info")
	case Trigger:
		add(stringSet, "trigger")
		add(stringRule, "priority")
	default:
		return nil
	}
	return out
}

func preamble(kind Type) string {
	known := rules(kind)
	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("package infraweave\n")
	for _, name := range names {
		switch known[name] {
		case booleanRule:
			value := "false"
			if kind == Login && name == "sample" {
				value = "true"
			}
			fmt.Fprintf(&b, "default %s := %s\n", name, value)
		case stringRule:
			fmt.Fprintf(&b, "default %s := \"\"\n", name)
		case stringSet, messageSet:
			fmt.Fprintf(&b, "%s contains \"\" if false\n", name)
		case objectSet:
			fmt.Fprintf(&b, "%s contains {} if false\n", name)
		case rolesRule:
			// The nested false rule keeps the observed empty sentinel while
			// allowing partial roles and rejecting a complete roles reassignment.
			b.WriteString("roles.nothing.nothing := true if false\n")
		}
	}
	b.WriteString("result := {")
	for i, name := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%q:%s", name, name)
	}
	b.WriteString("}\n")
	return b.String()
}
