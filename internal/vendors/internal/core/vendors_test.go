package core_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac"
	"github.com/weaveplatform/infraweave/internal/iac/version"
	"github.com/weaveplatform/infraweave/internal/kernel"
	vendorsapi "github.com/weaveplatform/infraweave/internal/vendors/api"
	"github.com/weaveplatform/infraweave/internal/vendors/internal/core"
	"github.com/weaveplatform/infraweave/internal/vendors/internal/index"
)

// store keeps releases in memory, in the order it was given them.
type store struct {
	held map[vendorsapi.Vendor][]string
	err  error
}

func (s *store) Add(_ context.Context, kind vendorsapi.Vendor, rs []index.Release) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	n := 0
	for _, r := range rs {
		if v := r.Version.String(); !slices.Contains(s.held[kind], v) {
			s.held[kind] = append(s.held[kind], v)
			n++
		}
	}
	return n, nil
}

func (s *store) Versions(_ context.Context, kind vendorsapi.Vendor) ([]string, error) {
	return slices.Clone(s.held[kind]), s.err
}

// reader lists versions, or fails with err.
type reader struct {
	versions []string
	err      error
}

func (r reader) Releases(context.Context) ([]index.Release, error) {
	out := make([]index.Release, len(r.versions))
	for i, s := range r.versions {
		out[i] = index.Release{Version: must(s)}
	}
	return out, r.err
}

func must(s string) version.Version {
	v, err := version.Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// TestUnitRefresh adds each vendor's new releases, and keeps a vendor's
// stored releases when its index cannot be read, logging why.
func TestUnitRefresh(t *testing.T) {
	ctx := t.Context()
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	st := &store{held: map[vendorsapi.Vendor][]string{vendorsapi.OpenTofu: {"1.6.0"}}}
	tofu := &reader{versions: []string{"1.7.0", "1.6.0"}}
	svc := core.New(st, map[vendorsapi.Vendor]index.Reader{
		vendorsapi.Terraform: reader{versions: []string{"1.5.7", "1.6.0", "1.5.0"}},
		vendorsapi.OpenTofu:  tofu,
	})
	if err := svc.Refresh(ctx, log); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Versions(ctx, vendorsapi.Terraform)
	if err != nil || !slices.Equal(got, []string{"1.6.0", "1.5.7", "1.5.0"}) {
		t.Errorf("terraform %v, %v", got, err)
	}
	if got, _ := svc.Versions(
		ctx,
		vendorsapi.OpenTofu,
	); !slices.Equal(
		got,
		[]string{"1.7.0", "1.6.0"},
	) {
		t.Errorf("opentofu %v", got)
	}

	tofu.err = errors.New("index down")
	if err := svc.Refresh(ctx, log); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Versions(
		ctx,
		vendorsapi.OpenTofu,
	); !slices.Equal(
		got,
		[]string{"1.7.0", "1.6.0"},
	) {
		t.Errorf("opentofu after an outage %v", got)
	}
	if !strings.Contains(logs.String(), "index down") ||
		!strings.Contains(logs.String(), "vendor=opentofu") {
		t.Errorf("the outage is not logged: %s", logs.String())
	}

	st.err = errors.New("database down")
	tofu.err = nil
	if err := svc.Refresh(ctx, log); !errors.Is(err, st.err) {
		t.Errorf("Refresh on a failing store = %v", err)
	}
	if _, err := svc.Versions(ctx, vendorsapi.Terraform); !errors.Is(err, st.err) {
		t.Errorf("Versions on a failing store = %v", err)
	}
	if _, err := svc.Effective(ctx, vendorsapi.Terraform, "1.5.7"); !errors.Is(err, st.err) {
		t.Errorf("Effective on a failing store = %v", err)
	}

	// A vendor with no index is not refreshed.
	if err := core.New(&store{held: map[vendorsapi.Vendor][]string{}}, nil).
		Refresh(ctx, log); err != nil {
		t.Errorf("Refresh with no indexes = %v", err)
	}
}

// TestUnitEffective resolves constraints to the newest stored release
// they allow, and refuses, naming the parameter, those that do not parse
// or that no stored release meets.
func TestUnitEffective(t *testing.T) {
	ctx := t.Context()
	svc := core.New(&store{held: map[vendorsapi.Vendor][]string{
		vendorsapi.Terraform: {"1.5.0", "1.6.0", "1.5.7", "1.10.0"},
	}}, nil)
	for constraints, want := range map[string]string{
		">= 1.5.0, < 1.6.0": "1.5.7",
		"~> 1.5":            "1.10.0",
		"1.6.0":             "1.6.0",
	} {
		if got, err := svc.Effective(
			ctx,
			vendorsapi.Terraform,
			constraints,
		); err != nil ||
			got != want {
			t.Errorf("%q = %q, %v; want %s", constraints, got, err, want)
		}
	}
	for constraints, reason := range map[string]string{
		"latest":   kernel.ReasonFormat,
		"> 2.0.0":  kernel.ReasonRule,
		"":         kernel.ReasonFormat,
		"~> 1.4.0": kernel.ReasonRule,
	} {
		_, err := svc.Effective(ctx, vendorsapi.Terraform, constraints)
		var inv *kernel.Invalid
		if !errors.As(err, &inv) || len(inv.Errors) != 1 ||
			inv.Errors[0].Pointer != "#/query/constraints" || inv.Errors[0].Reason != reason {
			t.Errorf("%q = %v, want %s at #/query/constraints", constraints, err, reason)
		}
	}
	if _, err := svc.Effective(ctx, vendorsapi.OpenTofu, "1.6.0"); err == nil {
		t.Error("an empty catalogue resolved a version")
	}

	bad := core.New(
		&store{held: map[vendorsapi.Vendor][]string{vendorsapi.Terraform: {"one"}}},
		nil,
	)
	if _, err := bad.Versions(ctx, vendorsapi.Terraform); !errors.Is(err, core.ErrStored) {
		t.Errorf("a stored release that does not parse = %v", err)
	}
}

// TestUnitVendorsAreKinds holds each vendor the catalogue lists to the
// stack kind of the same tool, so a stack's kind names its vendor.
func TestUnitVendorsAreKinds(t *testing.T) {
	for v, k := range map[vendorsapi.Vendor]iac.Kind{
		vendorsapi.Terraform: iac.Terraform,
		vendorsapi.OpenTofu:  iac.OpenTofu,
	} {
		if string(v) != string(k) {
			t.Errorf("vendor %q, kind %q", v, k)
		}
	}
	if len(core.Kinds) != 2 {
		t.Errorf("vendors %v", core.Kinds)
	}
}
