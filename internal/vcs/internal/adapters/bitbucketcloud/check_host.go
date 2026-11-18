package bitbucketcloud

import (
	"fmt"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func (p *Provider) CheckHost(port.Settings) (string, error) {
	if p.host != nil {
		return "", p.host
	}
	origin, err := port.CheckOrigin(p.api)
	if err != nil {
		return "", fmt.Errorf("VCS check origin: %w", err)
	}
	return origin, nil
}
