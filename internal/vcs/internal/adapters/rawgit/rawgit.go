package rawgit

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// errNotCommit is a ref naming neither a commit nor a tag of one.
var errNotCommit = errors.New("not a commit")

// Timeout bounds each call, the ref advertisement and the fetch together.
const Timeout = 30 * time.Second

// Options are what a deployment or a test changes.
type Options struct {
	// RootCAs are the certificate authorities trusted for a repository's
	// host; nil is the system's.
	RootCAs *x509.CertPool
}

// Source is port.Source over Git's smart HTTP protocol, for public
// repositories reached by their URL with no credential.
//
// ref: decision 0012
type Source struct {
	git transport.Transport
}

var _ port.Source = (*Source)(nil)

// New returns a Source that reaches repositories over https alone. go-git's
// transport refuses a redirect from https to http, and one past its limit,
// and follows a redirect only on the ref advertisement, so a fetch goes to
// the host that advertised the refs.
func New(o Options) *Source {
	tr := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the standard library's
	tr.TLSClientConfig = &tls.Config{RootCAs: o.RootCAs, MinVersion: tls.VersionTLS12}
	tr.ForceAttemptHTTP2 = true
	return &Source{git: githttp.NewClient(&http.Client{Transport: tr})}
}

// ResolveRef implements port.Source: a full commit SHA as it is, otherwise
// the branch ref names, then the tag, peeled to its commit.
func (s *Source) ResolveRef(
	ctx context.Context,
	repo port.Repository,
	ref string,
) (vcsapi.Commit, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	f, err := s.fetch(ctx, repo, ref)
	if err != nil {
		return vcsapi.Commit{}, err
	}
	c := f.commits[0]
	message, _, _ := strings.Cut(c.Message, "\n")
	return vcsapi.Commit{
		Hash:       c.Hash.String(),
		Tag:        f.tags[0],
		Message:    message,
		AuthorName: c.Author.Name,
		Timestamp:  c.Author.When.UTC(),
	}, nil
}

// ReadFile implements port.Source.
func (s *Source) ReadFile(
	ctx context.Context,
	repo port.Repository,
	ref, path string,
) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	f, err := s.fetch(ctx, repo, ref)
	if err != nil {
		return nil, err
	}
	// The fetch holds the commit's whole tree, so a path File cannot read
	// as a file, a directory among them, is not one the commit has.
	file, err := f.commits[0].File(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s at %s: %w", port.ErrNotFound, path, ref, err)
	}
	if file.Size > port.MaxFileSize {
		return nil, fmt.Errorf("%w: %s is %d bytes", port.ErrTooLarge, path, file.Size)
	}
	b, err := file.Contents()
	if err != nil {
		return nil, fmt.Errorf("rawgit: read %s at %s: %w", path, ref, err)
	}
	return []byte(b), nil
}

// AffectedFiles implements port.Source: every path added, removed or
// changed between the two commits' trees, sorted, a rename as both its
// paths.
func (s *Source) AffectedFiles(
	ctx context.Context,
	repo port.Repository,
	base, head string,
) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	f, err := s.fetch(ctx, repo, base, head)
	if err != nil {
		return nil, err
	}
	trees := make([]*object.Tree, 2)
	for i, c := range f.commits {
		if trees[i], err = c.Tree(); err != nil {
			return nil, fmt.Errorf("rawgit: tree of %s: %w", c.Hash, err)
		}
	}
	changes, err := object.DiffTreeWithOptions(ctx, trees[0], trees[1], nil)
	if err != nil {
		return nil, fmt.Errorf("rawgit: diff %s..%s: %w", base, head, err)
	}
	paths := make([]string, 0, len(changes))
	for _, ch := range changes {
		for _, p := range []string{ch.From.Name, ch.To.Name} {
			if p != "" {
				paths = append(paths, p)
			}
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths), nil
}

// SourceURL implements port.Source: the repository's own URL, which a
// worker fetches the commit from with no credential.
func (*Source) SourceURL(repo port.Repository, _ string) string { return repo.URL }

// fetched are the commits refs resolved to, in their order, with the tag
// each named, or "".
type fetched struct {
	commits []*object.Commit
	tags    []string
}

// fetch resolves each of refs on repo's advertised refs and fetches their
// commits into memory, one commit deep where the server allows it.
func (s *Source) fetch(ctx context.Context, repo port.Repository, refs ...string) (fetched, error) {
	ep, err := endpoint(repo.URL)
	if err != nil {
		return fetched{}, err
	}
	sess, err := s.git.NewUploadPackSession(ep, nil)
	if err != nil {
		return fetched{}, classify(repo, err)
	}
	defer sess.Close() //nolint:errcheck // an HTTP session holds nothing open
	ar, err := sess.AdvertisedReferencesContext(ctx)
	if err != nil {
		return fetched{}, classify(repo, err)
	}
	f := fetched{tags: make([]string, len(refs))}
	wants := make([]plumbing.Hash, len(refs))
	for i, ref := range refs {
		if wants[i], f.tags[i], err = resolve(ar, ref); err != nil {
			return fetched{}, err
		}
	}
	req := packp.NewUploadPackRequestFromCapabilities(ar.Capabilities)
	// The pack is read as it arrives, with no side band to demultiplex
	// and nothing held locally for a thin pack to refer to.
	req.Capabilities.Delete(capability.Sideband)
	req.Capabilities.Delete(capability.Sideband64k)
	req.Capabilities.Delete(capability.ThinPack)
	if ar.Capabilities.Supports(capability.Shallow) &&
		req.Capabilities.Set(capability.Shallow) == nil {
		req.Depth = packp.DepthCommits(1)
	}
	req.Wants = slices.Compact(slices.Clone(wants))
	store := memory.NewStorage()
	if err := upload(ctx, sess, req, store); err != nil {
		err = classify(repo, err)
		// A server refuses a want no ref of its reaches with an error
		// line, which go-git reports as text alone.
		if !errors.Is(err, kernel.ErrUnavailable) && !tips(ar, wants) {
			return fetched{}, fmt.Errorf("%w: %s on %s: %w",
				port.ErrNotFound, strings.Join(refs, ", "), repo.URL, err)
		}
		return fetched{}, err
	}
	for _, h := range wants {
		c, err := commitOf(store, h)
		if err != nil {
			return fetched{}, fmt.Errorf("%w: %s on %s: %w", port.ErrNotFound, h, repo.URL, err)
		}
		f.commits = append(f.commits, c)
	}
	return f, nil
}

