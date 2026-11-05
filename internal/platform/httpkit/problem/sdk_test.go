package problem

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/weaveplatform/infraweave/pkg/sdk"
)

// TestUnitSDKReadsEveryType holds pkg/sdk, which may not import this
// package, to what the server writes: every Type it answers with reads back
// in the SDK as that slug's typed error, with its detail.
func TestUnitSDKReadsEveryType(t *testing.T) {
	if sdk.TypePrefix != TypePrefix {
		t.Fatalf("sdk.TypePrefix %q, server %q", sdk.TypePrefix, TypePrefix)
	}
	for _, typ := range Types() {
		rec := httptest.NewRecorder()
		Write(rec, typ.New("why"))
		res := rec.Result()
		err := sdk.Check(res, rec.Body.Bytes())
		res.Body.Close()
		p, ok := sdk.AsProblem(err)
		if !ok || p.Slug() != typ.Slug || p.Detail != "why" {
			t.Errorf("%s reads back as %#v", typ.Slug, err)
			continue
		}
		if generic := (sdk.Problem{}); errors.As(err, &generic) {
			t.Errorf("%s has no typed error in pkg/sdk", typ.Slug)
		}
	}
}
