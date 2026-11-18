package azuredevops

import (
	"fmt"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func (*Provider) CheckHost(settings port.Settings) (string, error) {
	origin, err := orgOf(settings)
	if err != nil {
		return "", err
	}
	host, err := port.CheckOrigin(origin.api)
	if err != nil {
		return "", fmt.Errorf("VCS check origin: %w", err)
	}
	return host, nil
}
