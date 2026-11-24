package http

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/linking"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// maxLogoutBody bounds a back-channel logout's form; a logout token is a
// few kilobytes.
const maxLogoutBody = 64 << 10

// SignIn serves the sign-in through an organization's identity providers
// beside the APIs: pages for a browser, whose every refusal is a page, not
// a problem, and the back-channel logout its OIDC providers post to.
//
// ref: decision 0006 § "Human login"
type SignIn struct {
	svc  *sso.SignIn
	auth *authn.Service
}

// NewSignIn returns the pages over svc, reading a browser's session
// through auth.
func NewSignIn(svc *sso.SignIn, auth *authn.Service) SignIn {
	return SignIn{svc: svc, auth: auth}
}

// Routes mounts the sign-in's pages and the back-channel logout on r.
func (s SignIn) Routes(r chi.Router) {
	r.Get(accessapi.SignInPattern, s.choose)
	r.Get(accessapi.SignInOrganizationPattern, s.list)
	r.Get(accessapi.SignInProviderPattern, s.start)
	r.Get(accessapi.OIDCCallbackPattern, s.callback)
	r.Post(accessapi.OIDCBackchannelLogoutPattern, s.backchannel)
}

// signInPage is what a sign-in page shows: an organization's form, its
// providers, or a refusal with its code.
type signInPage struct {
	Title        string
	Message      string
	Code         string
	Organization string
	ReturnTo     string
	Choose       bool
	Providers    []link
}

type link struct{ Name, Href string }

