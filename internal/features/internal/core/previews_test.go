package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	root "github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/features/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func (s *store) FeaturePreviews(_ context.Context, acct kernel.AccountID) (bool, error) {
	s.read("previews")
	return s.optedIn[acct], s.previewErr
}

func (s *store) SetFeaturePreviews(_ context.Context, acct kernel.AccountID, enabled bool) error {
	if s.previewErr != nil {
		return s.previewErr
	}
	if s.optedIn == nil {
		s.optedIn = map[kernel.AccountID]bool{}
	}
	s.optedIn[acct] = enabled
	return nil
}

func (s *store) PersonalFeaturePreviews(_ context.Context, acct kernel.AccountID, member kernel.MemberID) (bool, error) {
	return !s.optedOut[[2]kernel.ULID{acct.ULID, member.ULID}], s.personalErr
}

func (s *store) SetPersonalFeaturePreviews(_ context.Context, acct kernel.AccountID, member kernel.MemberID, enabled bool) error {
	if s.optedOut == nil {
		s.optedOut = map[[2]kernel.ULID]bool{}
	}
	s.optedOut[[2]kernel.ULID{acct.ULID, member.ULID}] = !enabled
	return s.personalErr
}

func (s *store) RemoveMember(_ context.Context, acct kernel.AccountID, member kernel.MemberID) error {
	delete(s.optedOut, [2]kernel.ULID{acct.ULID, member.ULID})
	return s.personalErr
}

// testPreviews is a test catalogue's features in preview, a capability and
// an option, in catalogue order; the compiled catalogue marks none.
func testPreviews() []root.Feature {
	return []root.Feature{
		{Name: "insights", Kind: root.Capability, LowestTier: root.Business, Preview: &root.Preview{
			Name: "Insights", Description: "Stack insights.", DocsURL: "https://docs.example/insights",
		}},
		{Name: "vendor-preview", Kind: root.Option, LowestTier: root.Free, Preview: &root.Preview{
			Name: "Preview vendor", Description: "A vendor.", DocsURL: "https://docs.example/vendor",
			ImageURL: "https://docs.example/vendor.png",
		}},
	}
}

