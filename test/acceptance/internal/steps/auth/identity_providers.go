package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// providerSteps drive the Organization tag's identity providers as the
// browser of the home organization's member, and read what the database
// holds of them. A provider the scenario names is {name} in a path or
// body once its creation's answer names its ID.
type providerSteps struct{ w *harness.World }

func (p *providerSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the answer is the identity provider "(\w+)"$`, p.remember)
	sc.Step(`^the answer lists the identity providers "([^"]*)"$`, p.lists)
	sc.Step(`^the answer's settings hold "([^"]+)" as '([^']*)'$`, p.setting)
	sc.Step(
		`^the browser's session signed in through the identity provider "(\w+)" as "([^"]+)"$`,
		p.signedIn,
	)
	sc.Step(`^the identity provider "(\w+)" holds (\d+) identit(?:y|ies)$`, p.identities)
	sc.Step(`^the browser's session is refused through ([AB]) within a second$`, p.refused)
	sc.Step(`^the identity provider "(\w+)"'s secret is sealed, never "([^"]+)"$`, p.sealed)
	sc.Step(`^no answer of the scenario names "([^"]+)"$`, p.namesNot)
	sc.Step(`^the home organization's trail holds (\d+) "([a-z_.]+)" entr(?:y|ies)$`, p.trail)
	sc.Step(
		`^the browser's "([A-Z]+) ([^"]+)" with '([^']*)' through ([AB]) eventually answers a (\d+) problem "([a-z-]+)"$`,
		p.eventually,
	)
}

// remember keeps the ID of the last answer, a provider, as name.
func (p *providerSteps) remember(name string) error {
	var v struct {
		ID string `json:"id"`
	}
	r := p.w.Last()
	if r.Status/100 != 2 || json.Unmarshal(r.Body, &v) != nil || v.ID == "" {
		return fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	p.w.IDs[name] = v.ID
	return nil
}

// lists checks the last answer, the providers, against want: each
// name:KIND:default or name:KIND:-, then :enabled or :disabled,
// comma-separated, oldest first. No provider answers a secret.
func (p *providerSteps) lists(want string) error {
	var list struct {
		Items []map[string]any `json:"items"`
	}
	r := p.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &list) != nil {
		return fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	got := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		if _, ok := item["secret"]; ok {
			return fmt.Errorf("%w: a provider answers its secret: %s", harness.ErrStep, r.Body)
		}
		def, state := "-", "disabled"
		if item["isDefault"] == true {
			def = "default"
		}
		if item["enabled"] == true {
			state = "enabled"
		}
		got = append(got, fmt.Sprintf("%v:%v:%s:%s", item["name"], item["kind"], def, state))
	}
	if strings.Join(got, ", ") != want {
		return fmt.Errorf("%w: the providers are %q, want %q", harness.ErrStep, strings.Join(got, ", "), want)
	}
	return nil
}

