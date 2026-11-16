package urls

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/moving"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
	"github.com/weaveplatform/infraweave/internal/state/internal/signed"
)

// State is what the URLs ask of the state domain's core.
type State interface {
	Upload(ctx context.Context, acct kernel.AccountID, object kernel.ULID, subject string, body io.Reader, size int64) error
	Download(ctx context.Context, acct kernel.AccountID, stack, version kernel.ULID) (core.Version, io.ReadCloser, error)
	Latest(ctx context.Context, acct kernel.AccountID, stack kernel.ULID, workspace string) (stateapi.Version, bool, error)
}

// Stacks is what the URLs read of the stacks domain; stacksapi.Stacks is
// one.
type Stacks interface {
	BySlug(ctx context.Context, acct kernel.AccountID, slug string) (stacksapi.Stack, error)
	Settings(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Settings, error)
}

// Authorizer decides the action stateDownloadUrl needs beside its
// route's; accessapi.Authorizer is one.
type Authorizer interface {
	Authorize(ctx context.Context, p kernel.Principal, action accessapi.Action, space kernel.ULID) (bool, error)
}

// Deps are what Server takes: State, Stacks and Authz; Signer, which
// signs and verifies the tokens; Public, server.public_url, which every
// URL is absolute under; and TTL, how long a URL lasts.
type Deps struct {
	State  State
	Stacks Stacks
	Authz  Authorizer
	Signer signed.Signer
	Public string
	TTL    time.Duration
}

// Server is the state domain's partial of the API's strict server, which
// signs the state URLs, and the handler of their routes, which verifies
// them. Its fields are unexported, since internal/app embeds it beside
// every other domain's partial.
type Server struct{ d Deps }

// New returns the partial over d.
func New(d Deps) Server { return Server{d: d} }

func principal(ctx context.Context) (kernel.Principal, error) {
	p, ok := kernel.PrincipalFrom(ctx)
	if !ok {
		//nolint:wrapcheck // a problem, answered as it is
		return kernel.Principal{}, problem.Unauthenticated.Error(
			"The request carries no valid credential.")
	}
	return p, nil
}

// url is the absolute URL of pattern with token in place of {token}.
func (s Server) url(pattern, token string) string {
	return strings.TrimSuffix(s.d.Public, "/") + strings.Replace(pattern, "{token}", token, 1)
}

// StateUploadUrl signs a URL that takes one PUT of a state file from the
// caller, and the object ID an import names the file by. The route has
// decided STACK_MANAGED_STATE_IMPORT; an import decides it again on its
// stack. The URL needs no header, so uploadHeaders is empty.
//
// ref: decision 0010 § "Upload, import and download URLs"
func (s Server) StateUploadUrl(ctx context.Context, _ api.StateUploadUrlRequestObject) (api.StateUploadUrlResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	object := kernel.NewULID()
	token, err := s.d.Signer.Sign(signed.Token{
		Kind: signed.Upload, Account: p.AccountID, Subject: p.Caller(), Object: object,
	}, s.d.TTL)
	if err != nil {
		return nil, err //nolint:wrapcheck // signed names the failure
	}
	return api.StateUploadUrl200JSONResponse{Body: api.StateUploadUrl{
		ObjectId:      object.String(),
		Url:           s.url(stateapi.UploadPattern, token),
		UploadHeaders: &api.StringMap{Entries: []api.KeyValuePairType{}},
	}}, nil
}

// StateDownloadUrl signs a URL that reads the current version of the
// stack's default workspace. The route has decided STACK_STATE_DOWNLOAD
// on the stack's space, and found the stack readable; STACK_STATE_READ is
// decided here, since a route names one action. A stack whose state
// infraweave does not keep answers 404, and one whose external state
// access is off 403, in the order every external read answers in. A stack
// with no state yet answers 404, since there is no version to sign.
//
// ref: decision 0010 § "Who may call what" and § "Upload, import and download URLs"
func (s Server) StateDownloadUrl(ctx context.Context, req api.StateDownloadUrlRequestObject) (api.StateDownloadUrlResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body != nil && req.Body.Version != nil {
		return nil, &kernel.Invalid{
			Detail: "A download URL reads the stack's current state.",
			Errors: []kernel.FieldError{{
				Pointer: "/version", Reason: kernel.ReasonRule,
				Message: "No version but the current one can be downloaded.",
			}},
		}
	}
	st, err := s.stack(ctx, p.AccountID, req.StackId)
	if err != nil {
		return nil, err
	}
	allowed, err := s.d.Authz.Authorize(ctx, p, accessapi.ActionStackStateRead, st.Space)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	if !allowed {
		f := problem.Forbidden.New(fmt.Sprintf("You may not perform %s here.",
			accessapi.ActionStackStateRead))
		f.Action = string(accessapi.ActionStackStateRead)
		return nil, &problem.Error{Problem: f}
	}
	if !st.ExternalStateAccess {
		return nil, problem.Forbidden.Error(externalOff) //nolint:wrapcheck // a problem
	}
	v, ok, err := s.d.State.Latest(ctx, p.AccountID, st.ID, stateapi.DefaultWorkspace)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	if !ok {
		return nil, problem.NotFound.Error( //nolint:wrapcheck // a problem
			fmt.Sprintf("Stack %q has no state yet.", req.StackId))
	}
	token, err := s.d.Signer.Sign(signed.Token{
		Kind: signed.Download, Account: p.AccountID, Subject: p.Caller(),
		Stack: st.ID, Version: v.ID,
	}, s.d.TTL)
	if err != nil {
		return nil, err //nolint:wrapcheck // signed names the failure
	}
	return api.StateDownloadUrl200JSONResponse{Body: api.StateDownloadUrl{
		Url: s.url(stateapi.DownloadPattern, token),
	}}, nil
}