// TestUnitPreviewGate checks the feature lookup over a test catalogue that
// marks a capability and an option as previews: an account that has not
// opted in is refused each with preview-not-enabled, billing off or on;
// the switch-off and, with billing on, the tier refusal come first; a
// generally available feature and a member's personal opt-out change
// nothing; and active holds only when the entitlement holds the preview,
// the account opted in and the member did not opt out.
//
// ref: decision 0021
func TestUnitPreviewGate(t *testing.T) {
	org, acct, member := kernel.NewOrganizationID(), kernel.NewAccountID(), kernel.NewMemberID()
	tiers := []root.Tier{
		{Name: "Business", Features: []string{"insights", "stacks", "vendor-preview"}},
		{Name: "Free", Features: []string{"stacks", "vendor-preview"}},
	}
	free := featuresapi.Entitlement{Tier: "Free", Features: []string{"stacks", "vendor-preview"}}
	business := featuresapi.Entitlement{
		Tier: "Business", Features: []string{"insights", "stacks", "vendor-preview"},
	}
	type setting struct{ billing, optedIn, optedOut bool }
	preview := func(f string) *core.Refusal { return &core.Refusal{Preview: true, Feature: f} }
	cases := []struct {
		name     string
		set      setting
		ent      featuresapi.Entitlement
		disabled []string
		feature  string
		want     *core.Refusal
	}{
		{"billing off, capability, not opted in", setting{}, free, nil, "insights", preview("insights")},
		{"billing off, option, not opted in", setting{}, free, nil, "vendor-preview", preview("vendor-preview")},
		{"billing off, capability, opted in", setting{optedIn: true}, free, nil, "insights", nil},
		{"billing off, option, opted in", setting{optedIn: true}, free, nil, "vendor-preview", nil},
		{
			"billing on, capability held, not opted in",
			setting{billing: true},
			business, nil,
			"insights", preview("insights"),
		},
		{
			"billing on, option held, not opted in",
			setting{billing: true},
			free, nil,
			"vendor-preview", preview("vendor-preview"),
		},
		{"billing on, capability held, opted in", setting{true, true, false}, business, nil, "insights", nil},
		{"billing on, option held, opted in", setting{true, true, false}, free, nil, "vendor-preview", nil},
		{
			"billing on, outside the tier, not opted in: the tier wins",
			setting{billing: true},
			free, nil,
			"insights", &core.Refusal{Feature: "insights", Tiers: []string{"Business"}},
		},
		{
			"billing on, outside the tier, opted in",
			setting{true, true, false},
			free, nil,
			"insights", &core.Refusal{Feature: "insights", Tiers: []string{"Business"}},
		},
		{
			"billing off, switched off, not opted in: the switch-off wins",
			setting{},
			free,
			[]string{"insights"},
			"insights", &core.Refusal{Disabled: true, Feature: "insights"},
		},
		{
			"billing on, switched off, opted in",
			setting{true, true, false},
			business,
			[]string{"vendor-preview"},
			"vendor-preview",
			&core.Refusal{Disabled: true, Feature: "vendor-preview"},
		},
		{"a member's opt-out refuses nothing", setting{true, true, true}, business, nil, "insights", nil},
		{"billing off, a member's opt-out", setting{optedIn: true, optedOut: true}, free, nil, "vendor-preview", nil},
		{"generally available, not opted in", setting{}, free, nil, "stacks", nil},
		{"generally available, billing on, not opted in", setting{billing: true}, free, nil, "stacks", nil},
		{"generally available, opted in and out", setting{true, true, true}, free, nil, "stacks", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := c.ent
			s := &store{billingOff: !c.set.billing, entitlement: &e, tiers: tiers}
			var n int
			svc := core.New(s, inTx(&n), c.disabled).WithPreviews(testPreviews)
			ctx := t.Context()
			if err := svc.SetFeaturePreviewsEnabled(ctx, acct, c.set.optedIn); err != nil {
				t.Fatal(err)
			}
			if err := svc.SetPersonalFeaturePreviewsEnabled(ctx, acct, member, !c.set.optedOut); err != nil {
				t.Fatal(err)
			}
			p := kernel.Principal{OrganizationID: org, AccountID: acct, MemberID: member}
			for name, err := range map[string]error{
				"Allow":        svc.Allow(kernel.WithPrincipal(ctx, p), c.feature),
				"AllowAccount": svc.AllowAccount(ctx, org, acct, c.feature),
			} {
				got, _ := errors.AsType[*core.Refusal](err)
				if (err != nil) != (c.want != nil) || !reflect.DeepEqual(got, c.want) {
					t.Errorf("%s = %v; want %+v", name, err, c.want)
				}
			}
		})
	}

	t.Run("a principal of no organization meets only the switch-off", func(t *testing.T) {
		var n int
		svc := core.New(&store{}, inTx(&n), []string{"vendor-preview"}).WithPreviews(testPreviews)
		if err := svc.Allow(kernel.WithPrincipal(t.Context(), kernel.Principal{}), "insights"); err != nil {
			t.Errorf("an operator: %v", err)
		}
		err := svc.AllowAccount(t.Context(), kernel.OrganizationID{}, acct, "vendor-preview")
		if r, ok := errors.AsType[*core.Refusal](err); !ok || !r.Disabled {
			t.Errorf("a switched-off preview of no organization: %v", err)
		}
	})
	t.Run("a principal of no account has not opted in", func(t *testing.T) {
		s := &store{billingOff: true}
		var n int
		svc := core.New(s, inTx(&n), nil).WithPreviews(testPreviews)
		p := kernel.Principal{Kind: kernel.PrincipalOrganization, OrganizationID: org}
		err := svc.Allow(kernel.WithPrincipal(t.Context(), p), "insights")
		if r, ok := errors.AsType[*core.Refusal](err); !ok || !r.Preview || s.reads["previews"] != 0 {
			t.Errorf("Allow = %v after %d reads of an opt-in", err, s.reads["previews"])
		}
	})
	t.Run("an unreadable opt-in fails the lookup", func(t *testing.T) {
		var n int
		svc := core.New(&store{billingOff: true, previewErr: errStore}, inTx(&n), nil).
			WithPreviews(testPreviews)
		if err := svc.AllowAccount(t.Context(), org, acct, "insights"); !errors.Is(err, errStore) {
			t.Errorf("AllowAccount = %v", err)
		}
		if err := svc.AllowAccount(t.Context(), org, acct, "stacks"); err != nil {
			t.Errorf("a generally available feature reads no opt-in: %v", err)
		}
	})

	t.Run("active", func(t *testing.T) {
		type want struct{ insights, vendor bool }
		for _, c := range []struct {
			name     string
			set      setting
			ent      featuresapi.Entitlement
			disabled []string
			p        kernel.Principal
			want     want
			listed   []string
		}{
			{
				"not opted in",
				setting{billing: true},
				business, nil,
				kernel.Principal{OrganizationID: org, AccountID: acct, MemberID: member},
				want{},
				[]string{"insights", "vendor-preview"},
			},
			{
				"opted in, held",
				setting{true, true, false},
				business, nil,
				kernel.Principal{OrganizationID: org, AccountID: acct, MemberID: member},
				want{true, true},
				[]string{"insights", "vendor-preview"},
			},
			{
				"opted in, outside the tier",
				setting{true, true, false},
				free, nil,
				kernel.Principal{OrganizationID: org, AccountID: acct, MemberID: member},
				want{false, true},
				[]string{"insights", "vendor-preview"},
			},
			{
				"opted in, billing off holds every preview",
				setting{optedIn: true},
				free, nil,
				kernel.Principal{OrganizationID: org, AccountID: acct, MemberID: member},
				want{true, true},
				[]string{"insights", "vendor-preview"},
			},
			{
				"opted in, the member opted out",
				setting{true, true, true},
				business, nil,
				kernel.Principal{OrganizationID: org, AccountID: acct, MemberID: member},
				want{},
				[]string{"insights", "vendor-preview"},
			},
			{
				"a principal that names no member",
				setting{true, true, true},
				business, nil,
				kernel.Principal{Kind: kernel.PrincipalAPIKey, OrganizationID: org, AccountID: acct},
				want{true, true},
				[]string{"insights", "vendor-preview"},
			},
			{
				"a switched-off preview is not listed",
				setting{true, true, false},
				business,
				[]string{"insights"},
				kernel.Principal{OrganizationID: org, AccountID: acct, MemberID: member},
				want{false, true},
				[]string{"vendor-preview"},
			},
		} {
			t.Run(c.name, func(t *testing.T) {
				e := c.ent
				s := &store{billingOff: !c.set.billing, entitlement: &e, tiers: tiers}
				var n int
				svc := core.New(s, inTx(&n), c.disabled).WithPreviews(testPreviews)
				ctx := t.Context()
				if err := svc.SetFeaturePreviewsEnabled(ctx, acct, c.set.optedIn); err != nil {
					t.Fatal(err)
				}
				if err := svc.SetPersonalFeaturePreviewsEnabled(ctx, acct, member, !c.set.optedOut); err != nil {
					t.Fatal(err)
				}
				got, err := svc.FeaturePreviews(ctx, c.p)
				if err != nil {
					t.Fatal(err)
				}
				var listed []string
				active := map[string]bool{}
				for _, f := range got.Features {
					listed = append(listed, f.Name)
					active[f.Name] = f.Active
					if f.Preview == nil {
						t.Errorf("%s carries no preview", f.Name)
					}
				}
				if !slices.Equal(listed, c.listed) || active["insights"] != c.want.insights ||
					active["vendor-preview"] != c.want.vendor || got.Account != c.set.optedIn {
					t.Errorf("FeaturePreviews = %+v; want %v listed, active %+v", got, c.listed, c.want)
				}
				switch {
				case c.p.MemberID.IsZero() && got.Personal != nil:
					t.Errorf("a principal of no member has a personal setting: %t", *got.Personal)
				case !c.p.MemberID.IsZero() && (got.Personal == nil || *got.Personal == c.set.optedOut):
					t.Errorf("personal = %v; want %t", got.Personal, !c.set.optedOut)
				}
			})
		}
		yes, no := true, false
		for _, c := range []struct {
			held, optedIn bool
			personal      *bool
			want          bool
		}{
			{true, true, nil, true},
			{true, true, &yes, true},
			{true, true, &no, false},
			{false, true, nil, false},
			{true, false, &yes, false},
		} {
			if got := core.Active(c.held, c.optedIn, c.personal); got != c.want {
				t.Errorf("Active(%t, %t, %v) = %t", c.held, c.optedIn, c.personal, got)
			}
		}
	})
}

