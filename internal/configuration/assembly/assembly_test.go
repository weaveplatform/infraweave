package assembly

import "testing"

// TestUnitDomain checks that the domain's partial serves no operation yet
// and that its stream is named for the domain and loads.
func TestUnitDomain(t *testing.T) {
	if New().API() != (ConfigurationAPI{}) {
		t.Error("API serves an operation")
	}
	s := Stream()
	files, err := s.Load()
	if err != nil || s.Name != "configuration" || len(files) != 1 {
		t.Errorf("stream %s: %d files, %v", s.Name, len(files), err)
	}
}
