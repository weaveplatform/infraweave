package azureblob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
)

// delegationLife is how long a fetched user delegation key lasts; Azure
// allows up to seven days. A key outlives every URL signed with it, so a
// URL never ends before its TTL.
const delegationLife = 24 * time.Hour

// Bucket is a Blob Storage container.
type Bucket struct {
	container *container.Client
	name      string
	partSize  int64
	protocol  sas.Protocol
	sharedKey *service.SharedKeyCredential
	now       func() time.Time

	// delegate fetches a user delegation key; the key is cached until it
	// would end before a URL signed with it.
	delegate func(context.Context, service.KeyInfo) (*service.UserDelegationCredential, error)
	mu       sync.Mutex
	key      *service.UserDelegationCredential
	keyEnds  time.Time
}

// Options adjust how the container connects and signs, as an emulator's
// tests do. Zero Options are production's: DefaultAzureCredential, and
// URLs signed with a user delegation key.
type Options struct {
	// SharedKey authenticates with the account key and signs URLs with
	// it, as Azurite needs.
	SharedKey *service.SharedKeyCredential
	Client    *service.ClientOptions
}

// New returns container, or c.Container when container is "". Writes
// above partSize go up as blocks of partSize.
func New(
	c objectstore.AzureBlobConfig,
	containerName string,
	partSize int64,
	o Options,
) (*Bucket, error) {
	if containerName == "" {
		containerName = c.Container
	}
	var (
		svc *service.Client
		err error
	)
	if o.SharedKey != nil {
		svc, err = service.NewClientWithSharedKeyCredential(c.AccountURL, o.SharedKey, o.Client)
	} else {
		var cred azcore.TokenCredential
		if cred, err = azidentity.NewDefaultAzureCredential(nil); err == nil {
			svc, err = service.NewClient(c.AccountURL, cred, o.Client)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("azureblob: %w", err)
	}
	b := &Bucket{
		container: svc.NewContainerClient(containerName),
		name:      containerName,
		partSize:  partSize,
		protocol:  sas.ProtocolHTTPS,
		sharedKey: o.SharedKey,
		now:       time.Now,
		delegate: func(ctx context.Context, k service.KeyInfo) (*service.UserDelegationCredential, error) {
			return svc.GetUserDelegationCredential(
				ctx,
				k,
				nil,
			) //nolint:wrapcheck // delegation wraps it
		},
	}
	// Azurite serves plain HTTP; a SAS for HTTPS alone would never answer.
	if strings.HasPrefix(c.AccountURL, "http://") {
		b.protocol = sas.ProtocolHTTPSandHTTP
	}
	return b, nil
}

// Container is the container's SDK client, for a test that sets it up.
func (b *Bucket) Container() *container.Client { return b.container }

// blockID names block n; every ID of a blob must be the same length.
func blockID(n int) string {
	return base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "%08d", n))
}

// Put sends an object up to the part size in one Put Blob, and a larger
// one as blocks committed only once the source has given exactly size
// bytes. Each request carries a CRC64 the service checks. A failed write
// leaves only uncommitted blocks, which are unreadable and which Azure
// discards after seven days.
func (b *Bucket) Put(
	ctx context.Context,
	key string,
	r io.Reader,
	size int64,
) (objectstore.Object, error) {
	bb := b.container.NewBlockBlobClient(key)
	if size <= b.partSize {
		body, err := objectstore.ReadPart(r, make([]byte, size), size)
		if err == nil {
			err = objectstore.AtEnd(r)
		}
		if err != nil {
			return objectstore.Object{}, err //nolint:wrapcheck // ErrSize names the size
		}
		if _, err := bb.Upload(
			ctx,
			streaming.NopCloser(bytes.NewReader(body)),
			&blockblob.UploadOptions{
				TransactionalValidation: blob.TransferValidationTypeComputeCRC64(),
			},
		); err != nil {
			return objectstore.Object{}, fmt.Errorf("azureblob: put %s: %w", key, err)
		}
		return objectstore.Object{Size: size, SHA256: sha256.Sum256(body)}, nil
	}
	whole := sha256.New()
	buf := make([]byte, b.partSize)
	var ids []string
	for left := size; left > 0; {
		part, err := objectstore.ReadPart(r, buf, min(left, b.partSize))
		if err != nil {
			return objectstore.Object{}, err //nolint:wrapcheck // as above
		}
		left -= int64(len(part))
		whole.Write(part)
		id := blockID(len(ids))
		if _, err := bb.StageBlock(
			ctx,
			id,
			streaming.NopCloser(bytes.NewReader(part)),
			&blockblob.StageBlockOptions{
				TransactionalValidation: blob.TransferValidationTypeComputeCRC64(),
			},
		); err != nil {
			return objectstore.Object{}, fmt.Errorf(
				"azureblob: put %s block %d: %w",
				key,
				len(ids),
				err,
			)
		}
		ids = append(ids, id)
	}
	if err := objectstore.AtEnd(r); err != nil {
		return objectstore.Object{}, err //nolint:wrapcheck // as above
	}
	if _, err := bb.CommitBlockList(ctx, ids, nil); err != nil {
		return objectstore.Object{}, fmt.Errorf("azureblob: commit %s: %w", key, err)
	}
	obj := objectstore.Object{Size: size}
	whole.Sum(obj.SHA256[:0])
	return obj, nil
}