// commitOf is the commit h names in store, through any tags, for a server
// that advertises a tag without its peeled commit.
func commitOf(store *memory.Storage, h plumbing.Hash) (*object.Commit, error) {
	for {
		obj, err := object.GetObject(store, h)
		if err != nil {
			return nil, err //nolint:wrapcheck // the caller names the ref
		}
		switch o := obj.(type) {
		case *object.Commit:
			return o, nil
		case *object.Tag:
			h = o.Target
		default:
			return nil, fmt.Errorf("%w: %s is a %s", errNotCommit, h, obj.Type())
		}
	}
}

// upload fetches req's pack from sess into store.
func upload(
	ctx context.Context,
	sess transport.UploadPackSession,
	req *packp.UploadPackRequest,
	store *memory.Storage,
) error {
	resp, err := sess.UploadPack(ctx, req)
	if err != nil {
		return err //nolint:wrapcheck // classify wraps it
	}
	defer resp.Close()                               //nolint:errcheck // the pack is read in full
	return packfile.UpdateObjectStorage(store, resp) //nolint:wrapcheck // classify wraps it
}

// tips reports whether every one of hashes is the tip of a ref ar
// advertises, or a tag's peeled commit, which a server serves as a tip.
func tips(ar *packp.AdvRefs, hashes []plumbing.Hash) bool {
	advertised := make(map[plumbing.Hash]bool, len(ar.References)+len(ar.Peeled))
	for _, m := range []map[string]plumbing.Hash{ar.References, ar.Peeled} {
		for _, h := range m {
			advertised[h] = true
		}
	}
	for _, h := range hashes {
		if !advertised[h] {
			return false
		}
	}
	return true
}

// resolve is the commit ref names among ar's refs, and the tag it names: a
// full SHA as it is, then refs/heads/ref, then refs/tags/ref, peeled.
func resolve(ar *packp.AdvRefs, ref string) (plumbing.Hash, string, error) {
	if isHash(ref) {
		return plumbing.NewHash(ref), "", nil
	}
	if h, ok := ar.References["refs/heads/"+ref]; ok {
		return h, "", nil
	}
	tag := "refs/tags/" + ref
	if h, ok := ar.Peeled[tag]; ok {
		return h, ref, nil
	}
	if h, ok := ar.References[tag]; ok {
		return h, ref, nil
	}
	return plumbing.ZeroHash, "", fmt.Errorf("%w: ref %s", port.ErrNotFound, ref)
}

// isHash reports whether ref is a full SHA-1 commit ID in lower-case hex,
// the form Git writes.
func isHash(ref string) bool {
	if len(ref) != 40 {
		return false
	}
	for _, r := range ref {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// endpoint is the endpoint of raw, an https URL with no credential, query
// or fragment.
func endpoint(raw string) (*transport.Endpoint, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: not a URL", port.ErrRepositoryURL)
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" ||
		u.Fragment != "" {
		return nil, fmt.Errorf("%w: %s is not an https URL without a credential, query or fragment",
			port.ErrRepositoryURL, u.Redacted())
	}
	ep, err := transport.NewEndpoint(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", port.ErrRepositoryURL, u.Redacted(), err)
	}
	return ep, nil
}

// classify maps a failure to reach repo to port's errors: a repository the
// server denies or lacks, as it answers for a private one, is
// port.ErrNotFound, and a host that fails to answer, or answers with an
// unexpected status or a redirect it refuses, is kernel.ErrUnavailable.
// Anything else, such as an answer that is not Git's, keeps its cause
// alone.
func classify(repo port.Repository, err error) error {
	if errors.Is(err, transport.ErrRepositoryNotFound) ||
		errors.Is(err, transport.ErrAuthenticationRequired) ||
		errors.Is(err, transport.ErrAuthorizationFailed) ||
		errors.Is(err, transport.ErrEmptyRemoteRepository) {
		return fmt.Errorf("%w: repository %s: %w", port.ErrNotFound, repo.URL, err)
	}
	var netErr net.Error
	var unexpected *plumbing.UnexpectedError
	if errors.As(err, &netErr) || errors.As(err, &unexpected) ||
		errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: rawgit: %s: %w", kernel.ErrUnavailable, repo.URL, err)
	}
	return fmt.Errorf("rawgit: %s: %w", repo.URL, err)
}
