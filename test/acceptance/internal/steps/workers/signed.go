package workers

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cucumber/godog"
	jose "github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// signedSteps tamper with the run tokens the scenario's relay delivers to
// its launchers and read the tokens it delivered. Each forged token is a
// deliberate alteration of the server's genuine token, or a token the
// step signs with a key it made for the purpose: none is a reference
// capture. The relay passes each later call carrying a forged token on
// with the server's, so the server stays honest and only the launcher's
// own verification refuses the forgery.
//
// ref: decision 0006 § "Token kinds"
// ref: decision 0016
func (l *launcherSteps) signed(sc *godog.ScenarioContext) {
	sc.Step(`^the relay delivers the next run token (with its signature altered|with its run claim altered|signed by an unrelated installation's key)$`,
		l.forge)
	sc.Step(`^the relay delivers the token kept as "(\w+)" in place of the next run token$`, l.substitute)
	sc.Step(`^the relay delivered the run "(\w+)" the server's own run token, its signing key kept as "([\w.]+)"$`,
		func(run, key string) error {
			kid, err := l.genuine(run)
			l.w.IDs[key] = kid
			return err
		})
	sc.Step(`^the relay delivered the run "(\w+)" the server's own run token, signed by a key other than "([\w.]+)"$`,
		func(run, key string) error {
			kid, err := l.genuine(run)
			if err == nil && kid == l.w.IDs[key] {
				err = fmt.Errorf("%w: the run %s's token is signed by %s", harness.ErrStep, run, key)
			}
			return err
		})
	sc.Step(`^the relay passed no "([A-Z]+) ([^"]+)"$`, l.notPassed)
	sc.Step(`^the relay passed "([A-Z]+) ([^"]+)" answered (\d+) (\d+) times$`, l.passedTimes)
	sc.Step(`^the launcher "(\w+)" logged no JWT$`, l.noJWT)
	sc.Step(`^the answer names the run tokens' issuer and audience and the public key kept as "([\w.]+)", with no private member$`,
		l.keysAnswer)
	sc.Step(`^the api-token signing keys rotate until a token exchanged through ([AB]) is signed by a key other than "([\w.]+)"$`,
		l.rotate)
}

// notPassed checks that the relay passed no call of method to path.
func (l *launcherSteps) notPassed(method, path string) error {
	path = l.w.Resolve(path)
	for _, c := range l.relay.Calls() {
		if c.Method == method && c.Path == path {
			return fmt.Errorf("%w: the relay passed %s %s answered %d", harness.ErrStep, method, path, c.Status)
		}
	}
	return nil
}

// passedTimes checks that the relay passed n calls of method to path
// that answered status.
func (l *launcherSteps) passedTimes(method, path string, status, n int) error {
	path = l.w.Resolve(path)
	got := 0
	for _, c := range l.relay.Calls() {
		if c == (harness.Call{Method: method, Path: path, Status: status}) {
			got++
		}
	}
	if got != n {
		return fmt.Errorf("%w: the relay passed %s %s answered %d %d times, want %d",
			harness.ErrStep, method, path, status, got, n)
	}
	return nil
}

// noJWT checks that no line the launcher name wrote holds a JWT.
func (l *launcherSteps) noJWT(name string) error {
	launcher := l.launchers[name]
	if launcher == nil || launcher.srv == nil {
		return fmt.Errorf("%w: no launcher %q", harness.ErrStep, name)
	}
	if strings.Contains(string(launcher.srv.Stdout()), "eyJ") || strings.Contains(string(launcher.srv.Stderr()), "eyJ") {
		return fmt.Errorf("%w: the launcher %s logged JWT material", harness.ErrStep, name)
	}
	return nil
}

// keysAnswer checks the last answer of the run token keys: the issuer,
// the runner audience, and among its keys the public ES256 key whose kid
// is kept as key, with no private member in any key.
func (l *launcherSteps) keysAnswer(key string) error {
	var answer struct {
		Issuer   string           `json:"issuer"`
		Audience string           `json:"audience"`
		Keys     []map[string]any `json:"keys"`
	}
	body := l.w.Last().Body
	if err := json.Unmarshal(body, &answer); err != nil {
		return fmt.Errorf("%w: the run token keys: %w: %s", harness.ErrStep, err, body)
	}
	found := false
	for _, k := range answer.Keys {
		for _, private := range []string{"d", "p", "q", "dp", "dq", "qi", "k"} {
			if _, ok := k[private]; ok {
				return fmt.Errorf("%w: a run token key holds the private member %q", harness.ErrStep, private)
			}
		}
		found = found || k["kid"] == l.w.IDs[key] && k["kty"] == "EC" && k["crv"] == "P-256" && k["alg"] == "ES256"
	}
	if answer.Issuer == "" || answer.Audience != "infraweave-runner" || !found {
		return fmt.Errorf("%w: the run token keys name no public key %s: %s", harness.ErrStep, key, body)
	}
	return nil
}

