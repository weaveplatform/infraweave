package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
)

// Bucket is an S3 bucket, on AWS or an S3-compatible service.
type Bucket struct {
	client   *s3.Client
	presign  *s3.PresignClient
	bucket   string
	partSize int64
	sse      types.ServerSideEncryption
	kmsKeyID *string
}

// Option adjusts the SDK's configuration, as a test's static keys do.
type Option = func(*config.LoadOptions) error

// New returns bucket, or c.Bucket when bucket is "", with credentials
// from the SDK's default chain. Writes above partSize go in parts of
// partSize.
func New(
	ctx context.Context,
	c objectstore.S3Config,
	bucket string,
	partSize int64,
	opts ...Option,
) (*Bucket, error) {
	if bucket == "" {
		bucket = c.Bucket
	}
	cfg, err := config.LoadDefaultConfig(
		ctx,
		append([]Option{config.WithRegion(c.Region)}, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("s3: %w", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
		o.UsePathStyle = c.PathStyle
		// Every write carries its SHA-256 explicitly; the SDK adds no
		// checksum of its own.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	b := &Bucket{
		client:   client,
		presign:  s3.NewPresignClient(client),
		bucket:   bucket,
		partSize: partSize,
	}
	switch c.Encryption {
	case "s3":
		b.sse = types.ServerSideEncryptionAes256
	case "kms":
		b.sse, b.kmsKeyID = types.ServerSideEncryptionAwsKms, aws.String(c.KMSKeyID)
	}
	return b, nil
}

// Client is the bucket's SDK client, for a test that sets the bucket up.
func (b *Bucket) Client() *s3.Client { return b.client }

func b64(sum []byte) *string { return aws.String(base64.StdEncoding.EncodeToString(sum)) }

// Put sends an object up to the part size in one PutObject with
// x-amz-checksum-sha256, and a larger one as a multipart upload whose
// parts each carry theirs. The multipart upload completes only once the
// source has given exactly size bytes, and is aborted otherwise, so a
// failed write leaves nothing readable.
func (b *Bucket) Put(
	ctx context.Context,
	key string,
	r io.Reader,
	size int64,
) (objectstore.Object, error) {
	if size <= b.partSize {
		return b.putOne(ctx, key, r, size)
	}
	return b.putParts(ctx, key, r, size)
}

func (b *Bucket) putOne(
	ctx context.Context,
	key string,
	r io.Reader,
	size int64,
) (objectstore.Object, error) {
	body, err := objectstore.ReadPart(r, make([]byte, size), size)
	if err == nil {
		err = objectstore.AtEnd(r)
	}
	if err != nil {
		return objectstore.Object{}, err //nolint:wrapcheck // ErrSize names the size
	}
	obj := objectstore.Object{Size: size, SHA256: sha256.Sum256(body)}
	if _, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:               &b.bucket,
		Key:                  &key,
		Body:                 bytes.NewReader(body),
		ContentLength:        &size,
		ChecksumSHA256:       b64(obj.SHA256[:]),
		ServerSideEncryption: b.sse,
		SSEKMSKeyId:          b.kmsKeyID,
	}); err != nil {
		return objectstore.Object{}, fmt.Errorf("s3: put %s: %w", key, err)
	}
	return obj, nil
}

func (b *Bucket) putParts(
	ctx context.Context,
	key string,
	r io.Reader,
	size int64,
) (objectstore.Object, error) {
	up, err := b.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:               &b.bucket,
		Key:                  &key,
		ChecksumAlgorithm:    types.ChecksumAlgorithmSha256,
		ServerSideEncryption: b.sse,
		SSEKMSKeyId:          b.kmsKeyID,
	})
	if err != nil {
		return objectstore.Object{}, fmt.Errorf("s3: put %s: %w", key, err)
	}
	obj, err := b.upload(ctx, key, up.UploadId, r, size)
	if err != nil {
		// The abort runs even when ctx has ended, so no part is left
		// behind to be billed.
		_, aerr := b.client.AbortMultipartUpload(
			context.WithoutCancel(ctx),
			&s3.AbortMultipartUploadInput{
				Bucket: &b.bucket, Key: &key, UploadId: up.UploadId,
			},
		)
		if aerr != nil {
			err = errors.Join(err, fmt.Errorf("s3: abort %s: %w", key, aerr))
		}
		return objectstore.Object{}, err
	}
	return obj, nil
}

