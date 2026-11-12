package acceptance

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// workerSteps make a pool key's certificate signing request, check the
// pool token a pool answers against the pool CA and the pool's row,
// register and poll launchers through the worker API's generated client,
// and check a run's specification, opening its sealed part with the
// pool's key.
//
// ref: decision 0008 § "The worker API", § "The run specification and its
// secrets"
type workerSteps struct {
	o *orgSteps
	// keys are the pool keys by the name of their request.
	keys map[string]crypto.Signer
	// certs are the pool certificates by the name their serial is kept
	// as, each with its pool's ID.
	certs map[string]poolCert
	// registrations are each launcher's last signed registration.
	registrations map[string]string
	// held are each launcher's poll in flight.
	held map[string]chan response
}

type poolCert struct {
	cert *x509.Certificate
	pool string
}

func (w *workerSteps) register(sc *godog.ScenarioContext) {
	w.keys, w.certs = map[string]crypto.Signer{}, map[string]poolCert{}
	w.registrations, w.held = map[string]string{}, map[string]chan response{}
	sc.Step(`^a certificate signing request for an? (P-256|P-384|RSA) key is kept as "(\w+)"$`,
		w.request)
	sc.Step(
		`^the answer's pool token names the pool "(\w+)" and chains to the pool CA, its serial kept as "(\w+)"$`,
		w.token,
	)
	sc.Step(`^the pool "(\w+)" no longer has the serial kept as "(\w+)"$`, w.replaced)
	sc.Step(
		`^the launcher "(\w+)" registers with the key of "(\w+)" and the certificate "(\w+)"(?: as the worker "(\w+)")? through ([AB])$`,
		w.registerLauncher,
	)
	sc.Step(`^the launcher "(\w+)" sends its registration again through ([AB])$`, w.replay)
	sc.Step(`^the launcher "(\w+)" holds a poll with '([^']*)' through ([AB])$`, w.hold)
	sc.Step(`^the held poll of "(\w+)" answers within (\d+) seconds$`, w.answered)
	sc.Step(`^the answer lists the workers "([^"]*)"$`, w.lists)
	sc.Step(`^the answer assigns the run "(\w+)", its run token kept as "(\w+)"$`, w.assigned)
	sc.Step(`^the specification's variable "(\w+)" is "([^"]+)"$`, w.variable)
	sc.Step(`^the specification's variable "(\w+)" is write-only$`,
		func(name string) error { return w.variable(name, "") })
	sc.Step(
		`^the specification's sealed part opens with the key of "(\w+)" to the secret "(\w+)" as "([^"]*)"$`,
		w.sealed,
	)
}

// request keeps base64 of the PEM of a certificate signing request for a
// new key of kind as name, for a request body to name as {name}.
func (w *workerSteps) request(kind, name string) error {
	var key crypto.Signer
	var err error
	switch kind {
	case "P-256":
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "P-384":
		key, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	default:
		key, err = rsa.GenerateKey(rand.Reader, 2048)
	}
	if err != nil {
		return fmt.Errorf("a %s key: %w", kind, err)
	}
	w.keys[name] = key
	der, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: "acceptance"}}, key)
	if err != nil {
		return fmt.Errorf("a %s key's request: %w", kind, err)
	}
	w.o.ids[name] = base64.StdEncoding.EncodeToString(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
	return nil
}

