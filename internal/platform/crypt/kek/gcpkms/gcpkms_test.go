package gcpkms_test

import (
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"google.golang.org/api/option"
	htransport "google.golang.org/api/transport/http"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/gcpkms"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/kektest"
)

const (
	// KeyEnv names the CryptoKey the live test wraps with, which
	// test/cloud/kek/google creates.
	KeyEnv = "INFRAWEAVE_TEST_GCPKMS_KEY"
	// RecordEnv set to 1 makes the live test write the fixture.
	RecordEnv = "INFRAWEAVE_TEST_RECORD"
	fixture   = "testdata/gcpkms.json"
	// recorded is the key the fixture names: the lab's, with its project
	// redacted.
	recorded = "projects/lab-project/locations/global/keyRings/infraweave-ci/cryptoKeys/kek"
)

// TestUnitGCPKMS runs the contract against Cloud KMS's recorded answers.
func TestUnitGCPKMS(t *testing.T) {
	srv := kektest.Replay(t, fixture, false)
	p, err := gcpkms.New(t.Context(), kek.GCPKMSConfig{KeyName: recorded},
		option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	kektest.Contract(t, p, "gcpkms:"+recorded)
}

var project = regexp.MustCompile(`^projects/([^/]+)/`)

// TestAccGCPKMSLive runs the contract against Cloud KMS with Application
// Default Credentials, and with RecordEnv=1 records the fixture
// TestUnitGCPKMS replays.
func TestAccGCPKMSLive(t *testing.T) {
	key := os.Getenv(KeyEnv)
	if key == "" {
		t.Skipf("%s is not set: no Cloud KMS key to test against", KeyEnv)
	}
	var opts []option.ClientOption
	if os.Getenv(RecordEnv) == "1" {
		m := project.FindStringSubmatch(key)
		if m == nil {
			t.Fatalf("%s=%q is not a CryptoKey name", KeyEnv, key)
		}
		rec := kektest.Record(t, http.DefaultTransport, fixture, m[1], "lab-project")
		authed, err := htransport.NewTransport(t.Context(), rec,
			option.WithScopes("https://www.googleapis.com/auth/cloudkms"))
		if err != nil {
			t.Fatal(err)
		}
		opts = append(opts, option.WithHTTPClient(&http.Client{Transport: authed}))
	}
	p, err := gcpkms.New(t.Context(), kek.GCPKMSConfig{KeyName: key}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	kektest.Contract(t, p, "gcpkms:"+key)
}

// TestUnitGCPKMSUnreachable checks that a Cloud KMS the provider cannot
// reach fails with the provider and key named, not as a wrong KEK.
func TestUnitGCPKMSUnreachable(t *testing.T) {
	p, err := gcpkms.New(t.Context(), kek.GCPKMSConfig{KeyName: recorded},
		option.WithEndpoint("http://127.0.0.1:1"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	for name, call := range map[string]func() error{
		"wrap":   func() error { _, err := p.Wrap(t.Context(), 1, kektest.Key()); return err },
		"unwrap": func() error { _, err := p.Unwrap(t.Context(), 1, []byte("blob")); return err },
	} {
		if err := call(); err == nil || errors.Is(err, kek.ErrUnwrap) ||
			!strings.Contains(err.Error(), "gcpkms:"+recorded) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := gcpkms.New(
		t.Context(),
		kek.GCPKMSConfig{KeyName: "kek"},
	); !errors.Is(
		err,
		kek.ErrConfig,
	) {
		t.Errorf("not a CryptoKey name: %v", err)
	}
}
