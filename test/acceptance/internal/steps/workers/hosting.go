package workers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cucumber/godog"
	"github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// hostingCluster and hostingNamespace scope every hosting owner a
// scenario names; the owner's name is its resource UID.
const (
	hostingCluster   = "acceptance"
	hostingNamespace = "workers"
)

func hostingOwner(name string) kernel.WorkerOwner {
	return kernel.WorkerOwner{ClusterID: hostingCluster, Namespace: hostingNamespace, ResourceUID: name}
}

// hosting registers the steps of a pool a controller hosts: signed bind
// and release proofs of a named owner, registrations of an owner's slot,
// and a managed pool's creation under an Idempotency-Key.
func (w *workerSteps) hosting(sc *godog.ScenarioContext) {
	sc.Step(`^the owner "(\w+)" (binds|releases) the pool of the certificate "(\w+)" with the key of "(\w+)" through ([AB])$`, w.ownership)
	sc.Step(`^the owner "(\w+)" (binds|releases) with a malformed proof through ([AB])$`, w.malformed)
	sc.Step(`^the launcher "(\w+)" registers as slot (\d+) of the owner "(\w+)" with the key of "(\w+)" and the certificate "(\w+)" through ([AB])$`,
		func(ctx context.Context, launcher string, slot int32, owner, key, cert, replica string) error {
			o := hostingOwner(owner)
			w.owner, w.slot = &o, &slot
			defer func() { w.owner, w.slot = nil, nil }()
			return w.registerLauncher(ctx, launcher, key, cert, "", replica)
		})
	sc.Step(`^the launcher "(\w+)" registers as slot (\d+) without an owner with the key of "(\w+)" and the certificate "(\w+)" through ([AB])$`,
		func(ctx context.Context, launcher string, slot int32, key, cert, replica string) error {
			w.slot = &slot
			defer func() { w.slot = nil }()
			return w.registerLauncher(ctx, launcher, key, cert, "", replica)
		})
	sc.Step(`^token "(\w+)" sends "([A-Z]+) ([^"]+)" with '([^']*)' and the Idempotency-Key "([^"]+)" through ([AB])$`,
		func(ctx context.Context, token, method, path, raw, key, replica string) error {
			r, err := w.w.Do(ctx, replica, token, method, w.w.Resolve(path), harness.Body(w.w.Resolve(raw)),
				sdk.Header{Name: "Idempotency-Key", Value: key})
			if err != nil && r.Status == 0 {
				return err
			}
			return nil
		})
}

// ownership signs a proof that the owner holds the pool of cert and sends
// it to bind or release that pool.
func (w *workerSteps) ownership(ctx context.Context, owner, verb, cert, key, replica string) error {
	pc, ok := w.certs[cert]
	if !ok || w.w.Keys[key] == nil {
		return fmt.Errorf("%w: no certificate %q or key %q", harness.ErrStep, cert, key)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: w.w.Keys[key]},
		(&jose.SignerOptions{}).WithHeader("x5c", []string{base64.StdEncoding.EncodeToString(pc.cert.Raw)}))
	if err != nil {
		return fmt.Errorf("signer: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"pool": pc.pool, "owner": hostingOwner(owner), "iat": time.Now().Unix(), "jti": kernel.NewULID().String(),
	})
	if err != nil {
		return fmt.Errorf("proof: %w", err)
	}
	obj, err := signer.Sign(payload)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	raw, err := obj.CompactSerialize()
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	return w.prove(ctx, verb, raw, replica)
}

func (w *workerSteps) malformed(ctx context.Context, _, verb, replica string) error {
	return w.prove(ctx, verb, "not.a.proof", replica)
}

func (w *workerSteps) prove(ctx context.Context, verb, proof, replica string) error {
	path := map[string]string{"binds": "/worker/v1/pools/bind", "releases": "/worker/v1/pools/release"}[verb]
	r, err := w.w.Do(ctx, replica, "", http.MethodPost, path, map[string]string{"proof": proof})
	if err != nil && r.Status == 0 {
		return err
	}
	return nil
}