const externalOff = "The stack's external state access is off."

// stack reads acct's stack slug's settings: 404 when acct lacks it or
// infraweave does not keep its state.
func (s Server) stack(ctx context.Context, acct kernel.AccountID, slug string) (stacksapi.Settings, error) {
	st, err := s.d.Stacks.BySlug(ctx, acct, slug)
	var settings stacksapi.Settings
	if err == nil {
		settings, err = s.d.Stacks.Settings(ctx, acct, st.ID)
	}
	switch {
	case errors.Is(err, stacksapi.ErrNotFound), err == nil && !settings.ManagesState:
		return stacksapi.Settings{}, problem.NotFound.Error(fmt.Sprintf( //nolint:wrapcheck // a problem
			"No stack whose state infraweave keeps is named %q.", slug))
	case err != nil:
		return stacksapi.Settings{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	return settings, nil
}

// Routes adds PUT stateapi.UploadPattern and GET stateapi.DownloadPattern
// to r, the API listener's router. Each answers 403 for a token that does
// not verify as its kind. Once one verifies, its transfer's deadlines move
// to moving.Stall past each read or write, as the backend's do.
func (s Server) Routes(r chi.Router) {
	r.Put(stateapi.UploadPattern, s.upload)
	r.Get(stateapi.DownloadPattern, s.download)
}

// Unserved adds the routes Routes adds, each answering 501, for an API
// built without the state domain.
func Unserved(r chi.Router) {
	f := func(w http.ResponseWriter, _ *http.Request) {
		problem.Write(w, problem.NotImplemented.New("No state domain serves this route."))
	}
	r.Put(stateapi.UploadPattern, f)
	r.Get(stateapi.DownloadPattern, f)
}

// upload stores r's body as the state file its token names, sealed. The
// body declares its length, at most core.MaxUpload, since the store takes
// an object's size before it streams; a second upload to one URL answers
// 409.
//
// ref: decision 0010 § "Upload, import and download URLs"
func (s Server) upload(w http.ResponseWriter, r *http.Request) {
	t, err := s.d.Signer.Verify(signed.Upload, chi.URLParam(r, "token"))
	if err != nil {
		problem.Write(w, problem.Forbidden.New(err.Error()))
		return
	}
	switch {
	case r.ContentLength < 0:
		problem.Write(w, problem.InvalidRequest.New("An upload declares its Content-Length."))
		return
	case r.ContentLength > core.MaxUpload:
		problem.Write(w, problem.TooLarge(core.MaxUpload))
		return
	}
	body := &failed{ReadCloser: moving.Start(w, moving.Stall, true).Reader(r.Body)}
	err = s.d.State.Upload(r.Context(), t.Account, t.Object, t.Subject, body, r.ContentLength)
	switch {
	case errors.Is(err, core.ErrUploaded):
		problem.Write(w, problem.InvalidState.New(
			"This URL has taken its upload; ask for another to upload again."))
	case err != nil && body.err != nil:
		problem.BodyError(w, body.err)
	case err != nil:
		problem.Failure(w, err)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

// download streams the state version its token names, unsealed. The
// stack must still exist, keep its state and have external state access
// on; the caller's actions are the signature's, decided when it signed.
//
// ref: decision 0010 § "Upload, import and download URLs"
func (s Server) download(w http.ResponseWriter, r *http.Request) {
	t, err := s.d.Signer.Verify(signed.Download, chi.URLParam(r, "token"))
	if err != nil {
		problem.Write(w, problem.Forbidden.New(err.Error()))
		return
	}
	ctx := r.Context()
	st, err := s.d.Stacks.Settings(ctx, t.Account, t.Stack)
	switch {
	case errors.Is(err, stacksapi.ErrNotFound), err == nil && !st.ManagesState:
		problem.Write(w, problem.NotFound.New("No stack whose state infraweave keeps is "+
			"behind this URL."))
		return
	case err != nil:
		problem.Failure(w, err)
		return
	case !st.ExternalStateAccess:
		problem.Write(w, problem.Forbidden.New(externalOff))
		return
	}
	v, body, err := s.d.State.Download(ctx, t.Account, t.Stack, t.Version)
	switch {
	case errors.Is(err, core.ErrGone):
		problem.Write(w, problem.NotFound.New("The state version this URL names is gone."))
		return
	case err != nil:
		problem.Failure(w, err)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.FormatInt(v.Size, 10))
	// A copy that fails cuts the answer short of its Content-Length, which
	// the client reads as a failed transfer.
	_, _ = io.Copy(moving.Start(w, moving.Stall, false).Writer(w), body)
}

// failed is a body that keeps the first error a read of it met other than
// its end, which explains an upload that fails while the body streams.
type failed struct {
	io.ReadCloser
	err error
}

func (f *failed) Read(p []byte) (int, error) {
	n, err := f.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && f.err == nil {
		f.err = err
	}
	return n, err //nolint:wrapcheck // the body's own error, as io.Reader returns it
}
