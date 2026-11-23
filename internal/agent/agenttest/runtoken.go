package agenttest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// Issuer is the iss of every run token RunToken signs.
const Issuer = Endpoint

// runTokenKey is the one key every fake server signs run tokens with and
// answers as its run token keys, so a token signed before a server
// starts verifies against it.
var runTokenKey = func() *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(fmt.Sprintf("agenttest: run token key: %v", err))
	}
	return k
}()

// runTokenKID is runTokenKey's kid.
const runTokenKID = "agenttest-run-token-key"

// RunToken is a run token of run, as the server issues one: ES256 under
// the fake servers' key, iss Issuer, aud infraweave-runner, iw_kind
// runner, valid for a day from now on the caller's clock.
func RunToken(run string) string {
	js, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: runTokenKey},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader(jose.HeaderKey("kid"), runTokenKID))
	if err != nil {
		panic(fmt.Sprintf("agenttest: run token signer: %v", err))
	}
	now := time.Now()
	raw, err := jwt.Signed(js).Claims(map[string]any{
		"iss": Issuer, "aud": "infraweave-runner", "sub": run, "iw_kind": "runner",
		"jti": run + "-" + rand.Text(), "iat": now.Unix(), "nbf": now.Unix(),
		"exp": now.Add(24 * time.Hour).Unix(),
	}).Serialize()
	if err != nil {
		panic(fmt.Sprintf("agenttest: run token: %v", err))
	}
	return raw
}

// RunTokenKeys are the fake servers' run token keys, as the worker API
// answers them.
func RunTokenKeys() wire.RunTokenKeys {
	raw, err := json.Marshal(jose.JSONWebKey{
		Key: &runTokenKey.PublicKey, KeyID: runTokenKID, Algorithm: "ES256", Use: "sig",
	})
	if err != nil {
		panic(fmt.Sprintf("agenttest: run token key: %v", err))
	}
	var k wire.RunTokenKey
	if err := json.Unmarshal(raw, &k); err != nil {
		panic(fmt.Sprintf("agenttest: run token key: %v", err))
	}
	return wire.RunTokenKeys{Issuer: Issuer, Audience: "infraweave-runner", Keys: []wire.RunTokenKey{k}}
}

// runTokenKeys answers the run token keys to a worker's session.
func (s *Server) runTokenKeys(w http.ResponseWriter, r *http.Request) {
	if st := s.refused("run-token-keys"); st != 0 {
		problem(w, st, "the run token keys are refused")
		return
	}
	if _, _, ok := s.worker(r); !ok {
		problem(w, http.StatusUnauthorized, "the session is refused")
		return
	}
	answer(w, RunTokenKeys())
}
