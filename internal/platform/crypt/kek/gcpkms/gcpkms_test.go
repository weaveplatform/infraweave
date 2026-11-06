package gcpkms_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
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

// tampered models Cloud KMS answering encrypt and decrypt with reply.
func tampered(t *testing.T, reply string) *gcpkms.Provider {
	t.Helper()
	r := chi.NewRouter()
	r.Post("/*", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	p, err := gcpkms.New(t.Context(), kek.GCPKMSConfig{KeyName: recorded},
		option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// TestUnitGCPKMSIntegrity checks that an answer whose integrity fields do
// not confirm the request, or that another key gave, is refused.
func TestUnitGCPKMSIntegrity(t *testing.T) {
	// "AAEC" is the bytes 0, 1, 2, whose CRC32C is 2466073594.
	version := `"name":"` + recorded + `/cryptoKeyVersions/1"`
	for name, reply := range map[string]string{
		"plaintext unverified": `{` + version + `,"ciphertext":"AAEC","ciphertextCrc32c":"2466073594",
			"verifiedAdditionalAuthenticatedDataCrc32c":true}`,
		"AAD unverified": `{` + version + `,"ciphertext":"AAEC","ciphertextCrc32c":"2466073594",
			"verifiedPlaintextCrc32c":true}`,
		"ciphertext changed": `{` + version + `,"ciphertext":"AAEC","ciphertextCrc32c":"1",
			"verifiedPlaintextCrc32c":true,"verifiedAdditionalAuthenticatedDataCrc32c":true}`,
		"another key": `{"name":"projects/p/locations/global/keyRings/r/cryptoKeys/other/cryptoKeyVersions/1",
			"ciphertext":"AAEC","ciphertextCrc32c":"2466073594",
			"verifiedPlaintextCrc32c":true,"verifiedAdditionalAuthenticatedDataCrc32c":true}`,
	} {
		if _, err := tampered(
			t,
			reply,
		).Wrap(t.Context(), 1, kektest.Key()); !errors.Is(
			err,
			gcpkms.ErrIntegrity,
		) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := tampered(t, `{"plaintext":"AAEC","plaintextCrc32c":"1"}`).
		Unwrap(t.Context(), 1, []byte("blob")); !errors.Is(err, gcpkms.ErrIntegrity) {
		t.Errorf("plaintext changed: %v", err)
	}
}
