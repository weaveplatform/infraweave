package vcslab

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v93/github"
)

type appClient struct{ client *github.Client }

// NewAppHooks reaches only the dedicated GitHub App's hook configuration with
// its App JWT. This verification client does not expand product purpose routes.
func NewAppHooks(appID string, privateKey []byte) (AppHooks, error) {
	id, err := strconv.ParseInt(appID, 10, 64)
	if err != nil || id <= 0 {
		return nil, ErrAppKey
	}
	transport, err := ghinstallation.NewAppsTransport(http.DefaultTransport, id, privateKey)
	if err != nil {
		return nil, ErrAppKey
	}
	client, err := github.NewClient(github.WithHTTPClient(&http.Client{Transport: transport, Timeout: 30 * time.Second}))
	if err != nil {
		return nil, ErrAppKey
	}
	return &appClient{client: client}, nil
}

func (c *appClient) Get(ctx context.Context) (HookConfig, error) {
	v, _, err := c.client.Apps.GetHookConfig(ctx)
	if err != nil || v == nil || v.URL == nil || v.ContentType == nil || v.InsecureSSL == nil {
		return HookConfig{}, fmt.Errorf("%w: read App hook", ErrHook)
	}
	return HookConfig{URL: v.GetURL(), ContentType: v.GetContentType(), InsecureSSL: v.GetInsecureSSL()}, nil
}

func (c *appClient) Patch(ctx context.Context, config HookConfig) error {
	// Deliberately construct a fresh request. A GET secret is masked, and
	// copying it would replace the established secret with that mask.
	_, _, err := c.client.Apps.UpdateHookConfig(ctx, github.HookConfig{
		URL: &config.URL, ContentType: &config.ContentType, InsecureSSL: &config.InsecureSSL,
	})
	if err != nil {
		return fmt.Errorf("%w: update App hook", ErrHook)
	}
	return nil
}
