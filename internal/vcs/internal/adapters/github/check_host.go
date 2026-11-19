package github

import (
	"fmt"
	"strconv"

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
	id, err := strconv.ParseInt(settings.AppID, 10, 64)
	if err != nil || id <= 0 {
		return "", port.ErrCheckInput
	}
	return origin + "/app/" + strconv.FormatInt(id, 10), nil
}
