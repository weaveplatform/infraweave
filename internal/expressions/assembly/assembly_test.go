package assembly

import (
	"errors"
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

// TestUnitDomain checks that the domain's partial serves over the
// registry it is given, which declares no surface here, and that a nil
// domain's is the zero partial.
func TestUnitDomain(t *testing.T) {
	r, err := expr.NewRegistry(expr.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	ctx := kernel.WithPrincipal(t.Context(), kernel.Principal{})
	_, err = New(r).API().ExpressionCheck(ctx, api.ExpressionCheckRequestObject{
		Body: &api.ExpressionCheckRequest{Surface: "environment", Form: "expression", Source: "1"},
	})
	if _, ok := errors.AsType[*kernel.Invalid](err); !ok {
		t.Errorf("a surface the registry lacks = %v", err)
	}
	var none *Domain
	if !reflect.ValueOf(none.API()).IsZero() {
		t.Error("a nil domain's partial is not the zero one")
	}
}
