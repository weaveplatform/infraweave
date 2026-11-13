package plan

import "strings"

// Entity is a resource address's parts: Module, the path of the module
// the resource is in, empty for the root module; Type, its resource type,
// prefixed data. for a data source; and Name, its name with any instance
// key. Joined by dots, Module left out when empty, they are the address.
type Entity struct {
	Module string
	Type   string
	Name   string
}

// Local is e's address within its module.
func (e Entity) Local() string { return e.Type + "." + e.Name }

// EntityOf splits a resource address, such as
// module.net["eu"].aws_subnet.private[0], into its parts; false when it
// is not one. A dot inside an instance key does not split it.
func EntityOf(address string) (Entity, bool) {
	parts, ok := split(address)
	if !ok {
		return Entity{}, false
	}
	i := 0
	for len(parts)-i > 2 && parts[i] == "module" {
		i += 2
	}
	e := Entity{Module: strings.Join(parts[:i], ".")}
	switch rest := parts[i:]; {
	case len(rest) == 2 && rest[0] != "module" && rest[0] != "data":
		e.Type, e.Name = rest[0], rest[1]
	case len(rest) == 3 && rest[0] == "data":
		e.Type, e.Name = "data."+rest[1], rest[2]
	default:
		return Entity{}, false
	}
	if strings.ContainsRune(e.Type, '[') || strings.HasPrefix(e.Name, "[") {
		return Entity{}, false
	}
	return e, true
}

// split splits address at each dot outside an instance key, a bracketed
// index or quoted string; false when a part is empty or a bracket or
// quote is left open.
func split(address string) ([]string, bool) {
	var (
		parts         []string
		start         int
		depth         int
		quoted, escpd bool
	)
	for i, r := range address {
		switch {
		case escpd:
			escpd = false
		case quoted && r == '\\':
			escpd = true
		case r == '"' && depth > 0:
			quoted = !quoted
		case quoted:
		case r == '[':
			depth++
		case r == ']':
			if depth == 0 {
				return nil, false
			}
			depth--
		case r == '.' && depth == 0:
			if i == start {
				return nil, false
			}
			parts = append(parts, address[start:i])
			start = i + 1
		}
	}
	if depth != 0 || quoted || start == len(address) {
		return nil, false
	}
	return append(parts, address[start:]), true
}
