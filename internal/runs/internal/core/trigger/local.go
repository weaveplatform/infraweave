package trigger

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// MaxWorkspace bounds a local workspace's archive at 2 GiB, the bound of a
// saved workspace, which the launcher also holds a download to.
//
// ref: decision 0008 § "The run specification and its secrets"
const MaxWorkspace = 2 << 30

// ErrLocalPreview reports a local preview on a stack that does not enable
// local previews.
var ErrLocalPreview = errors.New("runs: the stack does not enable local previews")

// Objects records, signs and commits the uploads of local workspaces;
// blobs.Objects is one.
type Objects interface {
	Begin(ctx context.Context, store objectstore.Name, key string, size int64, sum []byte) error
	SignPut(ctx context.Context, store objectstore.Name, key string, size int64,
		sum []byte) (objectstore.Signed, error)
	CommitUpload(
		ctx context.Context,
		store objectstore.Name,
		key string,
	) (objectstore.Object, error)
	Live(ctx context.Context, store objectstore.Name, key string) (bool, error)
}

// Upload is a local workspace's upload: the ID a proposal names, and the
// signed PUT URL with the headers its request carries.
type Upload struct {
	ID     kernel.ULID
	URL    string
	Header map[string]string
}

// UploadWorkspace records a pending local workspace of size bytes with
// SHA-256 sum for p's account's stack slug, and signs its PUT, which
// accepts those bytes alone. A size outside 1 byte to MaxWorkspace, or a
// sum that is not 32 bytes, is a *kernel.Invalid; a stack that does not
// enable local previews refuses with ErrLocalPreview. The upload expires
// with the uploads store's retention, one day by default.
//
// ref: decision 0005 § "The stores of 0003"
func (s *Service) UploadWorkspace(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	size int64,
	sum []byte,
) (Upload, error) {
	if err := declared(size, sum); err != nil {
		return Upload{}, err
	}
	var up Upload
	err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		st, err := s.previewable(ctx, p.AccountID, slug)
		if err != nil {
			return err
		}
		id := kernel.NewULID()
		k := workspaceKey(p.AccountID, st.ID, id)
		if err := s.d.Objects.Begin(ctx, objectstore.Uploads, k, size, sum); err != nil {
			return err //nolint:wrapcheck // blobs names the object
		}
		signed, err := s.d.Objects.SignPut(ctx, objectstore.Uploads, k, size, sum)
		if err != nil {
			return err //nolint:wrapcheck // the drivers name the object
		}
		up = Upload{ID: id, URL: signed.URL, Header: make(map[string]string, len(signed.Header))}
		for h := range signed.Header {
			up.Header[h] = signed.Header.Get(h)
		}
		return nil
	})
	return up, err //nolint:wrapcheck // each step names itself
}

// ProposeWorkspace creates a PROPOSED run of p's account's stack slug on
// the local workspace upload uploaded, with the stack's tracked commit,
// else its branch head, as its commit's metadata, and the environment
// overrides ovs, or, with no overrides, answers the run of that upload
// that has not ended. The upload is committed with the run,
// so it is read from then on. An upload that is not the stack's, was
// never uploaded, or has expired is a *kernel.Invalid pointing at
// /workspace, as are overrides CheckOverrides refuses; a stack that does not enable local previews refuses with
// ErrLocalPreview.
//
// ref: decision 0008 § "The run specification and its secrets"
func (s *Service) ProposeWorkspace(
	ctx context.Context,
	p kernel.Principal,
	slug, upload string,
	ovs []lifecycle.Override,
) (read.View, error) {
	if err := CheckOverrides(ovs); err != nil {
		return read.View{}, err
	}
	var v read.View
	err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		st, err := s.previewable(ctx, p.AccountID, slug)
		if err != nil {
			return err
		}
		id, err := kernel.ParseULID(upload)
		if err != nil {
			return noUpload(upload)
		}
		k := workspaceKey(p.AccountID, st.ID, id)
		if err := s.commitUpload(ctx, k, upload); err != nil {
			return err
		}
		commit, err := s.commit(ctx, p.AccountID, st, "")
		if err != nil {
			return err
		}
		commit.Branch = st.Branch
		run, _, err := s.d.Lifecycle.Create(ctx, lifecycle.NewRun{
			Account: p.AccountID, Stack: st.ID, Type: kernel.RunTypeProposed, Commit: commit,
			Actor: lifecycle.ActorOf(p), LocalWorkspace: k, Overrides: ovs,
		})
		if err != nil {
			return err //nolint:wrapcheck // the lifecycle names the refusal
		}
		v, err = s.d.Views.One(ctx, p, run)
		return err //nolint:wrapcheck // the read names itself
	})
	return v, err //nolint:wrapcheck // each step names itself
}

