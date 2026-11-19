package github

import (
	"context"
	"strings"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// CanWrite reads the sender's effective repository permission through the App.
// Unknown/custom roles are refused unless GitHub reports their write-capable
// base role. The read shares the source client's route and timeout guards.
func (p *Provider) CanWrite(ctx context.Context, repo port.Repository, login string) (bool, error) {
	if login == "" || strings.ContainsAny(login, "/\\?#%\r\n") {
		return false, port.ErrCheckActionTarget
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, _, err := p.repo(ctx, repo)
	if err != nil {
		return false, err
	}
	permission, _, err := c.Repositories.GetPermissionLevel(ctx, repo.Namespace, repo.Name, login)
	if err != nil {
		return false, classify(err, "check action collaborator permission")
	}
	if permission == nil {
		return false, port.ErrCheckActionTarget
	}
	switch permission.GetPermission() {
	case "admin", "write":
		return true, nil
	default:
		return false, nil
	}
}

var _ port.CheckActions = (*Provider)(nil)