var pageTemplate = template.Must(template.New("signin").Parse(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title></head>
<body>
<h1>{{.Title}}</h1>
{{if .Message}}<p id="message">{{.Message}}</p>{{end}}
{{if .Code}}<p>Code: <code id="code">{{.Code}}</code></p>{{end}}
{{if .Choose}}<form method="get">
<label>Organization <input name="organization" required></label>
{{if .ReturnTo}}<input type="hidden" name="return_to" value="{{.ReturnTo}}">{{end}}
<button type="submit">Continue</button>
</form>{{end}}
{{if .Providers}}<ul>{{range .Providers}}<li><a href="{{.Href}}">{{.Name}}</a></li>{{end}}</ul>{{end}}
</body>
</html>
`))

// render answers status with p, never cached and never framed.
func render(w http.ResponseWriter, status int, p signInPage) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_ = pageTemplate.Execute(w, p)
}

// signInPath is the path of pattern's page for the organization and
// provider, with return_to when it is not the console's root.
func signInPath(org, provider, returnTo string) string {
	p := "/auth/login/" + url.PathEscape(org)
	if provider != "" {
		p += "/" + url.PathEscape(provider)
	}
	if returnTo != "" && returnTo != "/" {
		p += "?" + url.Values{"return_to": {returnTo}}.Encode()
	}
	return p
}

// choose sends the browser to its organization's page: the one named by
// the form's organization, or the installation's only one; otherwise it
// asks for the organization.
func (s SignIn) choose(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	returnTo := q.Get("return_to")
	if org := q.Get("organization"); org != "" {
		redirect(w, r, signInPath(org, "", returnTo))
		return
	}
	slug, ok, err := s.svc.SoleOrganization(r.Context())
	if err != nil {
		refuse(w, r, err)
		return
	}
	if ok {
		redirect(w, r, signInPath(string(slug), "", returnTo))
		return
	}
	render(w, http.StatusOK, signInPage{Title: "Sign in", Choose: true, ReturnTo: returnTo})
}

// list answers the organization's enabled providers to choose from, or
// sends the browser to the one there is.
func (s SignIn) list(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "organization")
	returnTo := r.URL.Query().Get("return_to")
	org, ps, err := s.svc.Organization(r.Context(), slug)
	if err != nil {
		refuse(w, r, err)
		return
	}
	if len(ps) == 1 {
		redirect(w, r, signInPath(string(org.Slug), ps[0].ID.String(), returnTo))
		return
	}
	p := signInPage{Title: "Sign in to " + org.Name, Organization: string(org.Slug)}
	if len(ps) == 0 {
		p.Message = "This organization has no identity provider to sign in through."
	}
	for _, pr := range ps {
		p.Providers = append(p.Providers, link{
			Name: pr.Name, Href: signInPath(string(org.Slug), pr.ID.String(), returnTo),
		})
	}
	render(w, http.StatusOK, p)
}

// start starts a sign-in through the provider and sends the browser to
// its authorization endpoint, with the cookie that binds the request to
// the browser.
func (s SignIn) start(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	by, err := s.browser(r)
	if err != nil {
		refuse(w, r, err)
		return
	}
	st, err := s.svc.Start(r.Context(), sso.Start{
		Organization: chi.URLParam(r, "organization"),
		Provider:     chi.URLParam(r, "providerId"),
		Purpose:      q.Get("purpose"),
		ReturnTo:     q.Get("return_to"),
		By:           by,
	})
	if err != nil {
		refuse(w, r, err)
		return
	}
	w.Header().Add("Set-Cookie", requestCookie(st.Cookie, int(sso.RequestTTL.Seconds())))
	redirect(w, r, st.Location)
}

// callback finishes the sign-in and sends the browser where it was going,
// signed in; the request's cookie goes either way, as the request is
// spent or was never this browser's.
func (s SignIn) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	w.Header().Add("Set-Cookie", requestCookie("", -1))
	by, err := s.browser(r)
	if err != nil {
		refuse(w, r, err)
		return
	}
	var cookie string
	if c, err := r.Cookie(sso.RequestCookie); err == nil {
		cookie = c.Value
	}
	in, err := s.svc.Callback(r.Context(), sso.Callback{
		State: q.Get("state"), Code: q.Get("code"), Error: q.Get("error"),
		Cookie: cookie, By: by, IP: ratelimit.ClientAddr(r),
	})
	if err != nil {
		refuse(w, r, err)
		return
	}
	w.Header().Add("Set-Cookie", sessionCookie(authn.Browser{Secret: in.Secret, Session: in.Session}))
	redirect(w, r, in.ReturnTo)
}

// browser is the principal of the browser's live session, nil for none,
// so a start or a callback that needs one can refuse the browser without.
// A cookie that names no live session is none.
func (s SignIn) browser(r *http.Request) (*kernel.Principal, error) {
	c, err := r.Cookie(session.CookieName)
	if err != nil || c.Value == "" {
		return nil, nil //nolint:nilnil // no session is no principal
	}
	p, _, err := s.auth.AuthenticateBrowser(r.Context(), c.Value, authn.ReachOrganizationAdmin)
	if errors.Is(err, authn.ErrUnauthenticated) {
		return nil, nil //nolint:nilnil // as above
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // the store marks an unreachable database
	}
	return &p, nil
}

// backchannel ends the sessions a provider's logout token names, and
// answers 200, or 400 with OpenID Connect Back-Channel Logout's
// invalid_request for a token it refuses.
func (s SignIn) backchannel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, maxLogoutBody)
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	err := s.svc.BackchannelLogout(r.Context(), r.PostForm.Get("logout_token"))
	switch {
	case err == nil:
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, sso.ErrLogoutToken):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid_request", "error_description": "The logout token is refused.",
		})
	default:
		problem.Failure(w, err)
	}
}

// requestCookie is the Set-Cookie value of the cookie that binds a
// started sign-in to its browser; a negative maxAge removes it.
func requestCookie(value string, maxAge int) string {
	return (&http.Cookie{
		Name:     sso.RequestCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}).String()
}

// redirect answers 302 to loc, never cached. loc is a sign-in page of
// the server's own, a provider's authorization endpoint from its
// discovery document, or a return_to that sso.CheckReturnTo admitted as
// a path on the public URL.
func redirect(w http.ResponseWriter, r *http.Request, loc string) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, loc, http.StatusFound) //nolint:gosec // loc is one of the above, never a URL from the request
}

// refusal is a refused sign-in's page: its status, its code and what it
// tells the person. A refusal names no detail of a provider's answer or a
// token, which are for the server's log.
type refusal struct {
	status        int
	code, message string
}

// refusalOf maps err onto the page that answers it.
func refusalOf(err error) refusal {
	if _, ok := errors.AsType[*featuresapi.LimitError](err); ok {
		return refusal{
			http.StatusForbidden, "limit-reached",
			"The organization has no seat left for a new member.",
		}
	}
	if _, ok := errors.AsType[*featuresapi.Refusal](err); ok {
		return refusal{
			http.StatusForbidden, "feature-not-in-tier",
			"The organization's tier does not include this sign-in.",
		}
	}
	switch {
	case errors.Is(err, sso.ErrRequest):
		return refusal{
			http.StatusBadRequest, "invalid-request",
			"This sign-in is not one this browser started, or it has expired. Start again.",
		}
	case errors.Is(err, sso.ErrToken):
		return refusal{
			http.StatusUnauthorized, "unauthenticated",
			"The identity provider's answer cannot be verified.",
		}
	case errors.Is(err, sso.ErrDeactivated):
		return refusal{http.StatusForbidden, "member-deactivated", "This member is deactivated."}
	case errors.Is(err, sso.ErrOtherMember):
		return refusal{
			http.StatusForbidden, "other-member",
			"The identity provider signed in a different member.",
		}
	case errors.Is(err, linking.ErrEnded):
		return refusal{
			http.StatusGone, "link-ended",
			"This link has expired or completed. An organization admin can open another.",
		}
	case errors.Is(err, linking.ErrNotLinked):
		return refusal{
			http.StatusForbidden, "not-linked",
			"The identity provider signed in an identity of neither linked member.",
		}
	case errors.Is(err, linking.ErrOtherBrowser):
		return refusal{
			http.StatusForbidden, "other-browser",
			"Sign in as the second member in the browser that signed in as the first.",
		}
	case errors.Is(err, sso.ErrForbidden):
		return refusal{http.StatusForbidden, "forbidden", "This browser may not start this sign-in."}
	case errors.Is(err, sso.ErrNotOffered), errors.Is(err, port.ErrNotFound):
		return refusal{http.StatusNotFound, "not-found", "There is no such sign-in."}
	case errors.Is(err, sso.ErrProvider):
		return refusal{
			http.StatusBadGateway, "identity-provider-failed",
			"The identity provider did not complete the sign-in.",
		}
	case errors.Is(err, kernel.ErrUnavailable):
		return refusal{
			http.StatusServiceUnavailable, "unavailable",
			"Sign-in is unavailable for the moment. Try again shortly.",
		}
	}
	return refusal{http.StatusInternalServerError, "internal", "The sign-in failed."}
}

// refuse answers err's page, and logs err, whose detail the page leaves
// out.
func refuse(w http.ResponseWriter, r *http.Request, err error) {
	f := refusalOf(err)
	level := slog.LevelInfo
	if f.status >= http.StatusInternalServerError {
		level = slog.LevelError
	}
	slog.Log(r.Context(), level, "sign-in refused", "code", f.code, "err", err)
	render(w, f.status, signInPage{Title: "Sign-in refused", Message: f.message, Code: f.code})
}
