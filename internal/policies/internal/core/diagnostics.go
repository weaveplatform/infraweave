package core

import (
	"strings"
	"unicode/utf8"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
)

// OPA columns count code points, while its source span counts bytes and may
// cross lines. Only the user module can locate a field in the submitted body.
func sourceRange(body string, d rego.Diagnostic) *kernel.Range {
	if d.File != "policy.rego" || d.Row < 1 || d.Column < 1 {
		return nil
	}
	start := 0
	for line := 1; line < d.Row; line++ {
		i := strings.IndexByte(body[start:], '\n')
		if i < 0 {
			return nil
		}
		start += i + 1
	}
	for col := 1; col < d.Column; col++ {
		if start == len(body) || body[start] == '\n' {
			return nil
		}
		_, n := utf8.DecodeRuneInString(body[start:])
		start += n
	}
	end := min(len(body), start+max(d.Length, 1))
	for end < len(body) && !utf8.RuneStart(body[end]) {
		end++
	}
	return &kernel.Range{Start: sourcePosition(body, start), End: sourcePosition(body, end)}
}

func sourcePosition(body string, offset int) kernel.Position {
	prefix := body[:offset]
	last := strings.LastIndexByte(prefix, '\n')
	return kernel.Position{Line: strings.Count(prefix, "\n") + 1, Column: utf8.RuneCountInString(prefix[last+1:]) + 1}
}
