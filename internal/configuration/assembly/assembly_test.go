package assembly

import (
	"testing"

	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
)

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
	d = New(Deps{})
	surfaces := d.Surfaces()
	if len(surfaces) != 1 || surfaces[0].Name != configapi.Surface || d.Environment() == nil {
		t.Errorf("surfaces %v", surfaces)
	}
	r, err := expr.NewRegistry(expr.Defaults(), surfaces...)
	if err != nil {
		t.Fatal(err)
	}
	d.BindExpressions(r)
}
