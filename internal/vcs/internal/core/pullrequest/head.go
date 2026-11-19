package pullrequest

import (
	"context"
	"fmt"
	"strings"

	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func (s *Service) resolveHead(ctx context.Context, d intake.Delivery, source *vcsapi.Repository, prefix string) (string, error) {
	resolver := s.d.Heads[d.Provider]
	if tx.In(ctx) || resolver == nil || source == nil || source.ID == "" || source.Namespace == "" || source.Name == "" || !headPrefix(prefix) {
		return "", ErrHead
	}
	in, found, err := s.d.Integrations.Use(ctx, d.Account, d.Provider, &d.Integration)
	if err != nil {
		return "", fmt.Errorf("vcs: resolve request head integration: %w", err)
	}
	if !found {
		return "", ErrHead
	}
	head, err := resolver.ResolveRequestHead(ctx, in, *source, prefix)
	if err != nil {
		return "", fmt.Errorf("vcs: resolve immutable request head: %w", err)
	}
	if !port.ValidCommit(head) || !strings.HasPrefix(strings.ToLower(head), strings.ToLower(prefix)) {
		return "", ErrHead
	}
	return strings.ToLower(head), nil
}

// A provider capability receives only immutable hexadecimal candidates. The
// provider owns native prefix limits; the final result must be a full Git ID.
func headPrefix(ref string) bool {
	if len(ref) < 7 || len(ref) >= 64 || strings.Trim(ref, "0") == "" {
		return false
	}
	for _, c := range ref {
		if c < '0' || c > '9' {
			if c < 'a' || c > 'f' {
				if c < 'A' || c > 'F' {
					return false
				}
			}
		}
	}
	return true
}