// rotate moves the api-token signing keys through a real rotation by
// authn.keys.rotate: it ages the active key to its successor's creation
// and runs the task, ages the successor past its publication and runs it
// again, then waits until replica signs a freshly exchanged token with a
// key other than the one kept as key, as it does once its signer reads
// the promotion.
func (l *launcherSteps) rotate(ctx context.Context, replica, key string) error {
	w := l.w
	due := `UPDATE periodic_tasks SET next_run_at = now() WHERE name = 'authn.keys.rotate'`
	if _, err := w.Query(ctx, `UPDATE signing_keys SET created_at = created_at - interval '90 days',
		activated_at = activated_at - interval '90 days' WHERE purpose = 'api-token' AND state = 'active';`+due); err != nil {
		return err
	}
	if err := l.keysIn(ctx, "a successor key to be published", `state = 'next'`); err != nil {
		return err
	}
	if _, err := w.Query(ctx, `UPDATE signing_keys SET created_at = created_at - interval '48 hours'
		WHERE purpose = 'api-token' AND state = 'next';`+due); err != nil {
		return err
	}
	if err := l.keysIn(ctx, "the successor key to be promoted", `state = 'retiring'`); err != nil {
		return err
	}
	c, err := w.Client(replica, "")
	if err != nil {
		return err
	}
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, "a token signed by the promoted key", func(ctx context.Context) (bool, error) {
		raw, err := harness.Home.Exchange(ctx, c)
		if err != nil {
			return false, err
		}
		header, ok := payload(strings.Split(raw, ".")[0])
		return ok && header["kid"] != nil && header["kid"] != w.IDs[key], nil
	})
}

// keysIn waits until one api-token key matches where.
func (l *launcherSteps) keysIn(ctx context.Context, what, where string) error {
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, what, func(ctx context.Context) (bool, error) {
		n, err := l.w.Number(ctx, `SELECT count(*) FROM signing_keys WHERE purpose = 'api-token' AND `+where)
		return n == 1, err
	})
}

// forge makes the relay deliver the next run token altered as how says.
func (l *launcherSteps) forge(how string) error {
	if l.relay == nil {
		return fmt.Errorf("%w: no relay: start a launcher first", harness.ErrStep)
	}
	var (
		other *ecdsa.PrivateKey
		err   error
	)
	if strings.HasPrefix(how, "signed by") {
		if other, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
			return fmt.Errorf("an unrelated installation's key: %w", err)
		}
	}
	l.relay.Forge(func(real string) string {
		parts := strings.Split(real, ".")
		if len(parts) != 3 {
			return real + "."
		}
		switch how {
		case "with its signature altered":
			sig, err := base64.RawURLEncoding.DecodeString(parts[2])
			if err != nil || len(sig) == 0 {
				return real + "."
			}
			sig[len(sig)/2] ^= 0x01
			return parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(sig)
		case "with its run claim altered":
			claims, ok := payload(parts[1])
			if !ok {
				return real + "."
			}
			claims["sub"] = kernel.NewULID().String()
			encoded, _ := json.Marshal(claims)
			return parts[0] + "." + base64.RawURLEncoding.EncodeToString(encoded) + "." + parts[2]
		}
		claims, ok := payload(parts[1])
		if !ok {
			return real + "."
		}
		return sign(other, kernel.NewULID().String(), claims)
	})
	return nil
}

// substitute makes the relay deliver the token kept as name in place of
// the next run token: another run's genuine token, or a genuine token of
// another kind.
func (l *launcherSteps) substitute(name string) error {
	if l.relay == nil {
		return fmt.Errorf("%w: no relay: start a launcher first", harness.ErrStep)
	}
	token, ok := l.w.Tokens[name]
	if !ok {
		return fmt.Errorf("%w: no token kept as %q", harness.ErrStep, name)
	}
	l.relay.Forge(func(string) string { return token })
	return nil
}

// genuine is the kid of the server's own run token the relay delivered
// for run.
func (l *launcherSteps) genuine(run string) (string, error) {
	id := l.w.IDs[run]
	for _, d := range l.relay.Deliveries() {
		if d.Run != id {
			continue
		}
		if d.Forged {
			return "", fmt.Errorf("%w: the relay forged the run %s's token", harness.ErrStep, run)
		}
		header, ok := payload(strings.Split(d.Token, ".")[0])
		kid, _ := header["kid"].(string)
		if !ok || kid == "" {
			return "", fmt.Errorf("%w: the run %s's token names no key", harness.ErrStep, run)
		}
		return kid, nil
	}
	return "", fmt.Errorf("%w: the relay delivered the run %s no token", harness.ErrStep, run)
}

// payload is a JWS segment decoded as a JSON object.
func payload(segment string) (map[string]any, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	var out map[string]any
	return out, err == nil && json.Unmarshal(raw, &out) == nil
}

// sign is claims signed with key under kid, as an installation the server
// does not know would sign them; a failure answers no JWT at all.
func sign(key *ecdsa.PrivateKey, kid string, claims map[string]any) string {
	js, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader(jose.HeaderKey("kid"), kid))
	if err != nil {
		return "unsigned"
	}
	encoded, _ := json.Marshal(claims)
	obj, err := js.Sign(encoded)
	if err != nil {
		return "unsigned"
	}
	raw, err := obj.CompactSerialize()
	if err != nil {
		return "unsigned"
	}
	return raw
}
