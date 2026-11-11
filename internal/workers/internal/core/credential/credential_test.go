package credential_test

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/credential"
)

var now = time.Unix(1_700_000_000, 0).UTC()

// request is a certificate signing request's DER for key.
func request(t *testing.T, key crypto.Signer) []byte {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: "pool"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func p256(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// spaced is a request's DER whose last byte is one bytes.TrimSpace
// drops; about one request in forty is.
func spaced(t *testing.T) []byte {
	t.Helper()
	k := p256(t)
	for {
		if der := request(t, k); len(bytes.TrimSpace(der)) != len(der) {
			return der
		}
	}
}

func pemOf(typ string, der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}))
}

// TestUnitParseCSR accepts a request for a P-256 key as PEM, or base64 of
// its PEM or DER, and refuses any other key, PEM block or text.
//
// ref: decision 0008 § "The worker API"
func TestUnitParseCSR(t *testing.T) {
	good := request(t, p256(t))
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString
	tampered := append([]byte{}, good...)
	tampered[len(tampered)-1] ^= 0xff
	for name, tc := range map[string]struct {
		in   string
		want error
	}{
		"PEM":                        {in: pemOf("CERTIFICATE REQUEST", good)},
		"base64 of its PEM":          {in: b64([]byte(pemOf("CERTIFICATE REQUEST", good)))},
		"base64 of its DER":          {in: b64(good)},
		"DER ending in a space byte": {in: b64(spaced(t))},
		"the old PEM type":           {in: pemOf("NEW CERTIFICATE REQUEST", good)},
		"P-384":                      {in: b64([]byte(pemOf("CERTIFICATE REQUEST", request(t, p384)))), want: credential.ErrKey},
		"RSA":                        {in: b64(request(t, rsaKey)), want: credential.ErrKey},
		"a certificate":              {in: pemOf("CERTIFICATE", good), want: credential.ErrCSR},
		"not base64":                 {in: "not a request!", want: credential.ErrCSR},
		"not DER":                    {in: b64([]byte("not a request")), want: credential.ErrCSR},
		"a bad signature":            {in: b64(tampered), want: credential.ErrCSR},
	} {
		t.Run(name, func(t *testing.T) {
			csr, err := credential.ParseCSR(tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ParseCSR = %v, want %v", err, tc.want)
			}
			if tc.want == nil && csr == nil {
				t.Fatal("no request")
			}
		})
	}
}

// TestUnitAuthority signs a pool's certificate that chains to the CA,
// names the pool and carries the serial; the CA survives Marshal and
// ParseCA, and a certificate of another pool or CA is refused.
func TestUnitAuthority(t *testing.T) {
	ca, err := credential.NewCA(now)
	if err != nil {
		t.Fatal(err)
	}
	certDER, keyDER, err := ca.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	ca, err = credential.ParseCA(certDER, keyDER)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := credential.ParseCSR(base64.StdEncoding.EncodeToString(request(t, p256(t))))
	if err != nil {
		t.Fatal(err)
	}
	pool := kernel.NewULID()
	cert, err := ca.Sign(csr, pool, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	if cert.SerialNumber.Int64() != 7 || !cert.NotAfter.Equal(ca.Certificate.NotAfter) {
		t.Errorf("serial %s, until %s", cert.SerialNumber, cert.NotAfter)
	}
	if err := ca.Verify(cert, pool, now.Add(time.Hour)); err != nil {
		t.Errorf("Verify = %v", err)
	}
	if err := ca.Verify(cert, kernel.NewULID(), now); !errors.Is(err, credential.ErrPool) {
		t.Error("a certificate verified for another pool")
	}
	other, err := credential.NewCA(now)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Verify(cert, pool, now); err == nil {
		t.Error("a certificate verified under another CA")
	}

	_, otherKey, err := other.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := x509.MarshalPKCS8PrivateKey(p256(t))
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaDER, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		cert, key []byte
		want      error
	}{
		"not a certificate": {cert: []byte("x"), key: keyDER},
		"not a key":         {cert: certDER, key: []byte("x")},
		"another CA's key":  {cert: certDER, key: otherKey, want: credential.ErrCA},
		"another P-256 key": {cert: certDER, key: ecKey, want: credential.ErrCA},
		"an RSA key":        {cert: certDER, key: rsaDER, want: credential.ErrCA},
	} {
		_, err := credential.ParseCA(tc.cert, tc.key)
		if err == nil || tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("ParseCA of %s = %v", name, err)
		}
	}
	if _, err := ca.Sign(
		&x509.CertificateRequest{PublicKey: "not a key"},
		pool,
		8,
		now,
	); err == nil {
		t.Error("Sign of a request with no key succeeded")
	}
}

// TestUnitToken round-trips a pool token and refuses one that does not
// decode.
func TestUnitToken(t *testing.T) {
	ca, err := credential.NewCA(now)
	if err != nil {
		t.Fatal(err)
	}
	pool := kernel.NewULID()
	tok := credential.NewToken("https://infraweave.example", pool, ca.Certificate.Raw)
	got, cert, err := credential.DecodeToken(tok.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if got != tok || got.Version != credential.TokenVersion || !cert.Equal(ca.Certificate) {
		t.Errorf("DecodeToken = %+v", got)
	}
	b64 := base64.StdEncoding.EncodeToString
	for name, in := range map[string]string{
		"not base64":        "!",
		"not JSON":          b64([]byte("x")),
		"no certificate":    b64([]byte(`{"version":1}`)),
		"a bad certificate": b64([]byte(`{"certificate":` + quote(pemOf("CERTIFICATE", []byte("x"))) + `}`)),
		"another PEM block": b64([]byte(`{"certificate":` + quote(pemOf("KEY", ca.Certificate.Raw)) + `}`)),
	} {
		if _, _, err := credential.DecodeToken(in); !errors.Is(err, credential.ErrToken) {
			t.Errorf("DecodeToken of %s = %v", name, err)
		}
	}
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, "\n", `\n`) + `"` }
