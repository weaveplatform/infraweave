package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// issuerSteps sign a browser in at a test OpenID Provider of the
// scenario's own: the in-process issuer, whose identities a step queues
// with the faults it chooses, or an issuer in the shared navikt container,
// whose sign-in form a step fills in. Until infraweave is the relying
// party, the steps play it, through deps.CodeFlow.
type issuerSteps struct {
	w *harness.World

	local *deps.Issuer
	mock  *deps.OAuth2Mock
	url   string
	// next is the identity the navikt issuer's form signs in.
	next deps.Login

	flow     *deps.CodeFlow
	callback *url.URL
	answer   deps.TokenAnswer
	header   map[string]any
	claims   map[string]any
	invalid  error
}

// issuerClient and issuerSecret are the client the steps register with
// the in-process issuer; navikt admits any.
const issuerClient, issuerSecret = "infraweave", "client-secret"

// issuerCallback is the redirect URI the steps' relying party names; the
// browser stops at it, as nothing listens there.
const issuerCallback = "https://relying-party.invalid/auth/oidc/callback"

func (s *issuerSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^an in-process OIDC issuer of the scenario's own$`, s.startLocal)
	sc.Step(`^an OIDC issuer of the scenario's own in the navikt container$`, s.startMock)
	sc.Step(`^the issuer signs in "([^"]+)" next with the claims '([^']*)'$`, s.signsIn)
	sc.Step(`^the issuer signs in "([^"]+)" next with the fault "([a-z0-9-]+)"$`, s.signsInFaulty)
	sc.Step(`^a relying party signs in through the issuer with PKCE$`, s.signIn)
	sc.Step(`^the relying party exchanges the code again$`, s.exchangeAgain)
	sc.Step(`^the ID token verifies against the issuer's JWKS$`, s.verifies)
	sc.Step(`^the ID token does not verify against the issuer's JWKS$`, s.doesNotVerify)
	sc.Step(`^the ID token's "([a-z_]+)" is '([^']*)'$`, s.claimIs)
	sc.Step(`^the ID token's nonce is the request's$`, s.nonceIsTheRequests)
	sc.Step(`^the ID token's header "([a-z]+)" is "([^"]*)"$`, s.headerIs)
	sc.Step(`^the token endpoint answers (\d+) with the error "([a-z_]+)"$`, s.tokenError)
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if s.local != nil {
			s.local.Close()
		}
		return ctx, err
	})
}

func (s *issuerSteps) startLocal() error {
	iss, err := deps.StartIssuer()
	if err != nil {
		return err //nolint:wrapcheck // names itself
	}
	iss.RegisterClient(issuerClient, issuerSecret)
	s.local, s.url = iss, iss.URL()
	return nil
}

func (s *issuerSteps) startMock(ctx context.Context) (err error) {
	s.mock, err = s.w.Suite.Deps.OAuth2Mock(ctx)
	if err != nil {
		return err //nolint:wrapcheck // names itself
	}
	s.url = s.mock.Issuer(fmt.Sprintf("scenario-%d", s.w.Suite.Deps.Serial()))
	return nil
}

func (s *issuerSteps) client() *http.Client {
	if s.local != nil {
		return s.local.Client()
	}
	return s.mock.Client()
}

func (s *issuerSteps) signsIn(subject, raw string) error {
	var claims map[string]any
	if err := json.Unmarshal([]byte(raw), &claims); err != nil {
		return fmt.Errorf("%w: claims %s: %w", harness.ErrStep, raw, err)
	}
	if s.local != nil {
		s.local.SignInNext(deps.Identity{Subject: subject, Claims: claims})
	}
	s.next = deps.Login{Subject: subject, Claims: claims}
	return nil
}

func (s *issuerSteps) signsInFaulty(subject, name string) error {
	f, ok := deps.Faults[name]
	if !ok || s.local == nil {
		return fmt.Errorf("%w: the in-process issuer has no fault %q", harness.ErrStep, name)
	}
	s.local.SignInNext(deps.Identity{Subject: subject, Faults: []deps.Fault{f}})
	return nil
}

// signIn runs the authorization code flow: the authorization request, the
// browser at the issuer, and the code's exchange.
func (s *issuerSteps) signIn(ctx context.Context) error {
	s.flow = &deps.CodeFlow{
		Issuer: s.url, ClientID: issuerClient, Secret: issuerSecret, RedirectURI: issuerCallback,
		Client: s.client(),
	}
	at, err := s.flow.AuthorizeURL(ctx)
	if err != nil {
		return err //nolint:wrapcheck // names itself
	}
	back, err := deps.SignIn(ctx, s.client(), at, s.next)
	if err != nil {
		return err //nolint:wrapcheck // names itself
	}
	if !strings.HasPrefix(back.String(), issuerCallback+"?") {
		return fmt.Errorf("%w: the issuer sent the browser to %s", harness.ErrStep, back)
	}
	s.answer, err = s.flow.Exchange(ctx, back)
	if err != nil {
		return err //nolint:wrapcheck // names itself
	}
	s.callback = back
	if s.answer.IDToken != "" {
		s.header, s.claims, s.invalid = s.flow.Verify(ctx, s.answer.IDToken)
	}
	return nil
}

func (s *issuerSteps) exchangeAgain(ctx context.Context) (err error) {
	s.answer, err = s.flow.Exchange(ctx, s.callback)
	return err //nolint:wrapcheck // names itself
}

func (s *issuerSteps) verifies() error {
	if s.answer.IDToken == "" || s.invalid != nil {
		return fmt.Errorf("%w: the token endpoint answered %+v: %w", harness.ErrStep, s.answer, s.invalid)
	}
	return nil
}

func (s *issuerSteps) doesNotVerify() error {
	if s.answer.IDToken == "" || s.invalid == nil {
		return fmt.Errorf("%w: the token endpoint answered %+v, which verifies", harness.ErrStep, s.answer)
	}
	return nil
}

func (s *issuerSteps) claimIs(name, raw string) error {
	var want any
	if err := json.Unmarshal([]byte(raw), &want); err != nil {
		return fmt.Errorf("%w: %s: %w", harness.ErrStep, raw, err)
	}
	if got := s.claims[name]; !reflect.DeepEqual(got, want) {
		return fmt.Errorf("%w: the ID token's %s is %v, want %v", harness.ErrStep, name, got, want)
	}
	return nil
}

func (s *issuerSteps) nonceIsTheRequests() error {
	if s.claims["nonce"] != s.flow.Nonce {
		return fmt.Errorf("%w: the ID token's nonce is %v, the request's %s", harness.ErrStep, s.claims["nonce"], s.flow.Nonce)
	}
	return nil
}

func (s *issuerSteps) headerIs(name, want string) error {
	if got := fmt.Sprint(s.header[name]); got != want {
		return fmt.Errorf("%w: the ID token's header %s is %s, want %s", harness.ErrStep, name, got, want)
	}
	return nil
}

func (s *issuerSteps) tokenError(status int, code string) error {
	if s.answer.Status != status || s.answer.Error != code {
		return fmt.Errorf("%w: the token endpoint answered %+v, want %d %s", harness.ErrStep, s.answer, status, code)
	}
	return nil
}