// token decodes the last answer's config, a pool token, checks that it
// names the pool kept as pool, that its certificate chains to the pool CA
// the database keeps and carries the pool's serial, and keeps the serial
// as name.
func (w *workerSteps) token(ctx context.Context, pool, name string) error {
	r := w.o.a.s.last()
	var v struct {
		Config string `json:"config"`
	}
	if json.Unmarshal(r.body, &v) != nil || v.Config == "" {
		return fmt.Errorf("%w: no pool token in %d %s", errStep, r.status, r.body)
	}
	raw, err := base64.StdEncoding.DecodeString(v.Config)
	if err != nil {
		return fmt.Errorf("%w: the pool token is not base64: %w", errStep, err)
	}
	var t struct {
		Version     int    `json:"version"`
		Endpoint    string `json:"endpoint"`
		Pool        string `json:"pool"`
		Certificate string `json:"certificate"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return fmt.Errorf("%w: the pool token is not JSON: %w", errStep, err)
	}
	if t.Version != 1 || t.Endpoint == "" || t.Pool != w.o.ids[pool] {
		return fmt.Errorf("%w: the pool token is %s", errStep, raw)
	}
	block, _ := pem.Decode([]byte(t.Certificate))
	if block == nil {
		return fmt.Errorf("%w: the pool token carries no certificate", errStep)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("%w: the pool certificate: %w", errStep, err)
	}
	var ca []byte
	var serial int64
	if err := w.row(ctx, `SELECT c.certificate, p.serial FROM worker_pool_ca c, worker_pools p
		WHERE p.id = $1`, []any{uuidOf(t.Pool)}, &ca, &serial); err != nil {
		return err
	}
	root, err := x509.ParseCertificate(ca)
	if err != nil {
		return fmt.Errorf("%w: the pool CA: %w", errStep, err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		return fmt.Errorf(
			"%w: the pool certificate does not chain to the pool CA: %w",
			errStep,
			err,
		)
	}
	if !cert.SerialNumber.IsInt64() || cert.SerialNumber.Int64() != serial {
		return fmt.Errorf("%w: the certificate's serial is %s, the pool's %d", errStep,
			cert.SerialNumber, serial)
	}
	w.o.ids[name] = strconv.FormatInt(serial, 10)
	w.certs[name] = poolCert{cert: cert, pool: t.Pool}
	return nil
}

// replaced checks that the pool kept as pool has a serial other than the
// one kept as name, so a registration under the old certificate is
// refused.
func (w *workerSteps) replaced(ctx context.Context, pool, name string) error {
	var serial int64
	if err := w.row(ctx, `SELECT serial FROM worker_pools WHERE id = $1`,
		[]any{uuidOf(w.o.ids[pool])}, &serial); err != nil {
		return err
	}
	if strconv.FormatInt(serial, 10) == w.o.ids[name] {
		return fmt.Errorf("%w: the pool still has the serial %d", errStep, serial)
	}
	return nil
}

// row scans the one row query answers into dest.
func (w *workerSteps) row(ctx context.Context, query string, args []any, dest ...any) error {
	db, err := w.o.a.s.w.database(ctx)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, db.DSN)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(context.WithoutCancel(ctx))
	if err := conn.QueryRow(ctx, query, args...).Scan(dest...); err != nil {
		return fmt.Errorf("%w: %s: %w", errStep, query, err)
	}
	return nil
}

// wire is a client of the worker API through replica.
func (w *workerSteps) wire(replica string) (*wire.ClientWithResponses, error) {
	i := 0
	if replica == "B" {
		i = 1
	}
	c, err := wire.NewClientWithResponses("http://" + w.o.a.s.w.servers[i].API + "/worker/v1")
	if err != nil {
		return nil, fmt.Errorf("worker client: %w", err)
	}
	return c, nil
}

// registerLauncher signs a registration with the key of key under the
// certificate kept as cert, naming the worker kept as named when set, and
// sends it through replica. A registration answered 200 keeps its worker
// as launcher and its session token as the token launcher.
func (w *workerSteps) registerLauncher(
	ctx context.Context,
	launcher, key, cert, named, replica string,
) error {
	pc, ok := w.certs[cert]
	if !ok || w.keys[key] == nil {
		return fmt.Errorf("%w: no certificate %q or key %q", errStep, cert, key)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: w.keys[key]},
		(&jose.SignerOptions{}).WithHeader("x5c",
			[]string{base64.StdEncoding.EncodeToString(pc.cert.Raw)}))
	if err != nil {
		return fmt.Errorf("signer: %w", err)
	}
	payload := map[string]any{
		"pool": pc.pool, "iat": time.Now().Unix(), "jti": kernel.NewULID().String(),
		"version": "1.0.0", "os": "linux", "arch": "amd64",
		"metadata": map[string]string{"host": launcher},
	}
	if named != "" {
		payload["worker"] = w.o.ids[named]
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("registration: %w", err)
	}
	obj, err := signer.Sign(b)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	raw, err := obj.CompactSerialize()
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	w.registrations[launcher] = raw
	return w.replay(ctx, launcher, replica)
}

// replay sends launcher's last registration through replica, with no
// token.
func (w *workerSteps) replay(ctx context.Context, launcher, replica string) error {
	r, err := w.o.a.do(ctx, replica, "", http.MethodPost, "/worker/v1/register",
		wire.WorkerRegisterJSONRequestBody{Registration: w.registrations[launcher]})
	if err != nil && r.status == 0 {
		return err
	}
	var reg wire.Registration
	if r.status == http.StatusOK {
		if err := json.Unmarshal(r.body, &reg); err != nil {
			return fmt.Errorf("%w: the registration answered %s", errStep, r.body)
		}
		w.o.ids[launcher] = reg.Worker
		w.o.a.tokens[launcher] = reg.SessionToken
	}
	return nil
}

// hold sends a poll of raw for launcher through replica, which the
// server may hold, and returns once the server has marked the worker seen
// by it, which a poll reporting busy shows.
func (w *workerSteps) hold(ctx context.Context, launcher, raw, replica string) error {
	c, err := w.wire(replica)
	if err != nil {
		return err
	}
	var in wire.WorkerPollJSONRequestBody
	if err := json.Unmarshal([]byte(raw), &in); err != nil || !in.Busy {
		return fmt.Errorf("%w: a held poll reports busy: %s", errStep, raw)
	}
	token := w.o.a.tokens[launcher]
	done := make(chan response, 1)
	w.held[launcher] = done
	go func() {
		resp, err := c.WorkerPollWithResponse(context.WithoutCancel(ctx), in,
			func(_ context.Context, r *http.Request) error {
				r.Header.Set("Authorization", "Bearer "+token)
				return nil
			})
		if err != nil {
			done <- response{err: err}
			return
		}
		done <- response{status: resp.StatusCode(), header: resp.HTTPResponse.Header, body: resp.Body}
	}()
	return Eventually(ctx, "the held poll's worker busy", func(ctx context.Context) (bool, error) {
		var busy bool
		err := w.row(ctx, `SELECT busy FROM workers WHERE id = $1`,
			[]any{uuidOf(w.o.ids[launcher])}, &busy)
		return busy, err
	})
}

// answered waits up to secs for launcher's held poll to answer, and
// makes its answer the last.
func (w *workerSteps) answered(launcher string, secs int) error {
	select {
	case r := <-w.held[launcher]:
		if r.status == 0 {
			return fmt.Errorf("%w: the held poll failed: %w", errStep, r.err)
		}
		w.o.a.s.sent = append(w.o.a.s.sent, r)
		return nil
	case <-time.After(time.Duration(secs) * time.Second):
		return fmt.Errorf(
			"%w: the held poll of %q did not answer within %ds",
			errStep,
			launcher,
			secs,
		)
	}
}

// lists checks the last answer, a page of workers, against the workers
// kept as want, comma-separated, in the page's order.
func (w *workerSteps) lists(want string) error {
	var page struct {
		Edges []struct {
			Node struct {
				ID string `json:"id"`
			} `json:"node"`
		} `json:"edges"`
	}
	r := w.o.a.s.last()
	if r.status != http.StatusOK || json.Unmarshal(r.body, &page) != nil {
		return fmt.Errorf("%w: answered %d %s", errStep, r.status, r.body)
	}
	var got, wanted []string
	for _, e := range page.Edges {
		got = append(got, e.Node.ID)
	}
	for name := range strings.SplitSeq(want, ",") {
		if name = strings.TrimSpace(name); name != "" {
			wanted = append(wanted, w.o.ids[name])
		}
	}
	if !slices.Equal(got, wanted) {
		return fmt.Errorf("%w: the workers are %v, want %v", errStep, got, wanted)
	}
	return nil
}
