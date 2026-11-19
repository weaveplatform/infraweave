package gitlab

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// GitLab creates a project token's bot membership synchronously, but queues
// its project authorization refresh. Until that finishes, Git HTTP rejects
// the otherwise valid token with 401. Hand over only a token that Git can
// use, within Mint's deadline, without borrowing the integration credential.
func (p *Provider) awaitRepositoryAccess(ctx context.Context, repo port.Repository, c port.Credential) error {
	client := &http.Client{
		Transport:     p.rt,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	for {
		ready, err := repositoryAccess(ctx, client, p.SourceURL(repo, ""), c)
		if err != nil || ready {
			return err
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("gitlab: wait for project token authorization: %w: %w", kernel.ErrUnavailable, ctx.Err())
		case <-timer.C:
		}
	}
}

func repositoryAccess(ctx context.Context, client *http.Client, cloneURL string, c port.Credential) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cloneURL+"/info/refs?service=git-upload-pack", nil)
	if err != nil {
		return false, fmt.Errorf("gitlab: project token repository URL: %w", err)
	}
	req.SetBasicAuth(c.Username, string(c.Token))
	// Protocol v2 advertises capabilities rather than an unbounded list of refs.
	req.Header.Set("Git-Protocol", "version=2")
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("gitlab: check project token authorization: %w: %w", kernel.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusUnauthorized {
		return false, nil
	}
	if resp.StatusCode == http.StatusOK &&
		resp.Header.Get("Content-Type") == "application/x-git-upload-pack-advertisement" {
		return true, nil
	}
	return false, fmt.Errorf("gitlab: project token authorization returned HTTP %d: %w", resp.StatusCode, ErrCredential)
}
