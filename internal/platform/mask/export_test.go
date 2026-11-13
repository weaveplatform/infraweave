package mask

// PatternNames are the well-known patterns' names, so a test can check
// that it samples every one.
func PatternNames() []string {
	out := make([]string, len(wellKnown))
	for i, p := range wellKnown {
		out[i] = p.name
	}
	return out
}
