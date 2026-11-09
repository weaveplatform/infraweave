package assembly

import "testing"

// TestUnitDomain checks that a nil domain's partial is the zero one, and
// that the domain's stream is named for it and loads.
func TestUnitDomain(t *testing.T) {
	var d *Domain
	if d.API() != (ConfigurationAPI{}) {
		t.Error("a nil domain's partial is not the zero one")
	}
	s := Stream()
	files, err := s.Load()
	if err != nil || s.Name != "configuration" || len(files) != 1 {
		t.Errorf("stream %s: %d files, %v", s.Name, len(files), err)
	}
	if got := SealedColumns(); len(got) != 1 || got[0].Name != "value_sealed" {
		t.Errorf("sealed columns %v", got)
	}
}
