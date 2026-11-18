package gitlab

import (
	"fmt"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func (*Provider) CheckHost(settings port.Settings) (string, error) {
	h, err := hostOf(settings)
	if err != nil {
		return "", err
	}
	origin, err := port.CheckOrigin(h.api)
	if err != nil {
		return "", fmt.Errorf("VCS check origin: %w", err)
	}
	return origin, nil
}
