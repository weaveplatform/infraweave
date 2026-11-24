package federation

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"

	jose "github.com/go-jose/go-jose/v4"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fixtureKeys are testdata/keys.json's public keys, out of kid order: two
// RS256 keys and an ES256 one.
func fixtureKeys(t *testing.T) []jose.JSONWebKey {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	var set jose.JSONWebKeySet
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	return set.Keys
}

// golden compares v, as indented JSON, with testdata/name, which -update
// rewrites.
func golden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s:\n%s\nwant\n%s", name, got, want)
	}
}

// TestUnitFederationDiscoveryDocument checks the discovery document of
// the default issuer, the public URL, and of a custom issuer with a path
// and a custom jwks_uri, against golden JSON.
func TestUnitFederationDiscoveryDocument(t *testing.T) {
	keys := fixtureKeys(t)
	golden(t, "discovery_default.json", NewDocument("https://iw.example.com", "", keys[:2]))
	golden(t, "discovery_custom.json",
		NewDocument("https://id.example.com/federation", "https://keys.example.net/iw/jwks.json", keys))
	if d := NewDocument("https://iw.example.com/federation/", "", nil); d.JWKSURI !=
		"https://iw.example.com/federation/.well-known/jwks" ||
		d.IDTokenSigningAlgValuesSupported == nil || len(d.IDTokenSigningAlgValuesSupported) != 0 {
		t.Errorf("an issuer with a trailing slash: %+v", d)
	}
	d := NewDocument("https://iw.example.com", "", keys)
	d.ClaimsSupported[0] = "changed"
	if Claims[0] != "iss" {
		t.Error("a document shares Claims")
	}
}

// TestUnitFederationKeySet checks the JWKS against golden JSON: sorted by
// kid, use sig on each, and only public members, even for a key given
// with its private half, a certificate or another use.
func TestUnitFederationKeySet(t *testing.T) {
	keys := fixtureKeys(t)
	set, err := KeySet(keys)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "jwks.json", set)

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	set, err = KeySet([]jose.JSONWebKey{{
		Key: priv, KeyID: "k", Algorithm: "ES256", Use: "enc",
		Certificates: []*x509.Certificate{{Raw: []byte("certificate")}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	var members struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(raw, &members); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"d", "x5c", "x5t", "x5t#S256", "x5u"} {
		if _, ok := members.Keys[0][m]; ok {
			t.Errorf("the key set carries %q: %s", m, raw)
		}
	}
	if members.Keys[0]["use"] != "sig" || members.Keys[0]["kid"] != "k" || members.Keys[0]["alg"] != "ES256" {
		t.Errorf("the key set's key: %s", raw)
	}

	if _, err := KeySet([]jose.JSONWebKey{{KeyID: "none"}}); !errors.Is(err, ErrUnpublishable) {
		t.Errorf("a key with no public half: %v", err)
	}
}

// TestUnitFederationIssuerPath checks the path the discovery routes are
// served under.
func TestUnitFederationIssuerPath(t *testing.T) {
	for issuer, want := range map[string]string{
		"https://iw.example.com":                 "",
		"https://iw.example.com/":                "",
		"https://iw.example.com/federation":      "/federation",
		"https://iw.example.com/federation/":     "/federation",
		"https://iw.example.com/a/b":             "/a/b",
		"://not a url":                           "",
		"http://infraweave.test/iw-federation/x": "/iw-federation/x",
	} {
		if got := IssuerPath(issuer); got != want {
			t.Errorf("IssuerPath(%q) = %q, want %q", issuer, got, want)
		}
	}
	if got := JWKSURI("https://iw.example.com", "https://keys.example.net/jwks?v=1"); got !=
		"https://keys.example.net/jwks?v=1" {
		t.Errorf("a custom jwks_uri is %q", got)
	}
}
