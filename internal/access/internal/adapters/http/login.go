package http

import (
	"errors"
	"html/template"
	"net/http"

	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// maxForm bounds a sign-in form's body; a login and a password fit many
// times over.
const maxForm = 16 << 10

// Login serves an organization's break-glass login at
// accessapi.LoginPattern.
type Login struct {
	auth *authn.Service
	// origin is the public URL's origin, which a sign-in must carry, so
	// another site cannot sign a browser in to a session of its choosing.
	origin string
}

// NewLogin returns the login over auth, for the public URL publicURL.
func NewLogin(auth *authn.Service, publicURL string) Login {
	return Login{auth: auth, origin: Origin(publicURL)}
}

// Routes mounts the login's form and sign-in on r.
func (l Login) Routes(r chi.Router) {
	r.Get(accessapi.LoginPattern, l.form)
	r.Post(accessapi.LoginPattern, l.signIn)
}

var loginForm = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Break-glass sign-in: {{.}}</title></head>
<body>
<h1>Break-glass sign-in</h1>
<p>Organization {{.}}</p>
<form method="post">
<label>Login <input name="login" autocomplete="username" required></label>
<label>Password <input name="password" type="password" autocomplete="current-password" required></label>
<button type="submit">Sign in</button>
</form>
</body>
</html>
`))

// form answers the sign-in form, whatever the organization: whether one
// exists, or has a credential, is the sign-in's to refuse alike.
func (Login) form(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set(
		"Content-Security-Policy",
		"default-src 'none'; form-action 'self'; frame-ancestors 'none'",
	)
	h.Set("X-Frame-Options", "DENY")
	_ = loginForm.Execute(w, chi.URLParam(r, "organization"))
}

// signIn checks the form's login and password and, when they match the
// organization's credential, sets the session's cookie and sends the
// browser to the console. Every refusal, a missing Origin among them,
// answers one 401 problem, with no challenge, since no bearer token would
// answer it.
func (l Login) signIn(w http.ResponseWriter, r *http.Request) {
	if l.origin == "" || r.Header.Get("Origin") != l.origin {
		problem.Write(w, problem.Unauthenticated.New(refused))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxForm)
	if err := r.ParseForm(); err != nil {
		problem.Write(w, problem.InvalidRequest.New("The sign-in form cannot be read."))
		return
	}
	b, err := l.auth.SignInBreakGlass(r.Context(), chi.URLParam(r, "organization"),
		r.PostForm.Get("login"), r.PostForm.Get("password"), ratelimit.ClientAddr(r))
	if errors.Is(err, authn.ErrUnauthenticated) {
		problem.Write(w, problem.Unauthenticated.New(refused))
		return
	}
	if err != nil {
		problem.Failure(w, err)
		return
	}
	w.Header().Set("Set-Cookie", sessionCookie(b))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
