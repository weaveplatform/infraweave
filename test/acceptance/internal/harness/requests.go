package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/vault"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/deps"
)

// Response is one answer a scenario's step received.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
	// Err is the SDK's typed error of an answer of 400 or more.
	Err error
}

// record keeps r as the scenario's last answer.
func (w *World) record(r Response) Response {
	w.Sent = append(w.Sent, r)
	return r
}

// Last is the scenario's last answer, or none.
func (w *World) Last() Response {
	if len(w.Sent) == 0 {
		return Response{}
	}
	return w.Sent[len(w.Sent)-1]
}

// Send drives the first server through pkg/sdk, as every infraweave
// client does, on its admin or API listener. The scenario's clients send
// each call once, so a step sees the answer it asserts on rather than the
// SDK's retry of it.
func (w *World) Send(ctx context.Context, method, listener, path string) (Response, error) {
	c := w.API
	if listener == "admin" {
		c = w.Admin
	}
	resp, err := c.Do(ctx, method, path, nil)
	if resp == nil {
		return Response{}, err
	}
	return w.record(
		Response{Status: resp.Status, Header: resp.Header, Body: resp.Body, Err: err},
	), nil
}

// replica is the client of the named server's API listener, A or B, which
// holds the home tenant's token.
func (w *World) replica(name string) *sdk.Client {
	if name == "B" {
		return w.APIs[1]
	}
	return w.APIs[0]
}

// SendThrough sends a request through the named replica's API listener.
func (w *World) SendThrough(
	ctx context.Context,
	name, method, path string,
	body any,
	headers ...sdk.Header,
) (Response, error) {
	resp, err := w.replica(name).Do(ctx, method, path, body, headers...)
	if resp == nil {
		return Response{}, err
	}
	return w.record(
		Response{Status: resp.Status, Header: resp.Header, Body: resp.Body, Err: err},
	), nil
}

// Client is a client of the replica's API listener holding token, or no
// token when token is empty.
func (w *World) Client(replica, token string) (*sdk.Client, error) {
	i := 0
	if replica == "B" {
		i = 1
	}
	opts := []sdk.Option{sdk.WithRetry(sdk.NoRetry)}
	if token != "" {
		opts = append(opts, sdk.WithToken(token))
	}
	c, err := sdk.New("http://"+w.Servers[i].API, opts...)
	if err != nil {
		return nil, fmt.Errorf("sdk: %w", err)
	}
	return c, nil
}

// Do sends a request through replica with the named token.
func (w *World) Do(
	ctx context.Context,
	replica, name, method, path string,
	body any,
	headers ...sdk.Header,
) (Response, error) {
	tok, ok := w.Tokens[name]
	if !ok && name != "" {
		return Response{}, fmt.Errorf("%w: no token %q", ErrStep, name)
	}
	c, err := w.Client(replica, tok)
	if err != nil {
		return Response{}, err
	}
	resp, err := c.Do(ctx, method, path, body, headers...)
	if resp == nil {
		return Response{}, err
	}
	return w.record(
		Response{Status: resp.Status, Header: resp.Header, Body: resp.Body, Err: err},
	), nil
}

// Base is the replica's API listener.
func (w *World) Base(replica string) string {
	i := 0
	if replica == "B" {
		i = 1
	}
	return "http://" + w.Servers[i].API
}

// browserClient is the SDK's client of the replica through the browser's
// jar.
func (w *World) browserClient(replica string) (*sdk.Client, error) {
	c, err := sdk.New(w.Base(replica), sdk.WithRetry(sdk.NoRetry), sdk.WithHTTPClient(w.Jar))
	if err != nil {
		return nil, fmt.Errorf("sdk: %w", err)
	}
	return c, nil
}

// BrowserDo sends a request through the browser, with origin when it is
// not empty.
func (w *World) BrowserDo(
	ctx context.Context,
	replica, method, path, origin string,
	body any,
) (Response, error) {
	c, err := w.browserClient(replica)
	if err != nil {
		return Response{}, err
	}
	var headers []sdk.Header
	if origin != "" {
		headers = append(headers, sdk.Header{Name: "Origin", Value: origin})
	}
	resp, err := c.Do(ctx, method, path, body, headers...)
	if resp == nil {
		return Response{}, err
	}
	return w.record(
		Response{Status: resp.Status, Header: resp.Header, Body: resp.Body, Err: err},
	), nil
}