// setting checks the last answer's settings member name against want,
// JSON.
func (p *providerSteps) setting(name, want string) error {
	var v struct {
		Settings map[string]json.RawMessage `json:"settings"`
	}
	r := p.w.Last()
	if json.Unmarshal(r.Body, &v) != nil {
		return fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	var got, exp any
	if json.Unmarshal(v.Settings[name], &got) != nil || json.Unmarshal([]byte(want), &exp) != nil ||
		fmt.Sprint(got) != fmt.Sprint(exp) {
		return fmt.Errorf("%w: settings.%s is %s, want %s", harness.ErrStep, name, v.Settings[name], want)
	}
	return nil
}

// signedIn makes the browser's session, the home account's member's, one
// signed in through the provider as subject, with the member's identity at
// the provider, as a sign-in through it writes them.
func (p *providerSteps) signedIn(ctx context.Context, name, subject string) error {
	_, err := p.w.Query(ctx, fmt.Sprintf(`
		INSERT INTO member_identities (organization_id, member_id, identity_provider_id, subject)
			VALUES ('%[1]s', '%[2]s', '%[3]s', %[4]s);
		UPDATE sessions SET identity_provider_id = '%[3]s', idp_subject = %[4]s
			WHERE id = '%[5]s'`,
		harness.UUIDOf(harness.Home.Org), harness.UUIDOf(harness.MemberID),
		harness.UUIDOf(p.w.IDs[name]), harness.Literal(subject),
		harness.UUIDOf(harness.MemberSession)))
	return err
}

// refused waits for the replica to refuse the browser's session, which a
// revocation on either replica ends.
func (p *providerSteps) refused(ctx context.Context, replica string) error {
	ctx, cancel := context.WithTimeout(ctx, revocationWithin)
	defer cancel()
	return deps.Eventually(ctx, "the browser's session to fail at "+replica,
		func(ctx context.Context) (bool, error) {
			r, err := p.w.BrowserDo(ctx, replica, http.MethodGet, "/api/v1/viewer", "", nil)
			if err != nil && r.Status == 0 {
				return false, err
			}
			return harness.IsProblem(r, "viewer", "401", "unauthenticated") == nil, nil
		})
}

// identities checks how many identities sign in through the provider.
func (p *providerSteps) identities(ctx context.Context, name string, want int) error {
	n, err := p.w.Number(ctx, fmt.Sprintf(
		`SELECT count(*) FROM member_identities WHERE identity_provider_id = '%s'`,
		harness.UUIDOf(p.w.IDs[name])))
	if err != nil {
		return err
	}
	if n != want {
		return fmt.Errorf("%w: %d identities, want %d", harness.ErrStep, n, want)
	}
	return nil
}

// sealed checks that the provider's secret column holds something, and not
// the plain secret.
func (p *providerSteps) sealed(ctx context.Context, name, plain string) error {
	var sealed []byte
	if err := p.w.Row(ctx, `SELECT secret_sealed FROM identity_providers WHERE id = $1`,
		[]any{harness.UUIDOf(p.w.IDs[name])}, &sealed); err != nil {
		return err //nolint:wrapcheck // names the query
	}
	if len(sealed) == 0 || bytes.Contains(sealed, []byte(plain)) {
		return fmt.Errorf("%w: secret_sealed is %q", harness.ErrStep, sealed)
	}
	return nil
}

// namesNot checks that no answer of the scenario holds text.
func (p *providerSteps) namesNot(text string) error {
	for _, r := range p.w.Sent {
		if bytes.Contains(r.Body, []byte(text)) {
			return fmt.Errorf("%w: an answer names %q: %s", harness.ErrStep, text, r.Body)
		}
	}
	return nil
}

// trail checks how many entries of action the home organization's trail
// holds.
func (p *providerSteps) trail(ctx context.Context, want int, action string) error {
	n, err := p.w.Number(ctx, fmt.Sprintf(`SELECT count(*) FROM organization_audit_trail_entries
		WHERE organization_id = '%s' AND action = %s`,
		harness.UUIDOf(harness.Home.Org), harness.Literal(action)))
	if err != nil {
		return err
	}
	if n != want {
		return fmt.Errorf("%w: %d %q entries, want %d", harness.ErrStep, n, action, want)
	}
	return nil
}

// eventually sends the request in the browser until it answers the
// problem, as a replica applies an entitlement change on its notification.
func (p *providerSteps) eventually(ctx context.Context, method, path, raw, replica, code, slug string) error {
	path, raw = p.w.Resolve(path), p.w.Resolve(raw)
	return deps.Eventually(ctx, method+" "+path+" through "+replica+" to answer "+code,
		func(ctx context.Context) (bool, error) {
			r, err := p.w.BrowserDo(ctx, replica, method, path, harness.PublicURL, harness.Body(raw))
			if err != nil && r.Status == 0 {
				return false, err
			}
			return harness.IsProblem(r, method+" "+path, code, slug) == nil, nil
		})
}
