//go:build !unix

package exec

// HandBack does nothing: a runner runs in a Linux container, and no other
// system has the owners that keep its launcher from removing what it
// wrote.
func HandBack(string) error { return nil }