// Resolve replaces each {name} in s with the scenario's ID of name, each
// {n × c} with the character c n times and each {n labels} with n
// distinct JSON strings, comma-separated, for bodies too long to write out.
func (w *World) Resolve(s string) string {
	for name, id := range w.IDs {
		s = strings.ReplaceAll(s, "{"+name+"}", id)
	}
	s = repeated.ReplaceAllStringFunc(s, func(m string) string {
		g := repeated.FindStringSubmatch(m)
		n, _ := strconv.Atoi(g[1])
		return strings.Repeat(g[2], n)
	})
	return labelList.ReplaceAllStringFunc(s, func(m string) string {
		n, _ := strconv.Atoi(labelList.FindStringSubmatch(m)[1])
		labels := make([]string, n)
		for i := range labels {
			labels[i] = fmt.Sprintf(`"label-%d"`, i)
		}
		return strings.Join(labels, ",")
	})
}

var (
	repeated  = regexp.MustCompile(`\{(\d+) × (.)\}`)
	labelList = regexp.MustCompile(`\{(\d+) labels\}`)
)

// SlugList is want's slugs, comma-separated, with each {name} resolved.
func (w *World) SlugList(want string) []string {
	out := []string{}
	if want = w.Resolve(want); want != "" {
		for slug := range strings.SplitSeq(want, ",") {
			out = append(out, strings.TrimSpace(slug))
		}
	}
	return out
}

// SendAs sends the request through the browser, as a write from the
// public URL's page, or with the named token, after resolving path and
// raw, and keeps the answer's body under its method and path.
func (w *World) SendAs(ctx context.Context, who, method, path, raw, replica string) error {
	path, raw = w.Resolve(path), w.Resolve(raw)
	var (
		r   Response
		err error
	)
	if who == "the browser" {
		r, err = w.BrowserDo(ctx, replica, method, path, PublicURL, Body(raw))
	} else {
		name := strings.TrimSuffix(strings.TrimPrefix(who, `token "`), `"`)
		r, err = w.Do(ctx, replica, name, method, path, Body(raw))
	}
	if err != nil && r.Status == 0 {
		return err
	}
	key := method + " " + path
	w.Answers[key] = append(w.Answers[key], r.Body)
	return nil
}

// Body is a step's JSON body, or none.
func Body(raw string) any {
	if raw == "" {
		return nil
	}
	return json.RawMessage(raw)
}

// Answered checks that r is a 200 whose body is want as JSON.
func Answered(r Response, what, want string) error {
	var got, exp any
	if err := json.Unmarshal([]byte(want), &exp); err != nil {
		return fmt.Errorf("%w: the step's JSON %q: %w", ErrStep, want, err)
	}
	if r.Status != 200 || json.Unmarshal(r.Body, &got) != nil || !reflect.DeepEqual(got, exp) {
		return fmt.Errorf("%w: %s answered %d %s, want 200 %s", ErrStep, what, r.Status,
			strings.TrimSpace(string(r.Body)), want)
	}
	return nil
}

// IsProblem checks that resp is a problem with status code and, when slug
// is not empty, of that type.
func IsProblem(resp Response, what, code, slug string) error {
	if strconv.Itoa(resp.Status) != code {
		return fmt.Errorf(
			"%w: %s answered %d, want %s: %s",
			ErrStep,
			what,
			resp.Status,
			code,
			resp.Body,
		)
	}
	p, ok := sdk.AsProblem(resp.Err)
	if !ok || p.Status != resp.Status {
		return fmt.Errorf("%w: %s answered no problem with its status: %w: %s",
			ErrStep, what, resp.Err, resp.Body)
	}
	if slug != "" && p.Slug() != slug {
		return fmt.Errorf("%w: %s answered type %q, want %q", ErrStep, what, p.Type, slug)
	}
	return nil
}