// previewable reads acct's stack slug's settings, refusing a stack that
// does not enable local previews.
func (s *Service) previewable(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
) (stacksapi.Settings, error) {
	st, err := s.d.Stacks.BySlug(ctx, acct, slug)
	if err != nil {
		return stacksapi.Settings{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	settings, err := s.d.Stacks.Settings(ctx, acct, st.ID)
	if err != nil {
		return stacksapi.Settings{}, err //nolint:wrapcheck // as above
	}
	if !settings.LocalPreviewEnabled {
		return stacksapi.Settings{}, fmt.Errorf("%w: %s", ErrLocalPreview, slug)
	}
	return settings, nil
}

// commitUpload makes the upload at key live, unless a proposal already
// has. An upload with no pending or live object, or whose object is not
// the size it declared, is refused.
func (s *Service) commitUpload(ctx context.Context, key, upload string) error {
	live, err := s.d.Objects.Live(ctx, objectstore.Uploads, key)
	if err != nil || live {
		return err //nolint:wrapcheck // blobs names the object
	}
	_, err = s.d.Objects.CommitUpload(ctx, objectstore.Uploads, key)
	switch {
	case errors.Is(err, blobs.ErrNotUploaded):
		return noUpload(upload)
	case errors.Is(err, objectstore.ErrSize):
		return &kernel.Invalid{
			Detail: "The local workspace's upload does not hold the size it declared.",
			Errors: []kernel.FieldError{{
				Pointer: "/workspace", Reason: kernel.ReasonRule,
				Message: fmt.Sprintf("Upload %s holds other bytes than it declared.", upload),
			}},
		}
	}
	return err //nolint:wrapcheck // blobs names the object
}

// noUpload refuses a proposal whose upload the stack does not have.
func noUpload(upload string) error {
	return &kernel.Invalid{
		Detail: "The stack has no such local workspace upload; it may have expired.",
		Errors: []kernel.FieldError{{
			Pointer: "/workspace", Reason: kernel.ReasonNotFound,
			Message: fmt.Sprintf("No uploaded local workspace %q on this stack.", upload),
		}},
	}
}

// declared refuses a local workspace's size and SHA-256 that no archive
// may declare.
func declared(size int64, sum []byte) error {
	var errs []kernel.FieldError
	switch {
	case size < 1:
		errs = append(errs, kernel.FieldError{
			Pointer: "/size", Reason: kernel.ReasonMinimum, Message: "An archive holds a byte.",
		})
	case size > MaxWorkspace:
		errs = append(errs, kernel.FieldError{
			Pointer: "/size", Reason: kernel.ReasonMaximum,
			Message: fmt.Sprintf("A local workspace is at most %d bytes.", MaxWorkspace),
		})
	}
	if len(sum) != sha256.Size {
		errs = append(errs, kernel.FieldError{
			Pointer: "/sha256", Reason: kernel.ReasonPattern,
			Message: "A SHA-256 is 64 hexadecimal digits.",
		})
	}
	if errs == nil {
		return nil
	}
	return &kernel.Invalid{Detail: "The local workspace's declaration is refused.", Errors: errs}
}

// workspaceKey is the uploads store's key of acct's stack's local
// workspace upload id.
func workspaceKey(acct kernel.AccountID, stack, id kernel.ULID) string {
	return fmt.Sprintf("%s/%s/%s", acct, stack, id)
}
