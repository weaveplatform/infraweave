package assembly

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/cloud/internal/aws"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitValidate(t *testing.T) {
	for endpoint, ok := range map[string]bool{
		"": true, "http://127.0.0.1:4566": true, "https://sts.example.com": true,
		"ftp://sts": false, "sts.example.com": false, "http://": false, "http://[::1": false,
	} {
		err := Settings{AWS: aws.Settings{STSEndpoint: endpoint}}.Validate()
		if (err == nil) != ok || (err != nil && !errors.Is(err, ErrConfig)) {
			t.Errorf("%q: err = %v", endpoint, err)
		}
	}
}

func TestUnitMapped(t *testing.T) {
	broken := errors.New("broken")
	if err := mapped(nil); err != nil {
		t.Fatalf("mapped(nil) = %v", err)
	}
	if err := mapped(aws.ErrNoCredentials); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("no credentials = %v", err)
	}
	if r, ok := errors.AsType[*core.Refused](mapped(&aws.Refused{Code: "AccessDenied", Message: "no"})); !ok ||
		r.Message != "no" {
		t.Fatalf("refused = %v", r)
	}
	if u, ok := errors.AsType[*core.Upstream](mapped(broken)); !ok || !errors.Is(u, broken) {
		t.Fatalf("upstream = %v", u)
	}
}

// options answers err for every feature.
type options struct{ err error }

func (o options) AllowAccount(context.Context, kernel.OrganizationID, kernel.AccountID, string) error {
	return o.err
}

func TestUnitEnabled(t *testing.T) {
	broken := errors.New("broken")
	org := func(context.Context, kernel.AccountID) (kernel.OrganizationID, error) {
		return kernel.OrganizationID{}, nil
	}
	for _, tc := range []struct {
		err  error
		want bool
		fail bool
	}{
		{nil, true, false},
		{&featuresapi.Refusal{}, false, false},
		{broken, false, true},
	} {
		ok, err := enabled(options{tc.err}, org)(t.Context(), kernel.NewAccountID())
		if ok != tc.want || (err != nil) != tc.fail {
			t.Errorf("%v: enabled = %v, %v", tc.err, ok, err)
		}
	}
	lost := func(context.Context, kernel.AccountID) (kernel.OrganizationID, error) {
		return kernel.OrganizationID{}, broken
	}
	if _, err := enabled(options{}, lost)(t.Context(), kernel.NewAccountID()); !errors.Is(err, broken) {
		t.Fatalf("err = %v", err)
	}
}

func TestUnitNilDomain(t *testing.T) {
	var d *Domain
	if d.API() != (CloudAPI{}) {
		t.Fatal("a nil domain's partial is not the zero partial")
	}
}
