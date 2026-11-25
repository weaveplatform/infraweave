package worker

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/x509"
	"errors"
	"fmt"
	"slices"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

// RunSpec is a run's specification as the worker API answers it: the
// runs domain's Spec, with the account's default runner image for
// private pools when neither the run nor its stack names one, and Sealed,
// the run's secrets in the pool envelope.
type RunSpec struct {
	workersapi.Spec
	Sealed []byte
}

// Spec answers the specification of run to the bearer b of a run token:
// ErrUnauthenticated when the token is another run's, and
// workersapi.ErrNotOnWorker when the run is not on a worker or its pool is
// gone. The secrets are sealed to the pool's current certificate's key
// for the run's state version, so only a launcher holding the pool's
// private key opens them, and only for this run in this state. The in-run
// API token and artifact key the runs domain makes commit with the
// answer, and a failure rolls them back.
//
// ref: decision 0008 § "The run specification and its secrets"
func (s *Service) Spec(ctx context.Context, b accessapi.Bearer, run kernel.ULID) (RunSpec, error) {
	if b.ID != run {
		return RunSpec{}, refuse("run %s's token asks for run %s's specification", b.ID, run)
	}
	if s.scheduler == nil {
		return RunSpec{}, workersapi.ErrNotOnWorker
	}
	var out RunSpec
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		h, err := s.scheduler.Handover(ctx, run)
		if err != nil {
			return err //nolint:wrapcheck // the runs domain names the handover
		}
		var p pool.Pool
		if h.Public {
			p, err = s.store.Public(ctx, false)
			if err == nil && p.ID != h.Pool {
				return workersapi.ErrNotOnWorker
			}
		} else {
			p, err = s.store.Get(ctx, h.Account, h.Pool, false)
		}
		if errors.Is(err, pool.ErrNotFound) {
			return workersapi.ErrNotOnWorker
		}
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		key, err := poolKey(p.Certificate)
		if err != nil {
			return err
		}
		out = RunSpec{Spec: h.Spec}
		if h.Public {
			out.PublicPool = &workersapi.PublicPoolPolicy{AllowedRegistries: slices.Clone(s.publicRegistries)}
		}
		if out.RunnerImage == nil {
			images, err := s.store.RunnerImages(ctx, h.Account)
			if err != nil {
				return err //nolint:wrapcheck // the store names the read
			}
			out.RunnerImage = images.Private
			if h.Public {
				out.RunnerImage = images.Public
			}
		}
		out.Sealed, err = seal.SealEnvelope(key, run.String(), h.Spec.Run.StateVersion,
			h.Secrets.Reveal())
		return err //nolint:wrapcheck // the seal package names the failure
	})
	if err != nil {
		return RunSpec{}, err //nolint:wrapcheck // named above
	}
	return out, nil
}

// poolKey is the P-256 key a pool certificate's DER certifies, as ECDH
// takes it. Pools are created and reset from P-256 keys alone, so another
// key is a corrupt row.
func poolKey(der []byte) (*ecdh.PublicKey, error) {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("workers: the pool's certificate: %w", err)
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, seal.ErrPoolKey
	}
	key, err := pub.ECDH()
	if err != nil {
		return nil, fmt.Errorf("workers: the pool's key: %w", err)
	}
	return key, nil
}

// FederationToken reissues, to the bearer b of a run token, the OIDC
// federation token of the phase run is in: ErrUnauthenticated when the
// token is another run's, and workersapi.ErrNotOnWorker before
// BindScheduler. The runs domain answers the run's state, and the
// features domain the account's tier, as workersapi.Scheduler says.
//
// ref: decision 0006 § "OIDC federation token"
func (s *Service) FederationToken(ctx context.Context, b accessapi.Bearer, run kernel.ULID) (workersapi.FederationToken, error) {
	if b.ID != run {
		return workersapi.FederationToken{}, refuse("run %s's token asks for run %s's federation token", b.ID, run)
	}
	if s.scheduler == nil {
		return workersapi.FederationToken{}, workersapi.ErrNotOnWorker
	}
	var out workersapi.FederationToken
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		out, err = s.scheduler.FederationToken(ctx, run)
		return err //nolint:wrapcheck // the runs domain names its refusal
	})
	return out, err //nolint:wrapcheck // named above
}
