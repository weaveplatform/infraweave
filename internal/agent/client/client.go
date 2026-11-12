package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// BasePath is where the worker API is served under the server's public
// URL.
const BasePath = "/worker/v1"

// Base is the worker API's base URL under the server's public URL public,
// which a pool token names.
func Base(public string) string { return strings.TrimSuffix(public, "/") + BasePath }

// Client calls the worker API at one base URL.
type Client struct{ c *wire.ClientWithResponses }

// New is a client of the worker API at base, sending its requests through
// doer. A base that is no URL fails each call.
func New(base string, doer wire.HttpRequestDoer) *Client {
	// The generated constructor fails only when an option does, and
	// WithHTTPClient never does.
	c, _ := wire.NewClientWithResponses(base, wire.WithHTTPClient(doer))
	return &Client{c: c}
}

// Error is a call the server refused: its status and problem detail.
type Error struct {
	Op     string
	Status int
	Type   string
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s: %d %s", e.Op, e.Status, http.StatusText(e.Status))
	}
	return fmt.Sprintf("%s: %d %s", e.Op, e.Status, e.Detail)
}

// Status is err's status when it is an *Error, else 0.
func Status(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

// refused is the *Error of a response that is not the one expected,
// reading its problem detail when it has one.
func refused(op string, res *http.Response, body []byte) error {
	e := &Error{Op: op, Status: res.StatusCode}
	var p wire.Problem
	if json.Unmarshal(body, &p) == nil {
		if p.Type != nil {
			e.Type = *p.Type
		}
		if p.Detail != nil {
			e.Detail = *p.Detail
		}
	}
	return e
}

// bearer sets token as the request's bearer token.
func bearer(token string) wire.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
}

// Register registers a worker with the signed registration jws.
func (c *Client) Register(ctx context.Context, jws string) (wire.Registration, error) {
	res, err := c.c.WorkerRegisterWithResponse(ctx, wire.RegisterRequest{Registration: jws})
	if err != nil {
		return wire.Registration{}, fmt.Errorf("register: %w", err)
	}
	if res.JSON200 == nil {
		return wire.Registration{}, refused("register", res.HTTPResponse, res.Body)
	}
	return *res.JSON200, nil
}

// Poll polls as the worker whose session token is session.
func (c *Client) Poll(
	ctx context.Context,
	session string,
	in wire.PollRequest,
) (wire.PollResult, error) {
	res, err := c.c.WorkerPollWithResponse(ctx, in, bearer(session))
	if err != nil {
		return wire.PollResult{}, fmt.Errorf("poll: %w", err)
	}
	if res.JSON200 == nil {
		return wire.PollResult{}, refused("poll", res.HTTPResponse, res.Body)
	}
	return *res.JSON200, nil
}

// Deregister removes the worker whose session token is session.
func (c *Client) Deregister(ctx context.Context, session string) error {
	res, err := c.c.WorkerDeregisterWithResponse(ctx, bearer(session))
	if err != nil {
		return fmt.Errorf("deregister: %w", err)
	}
	if res.StatusCode() != http.StatusNoContent {
		return refused("deregister", res.HTTPResponse, res.Body)
	}
	return nil
}

// Spec fetches run's specification with its run token.
func (c *Client) Spec(ctx context.Context, token, run string) (wire.RunSpec, error) {
	res, err := c.c.WorkerRunSpecWithResponse(ctx, run, bearer(token))
	if err != nil {
		return wire.RunSpec{}, fmt.Errorf("fetch the run's specification: %w", err)
	}
	if res.JSON200 == nil {
		return wire.RunSpec{}, refused("fetch the run's specification", res.HTTPResponse, res.Body)
	}
	return *res.JSON200, nil
}

// Report reports r of run with its run token and answers the next step.
func (c *Client) Report(
	ctx context.Context,
	token, run string,
	r wire.RunReport,
) (wire.NextStep, error) {
	op := fmt.Sprintf("report %s %s", r.State, r.Event)
	res, err := c.c.WorkerRunReportWithResponse(ctx, run, r, bearer(token))
	if err != nil {
		return wire.NextStep{}, fmt.Errorf("%s: %w", op, err)
	}
	if res.JSON200 == nil {
		return wire.NextStep{}, refused(op, res.HTTPResponse, res.Body)
	}
	return *res.JSON200, nil
}
