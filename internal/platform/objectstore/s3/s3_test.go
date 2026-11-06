package s3_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/objectstoretest"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/s3"
	"github.com/weaveplatform/infraweave/test/acceptance"
)

const accessKey, secretKey = "infraweave-test", "infraweave-test-secret"

var static = config.WithCredentialsProvider(
	credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
)

// TestAccS3Contract runs the contract against RustFS, the harness's
// @objectstore image.
func TestAccS3Contract(t *testing.T) {
	endpoint := objectstoretest.Emulator(t, acceptance.ImageObjectStore, "9000/tcp",
		testcontainers.WithEnv(map[string]string{
			"RUSTFS_ACCESS_KEY": accessKey,
			"RUSTFS_SECRET_KEY": secretKey,
		}),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/health").WithPort("9000/tcp")),
	)
	c := objectstore.Defaults().S3
	c.Bucket, c.Endpoint, c.PathStyle = "contract", endpoint, true
	b, err := s3.New(t.Context(), c, "", objectstore.MinPartSize, static,
		config.WithHTTPClient(objectstoretest.NoMD5(t, http.DefaultTransport)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Client().
		CreateBucket(t.Context(), &awss3.CreateBucketInput{Bucket: aws.String("contract")}); err != nil {
		t.Fatal(err)
	}
	objectstoretest.Run(t, b, objectstoretest.Options{
		PartSize: objectstore.MinPartSize,
		Skip:     objectstoretest.Skip(objectstore.DriverS3),
	})
	objectstoretest.Canceled(t, b, objectstore.MinPartSize)
}

// TestAccS3Cloud runs the contract against Amazon S3, in the
// objectstore-cloud job, as the identity its credentials hold; the AWS
// SDK's default chain finds them.
func TestAccS3Cloud(t *testing.T) {
	id, v := objectstoretest.Cloud(t, "INFRAWEAVE_TEST_S3_BUCKET", "INFRAWEAVE_TEST_S3_REGION")
	c := objectstore.Defaults().S3
	c.Bucket, c.Region = v[0], v[1]
	b, err := s3.New(t.Context(), c, "", objectstore.MinPartSize,
		config.WithHTTPClient(objectstoretest.NoMD5(t, http.DefaultTransport)))
	if err != nil {
		t.Fatal(err)
	}
	objectstoretest.Run(t, b, objectstoretest.Options{
		PartSize: objectstore.MinPartSize,
		Skip:     objectstoretest.Unbound[objectstore.DriverS3],
		Identity: id,
	})
	objectstoretest.Canceled(t, b, objectstore.MinPartSize)
}

// TestUnitS3Encryption checks that a signed PUT carries the configured
// server-side encryption, which the client must send as signed.
func TestUnitS3Encryption(t *testing.T) {
	for _, tc := range []struct {
		encryption, kmsKeyID, want, wantKey string
	}{
		{"none", "", "", ""},
		{"s3", "", "AES256", ""},
		{"kms", "alias/infraweave", "aws:kms", "alias/infraweave"},
	} {
		t.Run(tc.encryption, func(t *testing.T) {
			c := objectstore.Defaults().S3
			c.Bucket, c.Region, c.Encryption, c.KMSKeyID = "b", "eu-west-1", tc.encryption, tc.kmsKeyID
			b, err := s3.New(t.Context(), c, "", objectstore.MinPartSize, static)
			if err != nil {
				t.Fatal(err)
			}
			s, err := b.SignURL(
				t.Context(),
				http.MethodPut,
				"k",
				time.Minute,
				objectstore.Constraints{Size: 1},
			)
			if err != nil {
				t.Fatal(err)
			}
			if got := s.Header.Get("X-Amz-Server-Side-Encryption"); got != tc.want {
				t.Errorf("encryption %q, want %q", got, tc.want)
			}
			if got := s.Header.Get(
				"X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id",
			); got != tc.wantKey {
				t.Errorf("KMS key %q, want %q", got, tc.wantKey)
			}
		})
	}
}
