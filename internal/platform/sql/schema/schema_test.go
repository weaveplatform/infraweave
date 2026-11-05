package schema_test

import (
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
)

func TestUnitPlatformStream(t *testing.T) {
	s := schema.Stream()
	if s.Name != schema.Name {
		t.Errorf("name = %q", s.Name)
	}
	if _, err := s.Load(); err != nil {
		t.Errorf("Load: %v", err)
	}
}
