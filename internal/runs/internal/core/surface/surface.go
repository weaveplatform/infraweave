package surface

import (
	"encoding/json"

	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
)

// Name is the variable a template reads the run by.
const Name = "run"

// schema closes run and its commit to their fields, so a misspelt one is
// an error at check time.
var schema = json.RawMessage(`{"type": "object", "properties": {
	"id": {"type": "string"}, "type": {"type": "string"},
	"commit": {"type": "object", "properties": {
		"hash": {"type": "string"}, "message": {"type": "string"},
		"authorName": {"type": "string"}
	}, "additionalProperties": false},
	"triggeredBy": {"type": "string"}, "priority": {"type": "string"}
}, "additionalProperties": false}`)

// Variable is run, as the environment surface declares it.
func Variable() expr.Variable { return expr.Variable{Name: Name, Schema: schema} }

// Value is r as a template reads it: each field schema declares, the
// priority as its preset's name.
func Value(r lifecycle.Run) map[string]any {
	return map[string]any{
		"id":   r.ID.String(),
		"type": string(r.Type),
		"commit": map[string]any{
			"hash": r.Commit.Hash, "message": r.Commit.Message, "authorName": r.Commit.AuthorName,
		},
		"triggeredBy": r.TriggeredBy,
		"priority":    string(r.Priority),
	}
}
