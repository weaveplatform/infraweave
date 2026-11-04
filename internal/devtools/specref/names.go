package specref

import (
	"regexp"
	"strings"
	"unicode"
)

// words splits a camelCase or PascalCase identifier into words, keeping
// acronyms together: "githubEnterpriseIntegration" → github Enterprise
// Integration, "VCSIntegration" → VCS Integration, "repoID" → repo ID.
func words(s string) []string {
	r := []rune(s)
	var out []string
	start := 0
	for i := 1; i < len(r); i++ {
		prev, cur := r[i-1], r[i]
		boundary := unicode.IsUpper(cur) && (unicode.IsLower(prev) || unicode.IsDigit(prev)) ||
			unicode.IsUpper(prev) && unicode.IsUpper(cur) && i+1 < len(r) && unicode.IsLower(r[i+1])
		if boundary {
			out = append(out, string(r[start:i]))
			start = i
		}
	}
	return append(out, string(r[start:]))
}

// camel joins words into lowerCamelCase.
func camel(ws []string) string {
	if len(ws) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(lowerFirst(ws[0]))
	for _, w := range ws[1:] {
		b.WriteString(upperFirst(w))
	}
	return b.String()
}

// kebab renders an identifier as a lower-case, hyphenated path segment.
func kebab(s string) string {
	ws := words(s)
	for i, w := range ws {
		ws[i] = strings.ToLower(w)
	}
	return strings.Join(ws, "-")
}

// snakeToCamel converts a Terraform attribute name to the GraphQL spelling
// the provider uses for most fields: space_id → spaceId.
func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		parts[i] = upperFirst(parts[i])
	}
	return strings.Join(parts, "")
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	// An all-caps leading acronym lowers as a whole: "VCSAgentPool" is
	// already split, so here "VCS" → "vcs".
	if strings.ToUpper(s) == s {
		return strings.ToLower(s)
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// plural pluralises the last word of a camelCase name. Irregular names come
// from the mapping file and win.
func plural(name string, irregular map[string]string) string {
	if p, ok := irregular[name]; ok {
		return p
	}
	ws := words(name)
	last := ws[len(ws)-1]
	lower := strings.ToLower(last)
	switch {
	case strings.HasSuffix(lower, "s") || strings.HasSuffix(lower, "x") || strings.HasSuffix(lower, "ch") || strings.HasSuffix(lower, "sh"):
		last += "es"
	case strings.HasSuffix(lower, "y") && len(lower) > 1 && !strings.ContainsRune("aeiou", rune(lower[len(lower)-2])):
		last = last[:len(last)-1] + "ies"
	default:
		last += "s"
	}
	ws[len(ws)-1] = last
	return camel(ws)
}

// hasWordPrefix reports whether name starts with prefix on a word boundary,
// so "stackScheduledRun" has prefix "stack" but "stacks" does not.
func hasWordPrefix(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) || len(name) == len(prefix) {
		return false
	}
	return unicode.IsUpper(rune(name[len(prefix)]))
}

var versionSuffix = regexp.MustCompile(`^(.*[a-z])([Vv]\d+)$`)

// stripVersion splits a versioned operation name: contextCreateV2 →
// (contextCreate, V2); policyCreatev2 → (policyCreate, v2).
func stripVersion(name string) (string, string) {
	if m := versionSuffix.FindStringSubmatch(name); m != nil {
		return m[1], m[2]
	}
	return name, ""
}
