package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/sideband"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

func azureCloneRequired(raw string, err error) bool {
	u, parseErr := url.Parse(raw)
	if parseErr != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host != "dev.azure.com" && !strings.HasSuffix(host, ".visualstudio.com") {
		return false
	}
	// go-git's UnexpectedError keeps its cause in Err without Unwrap.
	var unexpected *plumbing.UnexpectedError
	if errors.As(err, &unexpected) {
		err = unexpected.Err
	}
	var response *githttp.Err
	return errors.As(err, &response) && response.Response != nil && response.StatusCode() == http.StatusBadRequest
}

// Azure requires multi_ack, which go-git's incremental fetch cannot handle.
// This workspace is newly initialized: requesting every branch with no haves
// transfers a complete pack and requires no multi-round ACK negotiation. Keep
// the capability on this session only; changing UnsupportedCapabilities would
// alter concurrent fetches from every other provider.
func cloneAzure(ctx context.Context, repo *git.Repository, raw string, opts *git.FetchOptions) error {
	ep, err := transport.NewEndpoint(raw)
	if err != nil {
		return fmt.Errorf("clone Azure endpoint: %w", err)
	}
	ep.CaBundle = opts.CABundle
	session, err := githttp.NewClient(nil).NewUploadPackSession(ep, opts.Auth)
	if err != nil {
		return fmt.Errorf("clone Azure session: %w", err)
	}
	defer session.Close()
	refs, err := session.AdvertisedReferencesContext(ctx)
	if err != nil {
		return fmt.Errorf("clone Azure references: %w", err)
	}
	req := packp.NewUploadPackRequestFromCapabilities(refs.Capabilities)
	if err := req.Capabilities.Set(capability.MultiACK); err != nil {
		return fmt.Errorf("clone Azure capability: %w", err)
	}
	seen := make(map[plumbing.Hash]bool)
	for name, hash := range refs.References {
		if strings.HasPrefix(name, "refs/heads/") && !seen[hash] {
			req.Wants = append(req.Wants, hash)
			seen[hash] = true
		}
	}
	response, err := session.UploadPack(ctx, req)
	if err != nil {
		return fmt.Errorf("clone Azure pack: %w", err)
	}
	defer response.Close()
	var pack io.Reader = response
	if req.Capabilities.Supports(capability.Sideband64k) {
		pack = sideband.NewDemuxer(sideband.Sideband64k, response)
	} else if req.Capabilities.Supports(capability.Sideband) {
		pack = sideband.NewDemuxer(sideband.Sideband, response)
	}
	if err := packfile.UpdateObjectStorage(repo.Storer, pack); err != nil {
		return fmt.Errorf("clone Azure objects: %w", err)
	}
	return nil
}
