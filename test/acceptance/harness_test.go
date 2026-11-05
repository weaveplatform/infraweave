package acceptance

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// harnessSteps prove the harness itself: each dependency tag starts its
// dependency, and nested runs of the suite over fixture features show the
// Docker rules and the evidence of a failure.
type harnessSteps struct {
	w *world

	db     *Database
	store  *ObjectStore
	issuer *Issuer

	fixtures string
	nested   struct {
		out    string
		err    error
		output string
		dir    string
	}
}

func (h *harnessSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the scenario's PostgreSQL database$`, h.postgres)
	sc.Step(`^it answers "([^"]+)" with "([^"]*)"$`, h.query)
	sc.Step(`^the shared object store$`, h.objectStore)
	sc.Step(`^its health endpoint answers 200$`, h.storeHealthy)
	sc.Step(`^the shared OIDC issuer$`, h.oidc)
	sc.Step(`^a token it mints for subject "([^"]+)" verifies against its JWKS$`, h.verifies)
	sc.Step(`^the fixture features in "([^"]+)"$`, h.fixtureFeatures)
	sc.Step(`^the suite runs them nested( without Docker)?( requiring it)?$`, h.runNested)
	sc.Step(`^the nested run (passes|fails)$`, h.nestedResult)
	sc.Step(`^its output says "([^"]+)"$`, h.outputSays)
	sc.Step(`^it left "([^"]+)" in the evidence of "([^"]+)"$`, h.leftEvidence)
	sc.Step(`^the scenario fails on purpose$`, h.failOnPurpose)
}

func (h *harnessSteps) postgres(ctx context.Context) (err error) {
	h.db, err = h.w.database(ctx)
	return err
}

func (h *harnessSteps) query(ctx context.Context, sql, want string) error {
	got, err := h.db.Query(ctx, sql)
	if err != nil {
		return err
	}
	if strings.TrimSpace(got) != want {
		return fmt.Errorf("%w: %s answered %q, want %q", errStep, sql, got, want)
	}
	return nil
}

func (h *harnessSteps) objectStore(ctx context.Context) (err error) {
	h.store, err = h.w.s.deps.ObjectStore(ctx)
	return err
}

func (h *harnessSteps) storeHealthy(ctx context.Context) error {
	return Eventually(ctx, "the object store's health endpoint", h.store.Healthy)
}

func (h *harnessSteps) oidc() (err error) {
	h.issuer, err = h.w.s.deps.OIDC()
	return err
}

// verifies mints a token and checks it as a relying party would: the
// discovery document names the JWKS, whose key verifies the signature.
func (h *harnessSteps) verifies(ctx context.Context, subject string) error {
	token, err := h.issuer.Mint(map[string]any{"sub": subject, "aud": "infraweave"})
	if err != nil {
		return err
	}
	var disc struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"` //nolint:tagliatelle // fixed by OpenID Connect Discovery
	}
	if err := getJSON(ctx, h.issuer.URL()+"/.well-known/openid-configuration", &disc); err != nil {
		return err
	}
	var jwks struct {
		Keys []struct{ Kid, N, E string } `json:"keys"`
	}
	if err := getJSON(ctx, disc.JWKSURI, &jwks); err != nil {
		return err
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(jwks.Keys) != 1 || disc.Issuer != h.issuer.URL() {
		return fmt.Errorf(
			"%w: token %d parts, %d keys, issuer %q",
			errStep,
			len(parts),
			len(jwks.Keys),
			disc.Issuer,
		)
	}
	n, err := base64.RawURLEncoding.DecodeString(jwks.Keys[0].N)
	if err != nil {
		return fmt.Errorf("%w: jwks n: %w", errStep, err)
	}
	e, err := base64.RawURLEncoding.DecodeString(jwks.Keys[0].E)
	if err != nil {
		return fmt.Errorf("%w: jwks e: %w", errStep, err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("%w: signature: %w", errStep, err)
	}
	pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return fmt.Errorf("%w: the JWKS key does not verify the token: %w", errStep, err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("%w: payload: %w", errStep, err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil || claims["sub"] != subject {
		return fmt.Errorf("%w: claims %s", errStep, payload)
	}
	return nil
}

func getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	return nil
}

func (h *harnessSteps) fixtureFeatures(dir string) error {
	abs, err := filepath.Abs(filepath.Join("testdata", "harness", dir))
	if err != nil {
		return fmt.Errorf("fixtures: %w", err)
	}
	h.fixtures = abs
	return nil
}

// runNested runs this test binary's TestAccFeatures over the fixtures,
// with Docker out of reach when without is set. The nested run shares the
// built binary and writes its evidence and coverage into the scenario's own
// directory; with Docker it starts dependencies of its own.
func (h *harnessSteps) runNested(ctx context.Context, without, requiring string) error {
	bin, err := binary(ctx)
	if err != nil {
		return err
	}
	h.nested.dir = h.w.dir
	h.nested.out = filepath.Join(h.w.dir, "out")
	args := []string{"-test.run", "^TestAccFeatures$"}
	// A test binary built without -cover rejects -test.gocoverdir.
	if testing.CoverMode() != "" {
		cover := filepath.Join(h.w.dir, "cover")
		if err := os.MkdirAll(cover, 0o750); err != nil {
			return fmt.Errorf("nested cover dir: %w", err)
		}
		args = append(args, "-test.gocoverdir", cover)
	}
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append(os.Environ(),
		envFeatures+"="+h.fixtures,
		envOut+"="+h.nested.out,
		envBinary+"="+bin,
		envCucumber+"=",
		envPartition+"=",
	)
	if without != "" {
		cmd.Env = append(cmd.Env, envNoDocker+"=1")
	} else {
		cmd.Env = append(cmd.Env, envNoDocker+"=")
	}
	if requiring != "" {
		cmd.Env = append(cmd.Env, envRequireDocker+"=1")
	} else {
		cmd.Env = append(cmd.Env, envRequireDocker+"=")
	}
	out, err := cmd.CombinedOutput()
	h.nested.output, h.nested.err = string(out), err
	return nil
}

func (h *harnessSteps) nestedResult(want string) error {
	var exit *exec.ExitError
	switch {
	case want == "passes" && h.nested.err != nil:
		return fmt.Errorf(
			"%w: the nested run failed: %w\n%s",
			errStep,
			h.nested.err,
			h.nested.output,
		)
	case want == "fails" && !errors.As(h.nested.err, &exit):
		return fmt.Errorf(
			"%w: the nested run did not fail (%w)\n%s",
			errStep,
			h.nested.err,
			h.nested.output,
		)
	}
	return nil
}

func (h *harnessSteps) outputSays(text string) error {
	if !strings.Contains(h.nested.output, text) {
		return fmt.Errorf("%w: the nested output lacks %q:\n%s", errStep, text, h.nested.output)
	}
	return nil
}

func (h *harnessSteps) leftEvidence(file, scenario string) error {
	p := filepath.Join(h.nested.out, slug(scenario), file)
	if info, err := os.Stat(p); err != nil || info.Size() == 0 {
		return fmt.Errorf("%w: no evidence %s (%w):\n%s", errStep, p, err, h.nested.output)
	}
	return nil
}

func (h *harnessSteps) failOnPurpose() error {
	return fmt.Errorf("%w: this fixture scenario fails on purpose", errStep)
}
