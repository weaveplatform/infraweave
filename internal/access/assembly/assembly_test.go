package assembly

import "testing"

func TestUnitAssembly(t *testing.T) {
	if s := Stream(); s.Name != "access" || s.Files == nil {
		t.Errorf("Stream = %+v", s)
	}
	if len(SealedColumns()) == 0 {
		t.Error("the access stream seals signing keys, but lists no sealed column")
	}
	d := New(Deps{})
	if d == nil {
		t.Fatal("New returned nil")
	}
	_ = d.API()
}
