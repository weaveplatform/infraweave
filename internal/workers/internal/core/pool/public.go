package pool

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var (
	// ErrPublicExists refuses creating a second installation pool.
	ErrPublicExists = errors.New("workers: the installation public pool already exists")
	// ErrPublicBusy refuses releasing controller ownership while workers or active assignments remain.
	ErrPublicBusy = errors.New("workers: the installation public pool has registered workers or active assignments")
	// ErrPublicCondition refuses credential rotation without a precondition.
	ErrPublicCondition = errors.New("workers: public pool reset requires a credential precondition")
)

// Public reads the installation singleton. Account roles never authorize this
// credential read; the operator API authenticates callers before invoking it.
func (s *Service) Public(ctx context.Context) (Pool, error) {
	return s.store.Public(ctx, false) //nolint:wrapcheck // the store names the read
}

// CreatePublic signs a caller-supplied key and persists one installation pool.
// The singleton lock and certificate serial allocation share the transaction.
func (s *Service) CreatePublic(ctx context.Context, req string) (Pool, error) {
	key, err := csr(req)
	if err != nil {
		return Pool{}, err
	}
	var out Pool
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		_, err := s.store.Public(ctx, true)
		if err == nil {
			return ErrPublicExists
		}
		if !errors.Is(err, ErrNotFound) {
			return err //nolint:wrapcheck // the store names the read
		}
		now := s.now()
		out = Pool{Public: true, ID: kernel.NewULID(), Name: "public", CreatedAt: now, UpdatedAt: now, Version: 1}
		if err := s.sign(ctx, &out, key); err != nil {
			return err
		}
		return s.store.CreatePublic(ctx, out) //nolint:wrapcheck // the store names the write
	})
	return out, err //nolint:wrapcheck // the transaction names each failure
}

// ResetPublic replaces the singleton certificate while preserving its pool ID.
// Ownership release additionally needs a different private key and no workers.
func (s *Service) ResetPublic(ctx context.Context, expect kernel.Expect, req string, releaseBinding bool) (Pool, error) {
	if !expect.Conditional() {
		return Pool{}, ErrPublicCondition
	}
	key, err := csr(req)
	if err != nil {
		return Pool{}, err
	}
	var out Pool
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		out, err = s.store.Public(ctx, true)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		if err := expect.Check(out.Version); err != nil {
			return err //nolint:wrapcheck // the shared precondition is answered as it is
		}
		if releaseBinding {
			cert, err := x509.ParseCertificate(out.Certificate)
			if err != nil {
				return fmt.Errorf("workers: read public pool certificate: %w", err)
			}
			if bytes.Equal(cert.RawSubjectPublicKeyInfo, key.RawSubjectPublicKeyInfo) {
				return &kernel.Invalid{
					Detail: "Releasing controller ownership requires a replacement pool key.",
					Errors: []kernel.FieldError{{Pointer: csrPointer, Reason: kernel.ReasonRule, Message: "Use a different pool key to revoke the old controller."}},
				}
			}
			busy, err := s.store.PublicWorkers(ctx, out.ID)
			if err != nil {
				return err //nolint:wrapcheck // the store names the read
			}
			if busy {
				return ErrPublicBusy
			}
			s.mu.RLock()
			assignments := s.publicAssignments
			s.mu.RUnlock()
			if assignments == nil {
				return fmt.Errorf("workers: public assignment reader is not bound: %w", kernel.ErrUnavailable)
			}
			busy, err = assignments.ActivePublicPool(ctx, out.ID)
			if err != nil {
				return err //nolint:wrapcheck // the runs domain names the read
			}
			if busy {
				return ErrPublicBusy
			}
		}
		if err := s.sign(ctx, &out, key); err != nil {
			return err
		}
		out.UpdatedAt = s.now()
		out.Version++
		return s.store.WritePublic(ctx, out) //nolint:wrapcheck // the store names the write
	})
	return out, err //nolint:wrapcheck // the transaction names each failure
}