// TestUnitPreviewsEmptyCatalogue checks the read while no feature is in
// preview, as the compiled catalogue is: an empty list, with both
// settings read back.
func TestUnitPreviewsEmptyCatalogue(t *testing.T) {
	if len(root.Previews()) != 0 {
		t.Skip("the compiled catalogue marks a preview")
	}
	s := &store{}
	var n int
	svc := core.New(s, inTx(&n), nil)
	acct, member := kernel.NewAccountID(), kernel.NewMemberID()
	if err := svc.SetFeaturePreviewsEnabled(t.Context(), acct, true); err != nil {
		t.Fatal(err)
	}
	got, err := svc.FeaturePreviews(t.Context(), kernel.Principal{
		OrganizationID: kernel.NewOrganizationID(), AccountID: acct, MemberID: member,
	})
	if err != nil || got.Features == nil || len(got.Features) != 0 || !got.Account ||
		got.Personal == nil || !*got.Personal {
		t.Errorf("FeaturePreviews = %+v, %v", got, err)
	}
}

// TestUnitPreviewSettingsFailures checks that the read fails with the
// store.
func TestUnitPreviewSettingsFailures(t *testing.T) {
	acct, member := kernel.NewAccountID(), kernel.NewMemberID()
	p := kernel.Principal{OrganizationID: kernel.NewOrganizationID(), AccountID: acct, MemberID: member}
	for name, s := range map[string]*store{
		"opt-in":   {previewErr: errStore},
		"personal": {personalErr: errStore},
		"tier":     {err: errStore},
	} {
		var n int
		svc := core.New(s, inTx(&n), nil).WithPreviews(testPreviews)
		if _, err := svc.FeaturePreviews(t.Context(), p); !errors.Is(err, errStore) {
			t.Errorf("%s unreadable: %v", name, err)
		}
	}
	s := &store{personalErr: errStore}
	var n int
	svc := core.New(s, inTx(&n), nil)
	if err := svc.RemoveMember(t.Context(), acct, member); !errors.Is(err, errStore) {
		t.Errorf("RemoveMember = %v", err)
	}
	if _, err := svc.PersonalFeaturePreviewsEnabled(t.Context(), acct, member); !errors.Is(err, errStore) {
		t.Errorf("PersonalFeaturePreviewsEnabled = %v", err)
	}
}

