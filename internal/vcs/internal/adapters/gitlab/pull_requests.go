package gitlab

import (
	"context"
	"fmt"
	"strconv"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// AuthorLogin resolves a creator identity without refreshing a request's head.
func (p *Provider) AuthorLogin(ctx context.Context, in port.Integration, id string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	user, err := strconv.ParseInt(id, 10, 64)
	if err != nil || user <= 0 {
		return "", fmt.Errorf("%w: invalid author identity", ErrCredential)
	}
	c, _, err := p.client(in)
	if err != nil {
		return "", err
	}
	result, response, err := c.Users.GetUser(user, gitlab.GetUsersOptions{}, gitlab.WithContext(ctx))
	if err != nil {
		return "", classify(response, err, "request author")
	}
	return result.Username, nil
}
