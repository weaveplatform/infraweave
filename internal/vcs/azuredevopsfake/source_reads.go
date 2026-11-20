package azuredevopsfake

// SourceReads counts HTTP reads of source objects and history, excluding
// repository identity metadata. It retains no request paths or credentials.
func (f *Fake) SourceReads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sourceReads
}

func (f *Fake) recordSourceRead() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sourceReads++
}