// TestUnitPreviewSnapshot checks that the replica reads an account's
// opt-in once, until its payload, a missed notification or a write of it
// on this replica drops it.
func TestUnitPreviewSnapshot(t *testing.T) {
	s := &store{}
	var n int
	svc := core.New(s, inTx(&n), nil)
	ctx, acct, other := t.Context(), kernel.NewAccountID(), kernel.NewAccountID()
	org := kernel.NewOrganizationID()
	for _, step := range []struct {
		name       string
		invalidate func()
		reads      int
		want       bool
	}{
		{"first read", func() {}, 1, false},
		{"cached", func() {}, 1, false},
		{"another account", func() { svc.Invalidate(core.AccountKey(other)) }, 1, false},
		{"an organization", func() { svc.Invalidate(core.InvalidateKey(org)) }, 1, false},
		{"written on this replica", func() {
			if err := svc.SetFeaturePreviewsEnabled(ctx, acct, true); err != nil {
				t.Fatal(err)
			}
		}, 2, true},
		{"cached after the write", func() {}, 2, true},
		{"this account", func() { svc.Invalidate(core.AccountKey(acct)) }, 3, true},
		{"an account payload that names none", func() { svc.Invalidate("features:account:x") }, 4, true},
		{"a missed notification", svc.Flush, 5, true},
	} {
		step.invalidate()
		got, err := svc.FeaturePreviewsEnabled(ctx, acct)
		if err != nil || got != step.want || s.reads["previews"] != step.reads {
			t.Errorf("%s: %t, %v after %d reads; want %t after %d",
				step.name, got, err, s.reads["previews"], step.want, step.reads)
		}
	}
	s.previewErr = errStore
	if err := svc.SetFeaturePreviewsEnabled(ctx, acct, false); !errors.Is(err, errStore) {
		t.Errorf("a failed write = %v", err)
	}
}