func isNotFound(err error) bool {
	var re *azcore.ResponseError
	return errors.As(err, &re) && re.StatusCode == http.StatusNotFound
}

func notFound(op, key string, err error) error {
	if isNotFound(err) {
		return objectstore.ErrNotFound
	}
	return fmt.Errorf("azureblob: %s %s: %w", op, key, err)
}

// Get streams the object.
func (b *Bucket) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := b.container.NewBlobClient(key).DownloadStream(ctx, nil)
	if err != nil {
		return nil, notFound("get", key, err)
	}
	return out.Body, nil
}

// Head returns the object's size.
func (b *Bucket) Head(ctx context.Context, key string) (int64, error) {
	out, err := b.container.NewBlobClient(key).GetProperties(ctx, nil)
	if err != nil {
		return 0, notFound("head", key, err)
	}
	return *out.ContentLength, nil
}

// Delete removes the object; a missing key is no error.
func (b *Bucket) Delete(ctx context.Context, key string) error {
	if _, err := b.container.NewBlobClient(key).Delete(ctx, nil); err != nil && !isNotFound(err) {
		return fmt.Errorf("azureblob: delete %s: %w", key, err)
	}
	return nil
}

// delegation returns a user delegation key that lasts until at least
// until, fetching a new one when the cached key ends sooner.
func (b *Bucket) delegation(
	ctx context.Context,
	until time.Time,
) (*service.UserDelegationCredential, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.key != nil && !b.keyEnds.Before(until) {
		return b.key, nil
	}
	// The key starts a little early, so a service clock behind ours
	// still accepts a URL signed now.
	start, ends := b.now().Add(-5*time.Minute).UTC(), until.Add(delegationLife).UTC()
	startS, endsS := start.Format(sas.TimeFormat), ends.Format(sas.TimeFormat)
	key, err := b.delegate(ctx, service.KeyInfo{Start: &startS, Expiry: &endsS})
	if err != nil {
		return nil, fmt.Errorf("azureblob: user delegation key: %w", err)
	}
	b.key, b.keyEnds = key, ends
	return key, nil
}

// SignURL signs a blob SAS: read for a GET, create and write for a PUT.
// A SAS binds neither the size nor a checksum, so Commit's Head refuses
// another size and Commit reads the object back to compare its SHA-256.
// A PUT sends x-ms-blob-type, which Put Blob requires.
func (b *Bucket) SignURL(
	ctx context.Context,
	method, key string,
	ttl time.Duration,
	_ objectstore.Constraints,
) (objectstore.Signed, error) {
	var perm sas.BlobPermissions
	h := http.Header{}
	switch method {
	case http.MethodGet:
		perm.Read = true
	case http.MethodPut:
		perm.Create, perm.Write = true, true
		h.Set("x-ms-blob-type", "BlockBlob")
	default:
		return objectstore.Signed{}, fmt.Errorf("%w: %s", objectstore.ErrMethod, method)
	}
	expiry := b.now().Add(ttl).UTC()
	v := sas.BlobSignatureValues{
		Protocol:      b.protocol,
		ExpiryTime:    expiry,
		Permissions:   perm.String(),
		ContainerName: b.name,
		BlobName:      key,
	}
	var (
		qp  sas.QueryParameters
		err error
	)
	if b.sharedKey != nil {
		qp, err = v.SignWithSharedKey(b.sharedKey)
	} else {
		var udk *service.UserDelegationCredential
		if udk, err = b.delegation(ctx, expiry); err == nil {
			qp, err = v.SignWithUserDelegation(udk)
		}
	}
	if err != nil {
		return objectstore.Signed{}, fmt.Errorf("azureblob: sign %s %s: %w", method, key, err)
	}
	return objectstore.Signed{
		URL:    b.container.NewBlobClient(key).URL() + "?" + qp.Encode(),
		Header: h,
	}, nil
}
