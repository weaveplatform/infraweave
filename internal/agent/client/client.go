package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// BasePath is where the worker API is served under the server's public
// URL.
const BasePath = "/worker/v1"

// Base is the worker API's base URL under the server's public URL public,
// which a pool token names.
func Base(public string) string { return strings.TrimSuffix(public, "/") + BasePath }

// Client calls the worker API at one base URL, and the pre-signed URLs it
// answers.
type Client struct {
	c    *wire.ClientWithResponses
	doer wire.HttpRequestDoer
	base string
}

// New is a client of the worker API at base, sending its requests through
// doer. A base that is no URL fails each call.
func New(base string, doer wire.HttpRequestDoer) *Client {
	// The generated constructor fails only when an option does, and
	// WithHTTPClient never does.
	c, _ := wire.NewClientWithResponses(base, wire.WithHTTPClient(doer))
	// A request's path resolves against base, so base ends in a slash,
	// as the generated client's does, or its last segment would be lost.
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return &Client{c: c, doer: doer, base: base}
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

// UploadURLs declares the uploads of run's state version in.StateVersion
// with its run token, and answers where to PUT each, in the order
// declared. A declaration repeated as it was answers the same object.
func (c *Client) UploadURLs(
	ctx context.Context,
	token, run string,
	in wire.UploadURLsRequest,
) ([]wire.SignedUpload, error) {
	const op = "declare uploads"
	res, err := c.c.WorkerRunUploadURLsWithResponse(ctx, run, in, bearer(token))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	if res.JSON200 == nil {
		return nil, refused(op, res.HTTPResponse, res.Body)
	}
	return res.JSON200.Uploads, nil
}

// maxStoreAnswer bounds what Put reads of an object store's answer.
const maxStoreAnswer = 64 << 10

// Put uploads body to u, a pre-signed URL UploadURLs answered, with the
// headers it names and no bearer token, since the URL is its own
// credential; no error names the URL. A store's refusal is an *Error
// with its status.
func (c *Client) Put(ctx context.Context, u wire.SignedUpload, body []byte) error {
	return c.put(ctx, u, bytes.NewReader(body), int64(len(body)))
}

// PutFile uploads the file at path to u, as Put does, reading it as it
// sends, so an upload as large as a saved workspace never sits in memory.
func (c *Client) PutFile(ctx context.Context, u wire.SignedUpload, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("upload %s: %w", u.Name, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("upload %s: %w", u.Name, err)
	}
	return c.put(ctx, u, f, info.Size())
}

func (c *Client) put(ctx context.Context, u wire.SignedUpload, body io.Reader, size int64) error {
	op := "upload " + u.Name
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.Url, body)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	req.ContentLength = size
	for k, v := range u.Headers {
		req.Header.Set(k, v)
	}
	res, err := c.doer.Do(req)
	if err != nil {
		// The URL's query is its signature, which the error would carry
		// into the runner's log.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("%s: %w", op, err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxStoreAnswer))
	if res.StatusCode/100 != 2 {
		return &Error{Op: op, Status: res.StatusCode}
	}
	return nil
}

// Artifacts records uploaded artifacts and workspaces of run's state
// version in.StateVersion with its run token. One already recorded is
// skipped.
func (c *Client) Artifacts(ctx context.Context, token, run string, in wire.ArtifactsRequest) error {
	const op = "record artifacts"
	res, err := c.c.WorkerRunArtifactsWithResponse(ctx, run, in, bearer(token))
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if res.StatusCode() != http.StatusNoContent {
		return refused(op, res.HTTPResponse, res.Body)
	}
	return nil
}

// Workspace answers the pre-signed URL of run's saved workspace, which
// the worker API answers as a redirect, with its run token. The redirect
// is not followed: the store would be sent the bearer token, which the
// HTTP client forwards to a URL on the same host. A run with none saved
// is an *Error with 404.
func (c *Client) Workspace(ctx context.Context, token, run string) (string, error) {
	const op = "find the saved workspace"
	doer := c.doer
	if hc, ok := doer.(*http.Client); ok {
		stay := *hc
		stay.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
		doer = &stay
	}
	req, err := wire.NewWorkerRunWorkspaceRequest(c.base, run)
	if err != nil {
		return "", fmt.Errorf("%s: %w", op, err)
	}
	req = req.WithContext(ctx)
	_ = bearer(token)(ctx, req)
	res, err := doer.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: %w", op, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, maxStoreAnswer))
	loc := res.Header.Get("Location")
	if res.StatusCode != http.StatusTemporaryRedirect || loc == "" {
		return "", refused(op, res, body)
	}
	return loc, nil
}

// LogChunks commits uploaded log chunks of run's state version
// in.StateVersion with its run token. A chunk already committed is
// skipped.
func (c *Client) LogChunks(ctx context.Context, token, run string, in wire.LogChunksRequest) error {
	const op = "commit log chunks"
	res, err := c.c.WorkerRunLogChunksWithResponse(ctx, run, in, bearer(token))
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if res.StatusCode() != http.StatusNoContent {
		return refused(op, res.HTTPResponse, res.Body)
	}
	return nil
}