// TestUnitPreviewSettingWrites checks that each write answers the
// previews with the write applied, that a principal naming no member has
// no personal setting to write, and that each write fails with the store.
func TestUnitPreviewSettingWrites(t *testing.T) {
	ctx := t.Context()
	acct := kernel.NewAccountID()
	member := kernel.Principal{
		OrganizationID: kernel.NewOrganizationID(), AccountID: acct, MemberID: kernel.NewMemberID(),
	}
	key := kernel.Principal{OrganizationID: member.OrganizationID, AccountID: acct}
	active := func(v core.Previews) []bool {
		out := make([]bool, 0, len(v.Features))
		for _, f := range v.Features {
			out = append(out, f.Active)
		}
		return out
	}
	s := &store{}
	var n int
	svc := core.New(s, inTx(&n), nil).WithPreviews(testPreviews)
	if v, err := svc.FeaturePreviews(ctx, key); err != nil || v.Account || v.Personal != nil {
		t.Fatalf("before = %+v, %v", v, err)
	}
	v, err := svc.SetAccountFeaturePreviews(ctx, key, true)
	if err != nil || !v.Account || v.Personal != nil || !slices.Equal(active(v), []bool{true, true}) {
		t.Errorf("an API key opts in = %+v, %v", v, err)
	}
	if _, err := svc.SetPersonalFeaturePreviews(ctx, key, false); !errors.Is(err, core.ErrNoMember) {
		t.Errorf("an API key's personal write = %v", err)
	}
	v, err = svc.SetPersonalFeaturePreviews(ctx, member, false)
	if err != nil || !v.Account || v.Personal == nil || *v.Personal ||
		!slices.Equal(active(v), []bool{false, false}) {
		t.Errorf("a member opts out = %+v, %v", v, err)
	}
	v, err = svc.SetAccountFeaturePreviews(ctx, member, false)
	if err != nil || v.Account || v.Personal == nil || *v.Personal {
		t.Errorf("a member opts the account out = %+v, %v", v, err)
	}
	if v, err := svc.FeaturePreviews(ctx, member); err != nil || v.Account || *v.Personal {
		t.Errorf("after = %+v, %v", v, err)
	}
	for name, c := range map[string]struct {
		s     *store
		write func(*core.Service) error
	}{
		"opt-in unwritable": {&store{previewErr: errStore}, func(svc *core.Service) error {
			_, err := svc.SetAccountFeaturePreviews(ctx, member, true)
			return err
		}},
		"personal unreadable after the opt-in": {&store{personalErr: errStore}, func(svc *core.Service) error {
			_, err := svc.SetAccountFeaturePreviews(ctx, member, true)
			return err
		}},
		"personal unwritable": {&store{personalErr: errStore}, func(svc *core.Service) error {
			_, err := svc.SetPersonalFeaturePreviews(ctx, member, true)
			return err
		}},
		"opt-in unreadable after the personal write": {&store{previewErr: errStore}, func(svc *core.Service) error {
			_, err := svc.SetPersonalFeaturePreviews(ctx, member, true)
			return err
		}},
	} {
		var n int
		if err := c.write(core.New(c.s, inTx(&n), nil).WithPreviews(testPreviews)); !errors.Is(err, errStore) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