// Literal quotes s for SQL.
func Literal(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Row scans the one row query answers on the scenario's database, with
// args, into dest.
func (w *World) Row(ctx context.Context, query string, args []any, dest ...any) error {
	db, err := w.Database(ctx)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, db.DSN)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(context.WithoutCancel(ctx))
	if err := conn.QueryRow(ctx, query, args...).Scan(dest...); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrStep, query, err)
	}
	return nil
}

// Query runs sql on the scenario's database and returns psql's output.
func (w *World) Query(ctx context.Context, sql string) (string, error) {
	db, err := w.Database(ctx)
	if err != nil {
		return "", err
	}
	return db.Query(ctx, sql)
}

// Number runs sql, which selects one number, on the scenario's database.
func (w *World) Number(ctx context.Context, sql string) (int, error) {
	out, err := w.Query(ctx, sql)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("%w: %q from %s", ErrStep, out, sql)
	}
	return n, nil
}

// Start runs one more server with the rate limit settings rateLimit.
func (w *World) Start(ctx context.Context, rateLimit map[string]any) error {
	_, err := w.StartServer(ctx, map[string]any{"rate_limit": rateLimit})
	return err
}

// TwoReplicas starts the scenario's two servers, A and B, with the default
// budgets.
func (w *World) TwoReplicas(ctx context.Context) error {
	for range 2 {
		if err := w.Start(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}

// TwoInstances starts two all instances with a short lease and fast
// polling, logging at debug so each task's run is a line.
func (w *World) TwoInstances(ctx context.Context, lease int) error {
	db, err := w.Database(ctx)
	if err != nil {
		return err
	}
	config, err := yaml.Marshal(map[string]any{
		"server": map[string]any{
			"listen":       "127.0.0.1:0",
			"admin_listen": "127.0.0.1:0",
			"shutdown":     map[string]any{"ready_delay": "0s"},
			"public_url":   PublicURL,
		},
		"database":    map[string]any{"url": db.DSN},
		"secrets":     SecretsWith(nil),
		"objectstore": objectStore(w.Objects()),
		"log":         map[string]any{"level": "debug"},
		"queue": map[string]any{
			"lease":          fmt.Sprintf("%ds", lease),
			"poll_interval":  "100ms",
			"scheduler_tick": "100ms",
		},
	})
	if err != nil {
		return fmt.Errorf("instance config: %w", err)
	}
	if auth := w.Settings["auth"]; auth != nil {
		if config, err = withSection(config, "auth", auth); err != nil {
			return err
		}
	}
	for range 2 {
		if _, err := w.Launch(ctx, "all", config); err != nil {
			return err
		}
	}
	return nil
}

// Keyring is the keyring of the scenario's database, loaded as a server
// loads it, under the KEK the servers run with, with the pool it reads
// through, which the caller closes.
func (w *World) Keyring(
	ctx context.Context,
) (keys *crypt.Keyring, pool *pgxpool.Pool, err error) {
	db, err := w.Database(ctx)
	if err != nil {
		return nil, nil, err
	}
	var p kek.Provider
	if w.Vault != nil {
		p, err = vault.New(*w.Vault, vault.Options{})
	} else {
		p, err = kek.OpenLocal(kek.LocalConfig{Key: deps.KEK})
	}
	if err != nil {
		return nil, nil, fmt.Errorf("keyring: %w", err)
	}
	if pool, err = pgxpool.New(ctx, db.DSN); err != nil {
		return nil, nil, fmt.Errorf("keyring: %w", err)
	}
	if keys, err = crypt.Load(ctx, pool, p); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("keyring: %w", err)
	}
	return keys, pool, nil
}

// SealBody seals body as the servers seal a stored response under key.
func (w *World) SealBody(ctx context.Context, key string, body []byte) ([]byte, error) {
	keys, pool, err := w.Keyring(ctx)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	sealed, err := keys.SealColumn(ctx, idempotency.Body, []string{ScenarioCaller, key}, body)
	if err != nil {
		return nil, fmt.Errorf("seal a stored body: %w", err)
	}
	return sealed, nil
}

// ScenarioCaller is the caller every scenario's requests count against:
// the home tenant's key, whose token the scenario's clients hold.
var ScenarioCaller = Home.caller()
