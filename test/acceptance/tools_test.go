package acceptance

import (
	"testing"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/steps/vendors"
	"github.com/weaveplatform/infraweave/test/deps"
)

// TestUnitToolPins checks that the Terraform and OpenTofu releases the
// suite pins are the newest each recorded index lists. A stack that sets
// no version runs the catalogue's newest, which the launcher downloads, so
// a pin behind the index fails every real run's download from the
// harness's mirror; deps-refresh.yml moves both together.
func TestUnitToolPins(t *testing.T) {
	for name, pin := range map[string]deps.Tool{
		"terraform": deps.Terraform, "opentofu": deps.OpenTofu,
	} {
		newest, err := vendors.Newest(name)
		if err != nil {
			t.Fatal(err)
		}
		if pin.Version != newest {
			t.Errorf("test/deps pins %s %s, and its recorded index's newest is %s: move both "+
				"with make vendors-fixtures and the pin in test/deps/tools.go", name, pin.Version,
				newest)
		}
	}
}
