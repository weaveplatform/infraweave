package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/wire/management"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// APIBasePath is where the account API is served under an endpoint.
const APIBasePath = "/api/v1"

// errNotFound is a management lookup that found no live pool.
var errNotFound = errors.New("worker controller: no managed pool")

// apiKey is a management API key as its Secret holds it.
type apiKey struct{ id, secret string }

// session is an API token exchanged for an API key, until expires.
type session struct {
	jwt     string
	expires time.Time
}

// remote is the infraweave installation at one WorkerPool endpoint: its
// worker protocol and its account API. API tokens are kept per key until
// shortly before they expire, so a reconciliation does not exchange the
// key on each pass.
type remote struct {
	worker *client.Client
	api    *management.ClientWithResponses
	now    func() time.Time

	mu       sync.Mutex
	sessions map[apiKey]session
}

func newRemote(endpoint string, doer *http.Client, now func() time.Time) *remote {
	endpoint = strings.TrimSuffix(endpoint, "/")
	// The generated constructor fails only when an option does, and
	// WithHTTPClient never does.
	api, _ := management.NewClientWithResponses(endpoint+APIBasePath, management.WithHTTPClient(doer))
	return &remote{
		worker: client.New(client.Base(endpoint), doer), api: api, now: now,
		sessions: map[apiKey]session{},
	}
}

// refused is the *client.Error of an account API response that is not the
// one expected, reading its problem type and detail when it has them.
func refused(op string, res *http.Response, body []byte) error {
	e := &client.Error{Op: op, Status: res.StatusCode}
	var p struct {
		Type   string `json:"type"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal(body, &p) == nil {
		e.Type, e.Detail = p.Type, p.Detail
	}
	return e
}

// token answers an API token for key, exchanging it when none is kept or
// the kept one expires within a minute.
func (r *remote) token(ctx context.Context, key apiKey) (string, error) {
	r.mu.Lock()
	s, ok := r.sessions[key]
	r.mu.Unlock()
	if ok && r.now().Add(time.Minute).Before(s.expires) {
		return s.jwt, nil
	}
	res, err := r.api.ApiKeyUserWithResponse(ctx, nil, management.ApiKeyUserRequest{Id: key.id, Secret: key.secret})
	if err != nil {
		return "", fmt.Errorf("exchange the management key: %w", err)
	}
	if res.JSON200 == nil {
		return "", refused("exchange the management key", res.HTTPResponse, res.Body)
	}
	s = session{jwt: res.JSON200.Jwt, expires: time.Unix(int64(res.JSON200.ValidUntil), 0)}
	r.mu.Lock()
	r.sessions[key] = s
	r.mu.Unlock()
	return s.jwt, nil
}

// forget drops key's kept token after the server refused it.
func (r *remote) forget(key apiKey) {
	r.mu.Lock()
	delete(r.sessions, key)
	r.mu.Unlock()
}

func bearer(jwt string) management.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+jwt)
		return nil
	}
}

// account is the ID of the account jwt's key belongs to.
func (r *remote) account(ctx context.Context, jwt string) (string, error) {
	res, err := r.api.IdWithResponse(ctx, bearer(jwt))
	if err != nil {
		return "", fmt.Errorf("read the management account: %w", err)
	}
	if res.JSON200 == nil {
		return "", refused("read the management account", res.HTTPResponse, res.Body)
	}
	return *res.JSON200, nil
}

func ownerParams(owner kernel.WorkerOwner) management.WorkerPoolManagement {
	return management.WorkerPoolManagement{ClusterID: owner.ClusterID, Namespace: owner.Namespace, ResourceUID: owner.ResourceUID}
}

// managed reads the live pool owner manages and its ETag; errNotFound when
// there is none, including when it was deleted.
func (r *remote) managed(ctx context.Context, jwt string, owner kernel.WorkerOwner) (management.WorkerPool, string, error) {
	res, err := r.api.WorkerPoolManagedWithResponse(ctx, &management.WorkerPoolManagedParams{
		ClusterID: owner.ClusterID, Namespace: owner.Namespace, ResourceUID: owner.ResourceUID,
	}, bearer(jwt))
	if err != nil {
		return management.WorkerPool{}, "", fmt.Errorf("read the managed pool: %w", err)
	}
	if res.StatusCode() == http.StatusNotFound {
		return management.WorkerPool{}, "", errNotFound
	}
	if res.JSON200 == nil {
		return management.WorkerPool{}, "", refused("read the managed pool", res.HTTPResponse, res.Body)
	}
	return *res.JSON200, res.HTTPResponse.Header.Get("ETag"), nil
}

// create creates the pool owner manages. Its Idempotency-Key derives from
// the resource UID, so a retry after a lost response replays the creation
// rather than creating another pool.
func (r *remote) create(ctx context.Context, jwt string, owner kernel.WorkerOwner, in management.WorkerPoolCreateRequest) (management.WorkerPool, error) {
	key := "worker-pool-" + owner.ResourceUID
	m := ownerParams(owner)
	in.Management = &m
	res, err := r.api.WorkerPoolCreateWithResponse(ctx, &management.WorkerPoolCreateParams{IdempotencyKey: &key}, in, bearer(jwt))
	if err != nil {
		return management.WorkerPool{}, fmt.Errorf("create the managed pool: %w", err)
	}
	if res.JSON201 == nil {
		return management.WorkerPool{}, refused("create the managed pool", res.HTTPResponse, res.Body)
	}
	return *res.JSON201, nil
}

// update replaces the managed pool id's settings with in, conditional on
// etag and carrying owner, as managed settings require.
func (r *remote) update(ctx context.Context, jwt, id, etag string, owner kernel.WorkerOwner, in management.WorkerPoolUpdateRequest) error {
	m := ownerParams(owner)
	in.Management = &m
	res, err := r.api.WorkerPoolUpdateWithResponse(ctx, id, &management.WorkerPoolUpdateParams{IfMatch: &etag}, in, bearer(jwt))
	if err != nil {
		return fmt.Errorf("update the managed pool: %w", err)
	}
	if res.JSON200 == nil {
		return refused("update the managed pool", res.HTTPResponse, res.Body)
	}
	return nil
}

// remove deletes the managed pool id through the ordinary delete and its
// guards; an already deleted pool succeeds.
func (r *remote) remove(ctx context.Context, jwt, id string) error {
	res, err := r.api.WorkerPoolDeleteWithResponse(ctx, id, &management.WorkerPoolDeleteParams{}, bearer(jwt))
	if err != nil {
		return fmt.Errorf("delete the managed pool: %w", err)
	}
	if res.JSON200 == nil && res.StatusCode() != http.StatusNotFound {
		return refused("delete the managed pool", res.HTTPResponse, res.Body)
	}
	return nil
}

// remotes keeps one remote per endpoint.
type remotes struct {
	doer *http.Client
	now  func() time.Time

	mu  sync.Mutex
	all map[string]*remote
}

func (rs *remotes) at(endpoint string) *remote {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	r, ok := rs.all[endpoint]
	if !ok {
		r = newRemote(endpoint, rs.doer, rs.now)
		rs.all[endpoint] = r
	}
	return r
}
