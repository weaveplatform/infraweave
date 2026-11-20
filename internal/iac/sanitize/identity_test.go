package sanitize_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/sanitize"
)

// Modern tool plans copy provider values into import/generated/identity fields.
// These are values, rather than structural addresses or replacement path names.
func TestUnitPlanIdentityCopies(t *testing.T) {
	const raw = `{"format_version":"1.2","variables":{"secret":{"value":"private-identity-642"}},"configuration":{"root_module":{"variables":{"secret":{"sensitive":true}}}},"resource_changes":[{"address":"terraform_data.x","change":{"actions":["create"],"before_identity":{"token":"private-identity-642"},"after_identity":{"token":"private-identity-642"},"generated_config":"resource x { token = private-identity-642 }","importing":{"id":"private-identity-642","identity":{"token":"private-identity-642"}}}}]}`
	for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
		var out bytes.Buffer
		if err := sanitize.Plan(&out, strings.NewReader(raw), mode); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(out.Bytes(), []byte("private-identity-642")) != (mode == sanitize.None) {
			t.Fatalf("%s left raw provider value copies", mode)
		}
	}
}
