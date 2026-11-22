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
	// ErrBusy refuses releasing controller ownership while workers or runs on
	// workers remain.
	ErrBusy = errors.New("workers: the pool has registered workers or runs on workers")
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
			if err := s.release(ctx, out, key); err != nil {
				return err
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

// release removes the hosting binding of out, whose row the caller's
// transaction locks, for a reset to key. A different key is required, so
// the old controller can no longer sign, and no worker or run on a worker
// may remain; otherwise ErrBusy.
func (s *Service) release(ctx context.Context, out Pool, key *x509.CertificateRequest) error {
	cert, err := x509.ParseCertificate(out.Certificate)
	if err != nil {
		return fmt.Errorf("workers: read pool certificate: %w", err)
	}
	if bytes.Equal(cert.RawSubjectPublicKeyInfo, key.RawSubjectPublicKeyInfo) {
		return &kernel.Invalid{
			Detail: "Releasing controller ownership requires a replacement pool key.",
			Errors: []kernel.FieldError{{Pointer: csrPointer, Reason: kernel.ReasonRule, Message: "Use a different pool key to revoke the old controller."}},
		}
	}
	busy, err := s.store.HasWorkers(ctx, out.ID)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	if busy {
		return ErrBusy
	}
	s.mu.RLock()
	assignments := s.poolAssignments
	s.mu.RUnlock()
	if assignments == nil {
		return fmt.Errorf("workers: pool assignment reader is not bound: %w", kernel.ErrUnavailable)
	}
	busy, err = assignments.ActivePool(ctx, out.ID)
	if err != nil {
		return err //nolint:wrapcheck // the runs domain names the read
	}
	if busy {
		return ErrBusy
	}
	return s.store.Unbind(ctx, out.ID) //nolint:wrapcheck // the store names the write
}