func (b *Bucket) upload(
	ctx context.Context,
	key string,
	id *string,
	r io.Reader,
	size int64,
) (objectstore.Object, error) {
	whole := sha256.New()
	buf := make([]byte, b.partSize)
	var parts []types.CompletedPart
	for left, n := size, int32(1); left > 0; n++ {
		part, err := objectstore.ReadPart(r, buf, min(left, b.partSize))
		if err != nil {
			return objectstore.Object{}, err //nolint:wrapcheck // as above
		}
		left -= int64(len(part))
		whole.Write(part)
		sum := sha256.Sum256(part)
		out, err := b.client.UploadPart(ctx, &s3.UploadPartInput{
			Bucket:         &b.bucket,
			Key:            &key,
			UploadId:       id,
			PartNumber:     aws.Int32(n),
			Body:           bytes.NewReader(part),
			ContentLength:  aws.Int64(int64(len(part))),
			ChecksumSHA256: b64(sum[:]),
		})
		if err != nil {
			return objectstore.Object{}, fmt.Errorf("s3: put %s part %d: %w", key, n, err)
		}
		parts = append(parts, types.CompletedPart{
			ETag: out.ETag, PartNumber: aws.Int32(n), ChecksumSHA256: out.ChecksumSHA256,
		})
	}
	if err := objectstore.AtEnd(r); err != nil {
		return objectstore.Object{}, err //nolint:wrapcheck // as above
	}
	if _, err := b.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          &b.bucket,
		Key:             &key,
		UploadId:        id,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	}); err != nil {
		return objectstore.Object{}, fmt.Errorf("s3: complete %s: %w", key, err)
	}
	obj := objectstore.Object{Size: size}
	whole.Sum(obj.SHA256[:0])
	return obj, nil
}

// notFound maps S3's 404s, NoSuchKey from GetObject and a bodiless 404
// from HeadObject, to ErrNotFound.
func notFound(op, key string, err error) error {
	var re *awshttp.ResponseError
	if errors.As(err, &re) && re.HTTPStatusCode() == http.StatusNotFound {
		return objectstore.ErrNotFound
	}
	return fmt.Errorf("s3: %s %s: %w", op, key, err)
}

// Get streams the object.
func (b *Bucket) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &b.bucket, Key: &key})
	if err != nil {
		return nil, notFound("get", key, err)
	}
	return out.Body, nil
}

// Head returns the object's size.
func (b *Bucket) Head(ctx context.Context, key string) (int64, error) {
	out, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &b.bucket, Key: &key})
	if err != nil {
		return 0, notFound("head", key, err)
	}
	return aws.ToInt64(out.ContentLength), nil
}

// Delete removes the object; S3 answers a missing key with success.
func (b *Bucket) Delete(ctx context.Context, key string) error {
	if _, err := b.client.DeleteObject(
		ctx,
		&s3.DeleteObjectInput{Bucket: &b.bucket, Key: &key},
	); err != nil {
		return fmt.Errorf("s3: delete %s: %w", key, err)
	}
	return nil
}

// SignURL presigns a GetObject or PutObject with SigV4. A PUT binds
// Content-Length and, when given, x-amz-checksum-sha256, which the
// client sends among the returned headers.
func (b *Bucket) SignURL(
	ctx context.Context,
	method, key string,
	ttl time.Duration,
	c objectstore.Constraints,
) (objectstore.Signed, error) {
	expires := s3.WithPresignExpires(ttl)
	var (
		req *v4.PresignedHTTPRequest
		err error
	)
	switch method {
	case http.MethodGet:
		req, err = b.presign.PresignGetObject(
			ctx,
			&s3.GetObjectInput{Bucket: &b.bucket, Key: &key},
			expires,
		)
	case http.MethodPut:
		in := &s3.PutObjectInput{
			Bucket:               &b.bucket,
			Key:                  &key,
			ContentLength:        aws.Int64(c.Size),
			ServerSideEncryption: b.sse,
			SSEKMSKeyId:          b.kmsKeyID,
		}
		if len(c.SHA256) != 0 {
			in.ChecksumSHA256 = b64(c.SHA256)
		}
		req, err = b.presign.PresignPutObject(ctx, in, expires)
	default:
		return objectstore.Signed{}, fmt.Errorf("%w: %s", objectstore.ErrMethod, method)
	}
	if err != nil {
		return objectstore.Signed{}, fmt.Errorf("s3: sign %s %s: %w", method, key, err)
	}
	h := req.SignedHeader.Clone()
	// The client sets Host from the URL itself.
	h.Del("Host")
	return objectstore.Signed{URL: req.URL, Header: h}, nil
}
