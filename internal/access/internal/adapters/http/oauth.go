package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/oauth"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// maxOAuthBody bounds a registration's or a token request's body; ten
// redirect URIs of the longest length fit.
const maxOAuthBody = 32 << 10

// OAuth serves the authorization server for MCP clients beside the APIs:
// its protocol answers OAuth's JSON, not problems.
//
// ref: decision 0006 § "The OAuth authorization server for MCP"
type OAuth struct{ svc *oauth.Service }

// NewOAuth returns the server over svc.
func NewOAuth(svc *oauth.Service) OAuth { return OAuth{svc: svc} }

// Routes mounts the metadata, registration, authorization and token
// endpoints on r.
func (o OAuth) Routes(r chi.Router) {
	r.Get(oauth.MetadataPath, o.metadata)
	r.Post(oauth.RegisterPath, o.register)
	r.Get(oauth.AuthorizePath, o.authorize)
	r.Post(oauth.TokenPath, o.token)
}

func (o OAuth) metadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, o.svc.Metadata())
}

// registration is an RFC 7591 request's fields; others are ignored, as
// the RFC lets a server do.
//
//nolint:tagliatelle // the names RFC 7591 sets
type registration struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

// registered is the answer RFC 7591 gives a public client.
//
//nolint:tagliatelle // the names RFC 7591 sets
type registered struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	ClientName              string   `json:"client_name,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

func (o OAuth) register(w http.ResponseWriter, r *http.Request) {
	var in registration
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOAuthBody)).
		Decode(&in); err != nil {
		writeOAuthError(w, &oauth.Error{
			Code: oauth.CodeInvalidClientMetadata, Description: "the body is not a JSON object",
		})
		return
	}
	c, err := o.svc.Register(r.Context(), oauth.Registration(in))
	if err != nil {
		writeOAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, registered{
		ClientID:                c.ID.String(),
		ClientIDIssuedAt:        c.CreatedAt.Unix(),
		ClientName:              c.Name,
		RedirectURIs:            c.RedirectURIs,
		GrantTypes:              []string{oauth.GrantAuthorizationCode, oauth.GrantRefreshToken},
		ResponseTypes:           []string{oauth.ResponseCode},
		TokenEndpointAuthMethod: "none",
	})
}

// authorize answers 302 to the consent page, or to the client with an
// error, and 400 when the client or its redirect URI is not known good.
func (o OAuth) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	loc, err := o.svc.Authorize(r.Context(), oauth.AuthorizeParams{
		ResponseType:        q.Get("response_type"),
		ClientID:            q.Get("client_id"),
		RedirectURI:         q.Get("redirect_uri"),
		Scope:               q.Get("scope"),
		State:               q.Get("state"),
		CodeChallenge:       q.Get("code_challenge"),
		CodeChallengeMethod: q.Get("code_challenge_method"),
		Resource:            q.Get("resource"),
	})
	if err != nil {
		writeOAuthError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, loc, http.StatusFound)
}

// token answers RFC 6749 section 5's JSON, never cached.
func (o OAuth) token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxOAuthBody)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, &oauth.Error{
			Code: oauth.CodeInvalidRequest, Description: "the body is not a form",
		})
		return
	}
	f := r.PostForm
	tok, err := o.svc.Token(r.Context(), oauth.TokenParams{
		GrantType:    f.Get("grant_type"),
		ClientID:     f.Get("client_id"),
		Code:         f.Get("code"),
		RedirectURI:  f.Get("redirect_uri"),
		CodeVerifier: f.Get("code_verifier"),
		RefreshToken: f.Get("refresh_token"),
		Scope:        f.Get("scope"),
		Resource:     f.Get("resource"),
	}, ratelimit.ClientAddr(r))
	if err != nil {
		writeOAuthError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, tok)
}

// writeOAuthError answers an *oauth.Error as RFC 6749 section 5.2's JSON,
// 401 for invalid_client and 400 for every other, and any other error as
// the problem it is, so an unreachable database answers 503.
func writeOAuthError(w http.ResponseWriter, err error) {
	var e *oauth.Error
	if !errors.As(err, &e) {
		problem.Failure(w, err)
		return
	}
	status := http.StatusBadRequest
	if e.Code == oauth.CodeInvalidClient {
		status = http.StatusUnauthorized
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]string{"error": e.Code, "error_description": e.Description})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) //nolint:errchkjson // the types encode
}

// consenter is the caller of a consent operation, which must present a
// browser session, since consent is a console page; the service refuses
// one that is not a member's.
//
// ref: decision 0006 § "The OAuth authorization server for MCP"
func consenter(ctx context.Context) (kernel.Principal, error) {
	p, err := principal(ctx)
	if err != nil {
		return kernel.Principal{}, err
	}
	if !from(ctx).browser {
		//nolint:wrapcheck // a problem, answered as it is
		return kernel.Principal{}, problem.Forbidden.Error(
			"Only a member's browser session answers an authorization request.")
	}
	return p, nil
}

// consentError maps a consent's refusal to its problem.
func consentError(err error, id string) error {
	switch {
	case errors.Is(err, port.ErrNotFound):
		//nolint:wrapcheck // a problem, answered as it is
		return problem.NotFound.Error(fmt.Sprintf(
			"No authorization request %q waits for consent.", id))
	case errors.Is(err, oauth.ErrNotMember):
		//nolint:wrapcheck // as above
		return problem.Forbidden.Error(
			"Only a member's browser session answers an authorization request.")
	case errors.Is(err, oauth.ErrNotAdmitted):
		//nolint:wrapcheck // as above
		return problem.Forbidden.Error("The account does not admit you.")
	case errors.Is(err, oauth.ErrRedirectHost):
		//nolint:wrapcheck // as above
		return problem.Forbidden.Error("The redirect URI is not loopback, and its host is not " +
			"one of the account's oauthRedirectAllowedHosts.")
	}
	return err
}

// requestID parses a consent operation's request ID; one that is not a
// ULID names no request.
func requestID(raw string) (kernel.ULID, error) {
	id, err := kernel.ParseULID(raw)
	if err != nil {
		return kernel.ULID{}, consentError(port.ErrNotFound, raw)
	}
	return id, nil
}

// AuthOAuthRequest is an authorization request as the consent page shows
// it.
func (s Server) AuthOAuthRequest(
	ctx context.Context,
	req api.AuthOAuthRequestRequestObject,
) (api.AuthOAuthRequestResponseObject, error) {
	p, err := consenter(ctx)
	if err != nil {
		return nil, err
	}
	id, err := requestID(req.RequestId)
	if err != nil {
		return nil, err
	}
	r, err := s.oauth.ConsentRequest(ctx, p, id)
	if err != nil {
		return nil, consentError(err, req.RequestId)
	}
	return api.AuthOAuthRequest200JSONResponse{Body: api.AuthOAuthRequest{
		Id:           r.ID.String(),
		ClientId:     r.Client.ID.String(),
		ClientName:   r.Client.Name,
		RedirectUri:  r.RedirectURI,
		RedirectHost: oauth.RedirectHost(r.RedirectURI),
		Resource:     r.Resource,
		Scopes:       r.Scopes,
		ExpiresAt:    r.ExpiresAt.Unix(),
	}}, nil
}

// AuthOAuthRequestApprove grants an authorization request to the
// caller's member in the account the body names.
func (s Server) AuthOAuthRequestApprove(
	ctx context.Context,
	req api.AuthOAuthRequestApproveRequestObject,
) (api.AuthOAuthRequestApproveResponseObject, error) {
	p, err := consenter(ctx)
	if err != nil {
		return nil, err
	}
	id, err := requestID(req.RequestId)
	if err != nil {
		return nil, err
	}
	acct, err := kernel.ParseULID(req.Body.Account)
	if err != nil {
		return nil, consentError(oauth.ErrNotAdmitted, req.RequestId)
	}
	loc, err := s.oauth.Approve(ctx, p, id, kernel.AccountID{ULID: acct})
	if err != nil {
		return nil, consentError(err, req.RequestId)
	}
	return api.AuthOAuthRequestApprove200JSONResponse{
		Body: api.AuthOAuthDecision{RedirectTo: loc},
	}, nil
}

// AuthOAuthRequestDeny refuses an authorization request.
func (s Server) AuthOAuthRequestDeny(
	ctx context.Context,
	req api.AuthOAuthRequestDenyRequestObject,
) (api.AuthOAuthRequestDenyResponseObject, error) {
	p, err := consenter(ctx)
	if err != nil {
		return nil, err
	}
	id, err := requestID(req.RequestId)
	if err != nil {
		return nil, err
	}
	loc, err := s.oauth.Deny(ctx, p, id)
	if err != nil {
		return nil, consentError(err, req.RequestId)
	}
	return api.AuthOAuthRequestDeny200JSONResponse{
		Body: api.AuthOAuthDecision{RedirectTo: loc},
	}, nil
}
