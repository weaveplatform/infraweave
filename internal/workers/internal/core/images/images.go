package images

import (
	"context"
	"strings"
	"unicode"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// DefaultPrivate is the account read's fallback and the built-in image for
// Terraform and Terragrunt runs. With no account default, an OpenTofu run
// uses its own vendor image; a launcher's configured default overrides both.
// It is untagged, so it resolves to the latest release. This reference must
// match config.VendorRunnerImage in internal/agent/config; neither imports
// the other.
//
// ref: decision 0008 § "Runner images"
const DefaultPrivate = "ghcr.io/weaveplatform/infraweave-runner-terraform"

// maxLength bounds an image reference, as Docker's own references are.
const maxLength = 255

// Images are an account's default runner images for runs on private and
// on public pools; nil takes the launcher's configured default, else the
// stack's vendor image.
type Images struct {
	Private *string
	Public  *string
}

// Store is what the service needs of the images' storage.
type Store interface {
	// RunnerImages reads acct's images, both nil when it sets none.
	RunnerImages(ctx context.Context, acct kernel.AccountID) (Images, error)
	// SetRunnerImages writes acct's images.
	SetRunnerImages(ctx context.Context, acct kernel.AccountID, in Images) error
}

// Service is the default runner images' logic.
type Service struct{ store Store }

// New returns the service over store.
func New(store Store) *Service { return &Service{store: store} }

// Private answers the account's default for private pools, or DefaultPrivate
// when none is set. The launcher selects each run's vendor fallback.
func (s *Service) Private(ctx context.Context, acct kernel.AccountID) (string, error) {
	in, err := s.store.RunnerImages(ctx, acct)
	if err != nil {
		return "", err //nolint:wrapcheck // the store names the read
	}
	if in.Private != nil {
		return *in.Private, nil
	}
	return DefaultPrivate, nil
}

// Set replaces acct's images with in's and answers them as stored: each
// trimmed, an empty one nil. The route has decided ACCOUNT_ADMIN.
func (s *Service) Set(ctx context.Context, acct kernel.AccountID, in Images) (Images, error) {
	var errs []kernel.FieldError
	in.Private = clean(in.Private, "/privateWorkerPoolRunnerImage", &errs)
	in.Public = clean(in.Public, "/publicWorkerPoolRunnerImage", &errs)
	if len(errs) > 0 {
		return Images{}, &kernel.Invalid{Detail: "The runner images are refused.", Errors: errs}
	}
	if err := s.store.SetRunnerImages(ctx, acct, in); err != nil {
		return Images{}, err //nolint:wrapcheck // the store names the write
	}
	return in, nil
}

// clean is image trimmed, nil when empty, refusing at pointer one that
// is too long or holds a space or a control character.
func clean(image *string, pointer string, errs *[]kernel.FieldError) *string {
	if image == nil {
		return nil
	}
	v := strings.TrimSpace(*image)
	switch {
	case v == "":
		return nil
	case len(v) > maxLength:
		*errs = append(*errs, kernel.FieldError{
			Pointer: pointer, Reason: kernel.ReasonMaxLength,
			Message: "An image reference is at most 255 characters.",
		})
	case strings.IndexFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0:
		*errs = append(*errs, kernel.FieldError{
			Pointer: pointer, Reason: kernel.ReasonRule,
			Message: "An image reference holds no space or control character.",
		})
	}
	return &v
}
