package core

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

// Identities supplies run receipt views with live existence without exposing
// source or coupling a historical receipt to a policy's later deletion.
func (s *Service) Identities(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID]policyapi.PolicyIdentity, error) {
	rows, err := s.deps.Store.ByIDs(ctx, acct, ids)
	if err != nil {
		return nil, fmt.Errorf("policies: read identities: %w", err)
	}
	out := make(map[kernel.ULID]policyapi.PolicyIdentity, len(rows))
	for id, p := range rows {
		out[id] = policyapi.PolicyIdentity{ID: id, Space: p.Space, Name: p.Name, Type: policyapi.Type(p.Type), Engine: string(p.Engine), Version: p.Version}
	}
	return out, nil
}
