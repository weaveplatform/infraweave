package vendors

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/weaveplatform/infraweave/internal/iac/version"
	"github.com/weaveplatform/infraweave/internal/kernel"
	vendorsapi "github.com/weaveplatform/infraweave/internal/vendors/api"
	"github.com/weaveplatform/infraweave/internal/vendors/internal/index"
)

// Store keeps each vendor's releases.
type Store interface {
	// Add stores the releases of kind it does not hold, and reports how
	// many it added.
	Add(ctx context.Context, kind vendorsapi.Vendor, rs []index.Release) (int, error)
	// Versions are the stored releases of kind, in no order.
	Versions(ctx context.Context, kind vendorsapi.Vendor) ([]string, error)
}

// Kinds are the vendors the catalogue lists, in the order a refresh reads
// their indexes.
var Kinds = []vendorsapi.Vendor{vendorsapi.Terraform, vendorsapi.OpenTofu}

// Service is the version catalogue.
type Service struct {
	store   Store
	indexes map[vendorsapi.Vendor]index.Reader
}

// New is the catalogue over store, refreshed from indexes, one reader
// for each of Kinds.
func New(store Store, indexes map[vendorsapi.Vendor]index.Reader) *Service {
	return &Service{store: store, indexes: indexes}
}

// Refresh reads each vendor's index and stores the releases it lists that
// the store does not hold. An index it cannot read is logged and leaves
// that vendor's stored releases as they are, so a vendor's outage never
// empties the catalogue; the store failing fails the refresh, for the
// queue to retry.
func (s *Service) Refresh(ctx context.Context, log *slog.Logger) error {
	for _, kind := range Kinds {
		r, ok := s.indexes[kind]
		if !ok {
			continue
		}
		rs, err := r.Releases(ctx)
		if err != nil {
			log.Warn("release index unreadable; the stored releases stand",
				"vendor", string(kind), "error", err)
			continue
		}
		n, err := s.store.Add(ctx, kind, rs)
		if err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		log.Debug(
			"release index refreshed",
			"vendor",
			string(kind),
			"releases",
			len(rs),
			"added",
			n,
		)
	}
	return nil
}

// Versions are kind's stored releases, newest first.
func (s *Service) Versions(ctx context.Context, kind vendorsapi.Vendor) ([]string, error) {
	rs, err := s.releases(ctx, kind)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Version.String()
	}
	return out, nil
}

// constraintsPointer is the effective version operations' parameter.
const constraintsPointer = "#/query/constraints"

// Effective is the newest stored release of kind that constraints allow.
func (s *Service) Effective(
	ctx context.Context,
	kind vendorsapi.Vendor,
	constraints string,
) (string, error) {
	cs, err := version.ParseConstraints(constraints)
	if err != nil {
		return "", &kernel.Invalid{
			Detail: "The version constraints do not parse.",
			Errors: []kernel.FieldError{{
				Pointer: constraintsPointer, Reason: kernel.ReasonFormat, Message: err.Error(),
			}},
		}
	}
	rs, err := s.releases(ctx, kind)
	if err != nil {
		return "", err
	}
	r, ok := index.Resolve(rs, cs)
	if !ok {
		return "", &kernel.Invalid{
			Detail: fmt.Sprintf(
				"No %s version the catalogue lists satisfies the constraints.",
				kind,
			),
			Errors: []kernel.FieldError{{
				Pointer: constraintsPointer, Reason: kernel.ReasonRule,
				Message: fmt.Sprintf("No listed %s version satisfies %q.", kind, constraints),
			}},
		}
	}
	return r.Version.String(), nil
}

// ErrStored is a stored release that does not parse, which the refresh
// never stores.
var ErrStored = errors.New("a stored release does not parse")

// releases are kind's stored releases, newest first.
func (s *Service) releases(ctx context.Context, kind vendorsapi.Vendor) ([]index.Release, error) {
	versions, err := s.store.Versions(ctx, kind)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	out := make([]index.Release, len(versions))
	for i, raw := range versions {
		v, err := version.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %s %q", ErrStored, kind, raw)
		}
		out[i] = index.Release{Version: v}
	}
	slices.SortFunc(out, func(a, b index.Release) int { return b.Version.Compare(a.Version) })
	return out, nil
}
