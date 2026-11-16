package core

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// MaxUpload is the largest state file an upload URL takes.
//
// ref: decision 0010 § "Upload, import and download URLs"
const MaxUpload = 100 << 20

// ErrUploaded reports a PUT to an upload URL whose object another PUT has
// written or begun: a URL takes one upload, so one cut short needs a new
// URL.
var ErrUploaded = errors.New("this upload URL has taken its upload")

// ErrGone reports a download URL's version that no longer exists, deleted
// by retention or with its stack.
var ErrGone = errors.New("the state version is gone")

// UploadKey is the key in the uploads store of the state file subject
// uploads as object in acct, so an import finds it only when the importer
// is the principal that asked for its URL. subject is hashed into one
// segment, since a caller's name may hold any character.
func UploadKey(acct kernel.AccountID, object kernel.ULID, subject string) string {
	return fmt.Sprintf("%s/state/%s/%x", acct, object, sha256.Sum256([]byte(subject)))
}

// Upload stores size bytes of body as the state file subject uploads as
// object in acct, sealed under a content key of its own in the uploads
// store, and makes it live once whole: ErrUploaded when the key already
// has an object. The uploads store's retention expires it unless an
// import deletes it first.
//
// ref: decision 0010 § "Upload, import and download URLs"
func (s *Service) Upload(ctx context.Context, acct kernel.AccountID, object kernel.ULID, subject string, body io.Reader, size int64) error {
	key := UploadKey(acct, object, subject)
	_, err := s.objects.PutSealed(ctx, objectstore.Uploads, key, body, size)
	if errors.Is(err, blobs.ErrKeyInUse) {
		return ErrUploaded
	}
	if err != nil {
		return err //nolint:wrapcheck // blobs names the object
	}
	//nolint:wrapcheck // blobs names the object
	return tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		return s.objects.Commit(ctx, objectstore.Uploads, key)
	})
}

// Download opens acct's stack's version, streaming unsealed: ErrGone when
// the version, or its object, no longer exists. The caller closes the
// contents.
func (s *Service) Download(ctx context.Context, acct kernel.AccountID, stack, version kernel.ULID) (Version, io.ReadCloser, error) {
	v, ok, err := s.store.Version(ctx, acct, version)
	switch {
	case err != nil:
		return Version{}, nil, err //nolint:wrapcheck // the store names the read
	case !ok || v.Stack != stack:
		return Version{}, nil, ErrGone
	}
	r, err := s.objects.Open(ctx, objectstore.States, v.Key)
	if errors.Is(err, objectstore.ErrNotFound) {
		return Version{}, nil, ErrGone
	}
	if err != nil {
		return Version{}, nil, err //nolint:wrapcheck // the objects name the key
	}
	return v, r, nil
}
