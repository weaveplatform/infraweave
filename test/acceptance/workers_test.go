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
	"strconv"

	"github.com/cucumber/godog"
	"github.com/jackc/pgx/v5"
)

// workerSteps make a pool key's certificate signing request and check
// the pool token a pool answers against the pool CA and the pool's row.
//
// ref: decision 0008 § "The worker API"
type workerSteps struct{ o *orgSteps }

func (w *workerSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^a certificate signing request for an? (P-256|P-384|RSA) key is kept as "(\w+)"$`,
		w.request)
	sc.Step(
		`^the answer's pool token names the pool "(\w+)" and chains to the pool CA, its serial kept as "(\w+)"$`,
		w.token,
	)
	sc.Step(`^the pool "(\w+)" no longer has the serial kept as "(\w+)"$`, w.replaced)
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
