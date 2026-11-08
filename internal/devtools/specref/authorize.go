package specref

import (
	"fmt"
	"strings"
)

// authorize writes the authorization extensions x-infraweave-action and
// x-infraweave-subject on a scaffolded operation where the catalogue rows
// decide them, and leaves out what they cannot, so the served API's
// authorization test names the operation for its author:
//   - the action is the catalogue's value spelled as the operationId, such
//     as STACK_CREATE for stackCreate, or for a GET the read of the
//     entity it names, API_KEY_READ for apiKey or apiKeys, or SPACE_READ;
//   - an infraweave account action's subject is any where it is decided
//     anywhere, else account;
//   - another action's subject is the path parameter naming an entity of
//     the action's kind, {stackId} for a stack, or, for a GET of no
//     entity, a query parameter named space, or else list.
//
// ref: decision 0007
func authorize(method, path string, op ymap, rows map[string]ActionRow) {
	id, _ := op["operationId"].(string)
	name := strings.ToUpper(strings.Join(words(id), "_"))
	candidates := []string{name}
	if method == "get" {
		candidates = append(candidates, name+"_READ", strings.TrimSuffix(name, "S")+"_READ",
			"SPACE_READ")
	}
	var row ActionRow
	ok := false
	for _, c := range candidates {
		if row, ok = rows[c]; ok {
			name = c
			break
		}
	}
	if !ok {
		return
	}
	op["x-infraweave-action"] = name
	params := pathParam.FindAllStringSubmatch(path, -1)
	switch {
	case row.Anywhere:
		op["x-infraweave-subject"] = "any"
	case row.Infraweave:
		op["x-infraweave-subject"] = "account"
	case name == "SPACE_READ" && len(params) == 0 && hasQuery(op, "space"):
		op["x-infraweave-subject"] = "query:space"
	case name == "SPACE_READ" && len(params) == 0:
		op["x-infraweave-subject"] = "list"
	default:
		for _, p := range params {
			entity := strings.ToLower(strings.Join(words(strings.TrimSuffix(p[1], "Id")), "_"))
			if entity == row.Subject || name == "SPACE_READ" && kinds(rows)[entity] {
				op["x-infraweave-subject"] = fmt.Sprintf("path:%s:%s", p[1], entity)
				return
			}
		}
	}
}

// hasQuery reports whether op takes the query parameter name.
func hasQuery(op ymap, name string) bool {
	params, _ := op["parameters"].([]any)
	for _, p := range params {
		if m, ok := p.(ymap); ok && m["in"] == "query" && m["name"] == name {
			return true
		}
	}
	return false
}

// kinds are the entity kinds the catalogue's rows name.
func kinds(rows map[string]ActionRow) map[string]bool {
	out := map[string]bool{}
	for _, r := range rows {
		out[r.Subject] = true
	}
	return out
}
