package github

import (
	"context"
	"fmt"
	"strconv"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.InstallationReader = (*Provider)(nil)

// ReadInstallations uses the App JWT, so suspended installations need no token.
func (p *Provider) ReadInstallations(ctx context.Context, in port.Integration) ([]port.InstallationState, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	app, err := p.app(in)
	if err != nil {
		return nil, err
	}
	list, err := installations(ctx, client(app.host, app.tr))
	if err != nil {
		return nil, err
	}
	out := make([]port.InstallationState, 0, len(list))
	for _, native := range list {
		if native.GetID() <= 0 || native.GetAccount().GetID() <= 0 || native.GetAccount().GetLogin() == "" {
			return nil, fmt.Errorf("%w: github: missing installation identity", kernel.ErrUnavailable)
		}
		state := "active"
		if native.SuspendedAt != nil && !native.SuspendedAt.IsZero() {
			state = "suspended"
		}
		out = append(out, port.InstallationState{ID: strconv.FormatInt(native.GetID(), 10),
			Owner: vcsapi.Owner{ID: strconv.FormatInt(native.GetAccount().GetID(), 10), Login: native.GetAccount().GetLogin()}, State: state})
	}
	return out, nil
}
