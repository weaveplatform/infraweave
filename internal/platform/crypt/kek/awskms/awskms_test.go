package awskms_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/go-chi/chi/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/awskms"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/kektest"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/objectstoretest"
	"github.com/weaveplatform/infraweave/test/acceptance"
)

func endpoint(url string) func(*kms.Options) {
	return func(o *kms.Options) { o.BaseEndpoint = aws.String(url) }
}

func environment(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_PROFILE", "")
}

// TestAccAWSKMS runs the contract against moto's KMS, with the key named
// by its ARN and by an alias.
func TestAccAWSKMS(t *testing.T) {
	environment(t)
	url := objectstoretest.Emulator(t, acceptance.ImageMoto, "5000/tcp",
		testcontainers.WithWaitStrategy(wait.ForHTTP("/moto-api/").WithPort("5000/tcp")))
	c := kek.AWSKMSConfig{Region: "us-east-1"}
	admin := kms.New(kms.Options{
		Region:       c.Region,
		BaseEndpoint: aws.String(url),
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
	})
	created, err := admin.CreateKey(t.Context(), &kms.CreateKeyInput{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.CreateAlias(t.Context(), &kms.CreateAliasInput{
		AliasName:   aws.String("alias/infraweave"),
		TargetKeyId: created.KeyMetadata.KeyId,
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{*created.KeyMetadata.Arn, "alias/infraweave"} {
		c.KeyID = id
		p, err := awskms.New(t.Context(), c, endpoint(url))
		if err != nil {
			t.Fatal(err)
		}
		kektest.Contract(t, p, "awskms:"+id)
	}
}

// kmsError models KMS answering every call with the error typ.
func kmsError(t *testing.T, typ string) string {
	t.Helper()
	r := chi.NewRouter()
	r.Post("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"__type":"` + typ + `","message":"refused"}`))
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestUnitAWSKMSErrors checks how Decrypt's refusals read: a blob another
// key wrapped is ErrUnwrap, as KMS answers it with IncorrectKeyException,
// and a refused permission is not, since the KEK may be right and the
// operator must fix the key policy instead.
func TestUnitAWSKMSErrors(t *testing.T) {
	environment(t)
	for typ, wrongKEK := range map[string]bool{
		"IncorrectKeyException":      true,
		"InvalidCiphertextException": true,
		"AccessDeniedException":      false,
	} {
		p, err := awskms.New(
			t.Context(),
			kek.AWSKMSConfig{KeyID: "alias/infraweave", Region: "us-east-1"},
			endpoint(kmsError(t, typ)),
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Unwrap(t.Context(), 1, []byte("blob"))
		if errors.Is(err, kek.ErrUnwrap) != wrongKEK || !strings.Contains(err.Error(), typ) ||
			!strings.Contains(err.Error(), "awskms:alias/infraweave") {
			t.Errorf("%s: %v", typ, err)
		}
	}
}

// TestUnitAWSKMSUnreachable checks that a KMS the provider cannot reach
// fails with the provider and key named, not as a wrong KEK.
func TestUnitAWSKMSUnreachable(t *testing.T) {
	environment(t)
	p, err := awskms.New(
		t.Context(),
		kek.AWSKMSConfig{KeyID: "alias/infraweave", Region: "us-east-1"},
		endpoint("http://127.0.0.1:1"),
		func(o *kms.Options) { o.RetryMaxAttempts = 1 },
	)
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"wrap": func() error { _, err := p.Wrap(t.Context(), 1, kektest.Key()); return err },
		"unwrap": func() error {
			_, err := p.Unwrap(t.Context(), 1, []byte("blob"))
			return err
		},
	} {
		if err := call(); err == nil || errors.Is(err, kek.ErrUnwrap) ||
			!strings.Contains(err.Error(), "awskms:alias/infraweave") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := awskms.New(t.Context(), kek.AWSKMSConfig{}); !errors.Is(err, kek.ErrConfig) {
		t.Errorf("no key: %v", err)
	}
}
