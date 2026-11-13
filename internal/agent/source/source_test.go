package source

import (
	"encoding/pem"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/util"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// The credential the test server takes.
const (
	user  = "x-access-token"
	token = "ghs_secretsecretsecret"
)

// repo is a repository of two commits: first, with main.tf and
// infra/net.tf, and second, its child, which changes main.tf.
type repo struct {
	s             storer.Storer
	first, second string
}

func newRepo(t *testing.T) repo {
	t.Helper()
	s := memory.NewStorage()
	fs := memfs.New()
	r, err := git.Init(s, fs)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	commit := func(files map[string]string) string {
		for name, body := range files {
			if err := util.WriteFile(fs, name, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := wt.Add(name); err != nil {
				t.Fatal(err)
			}
		}
		h, err := wt.Commit("commit", &git.CommitOptions{Author: &object.Signature{
			Name: "a", Email: "a@example.com", When: time.Unix(1_700_000_000, 0),
		}})
		if err != nil {
			t.Fatal(err)
		}
		return h.String()
	}
	first := commit(map[string]string{"main.tf": "one\n", "infra/net.tf": "net\n"})
	second := commit(map[string]string{"main.tf": "two\n"})
	return repo{s: s, first: first, second: second}
}

// gitServer serves one repository over Git's smart HTTP protocol at
// /repo.git, taking the test credential when private is set, and serving
// a commit by its hash unless byHash is false. It keeps each request's
// Authorization header, and the depth of each upload-pack request.
type gitServer struct {
	srv     *httptest.Server
	private bool
	byHash  bool
	mu      sync.Mutex
	auths   []string
	depths  []packp.Depth
}

type oneRepo struct{ s storer.Storer }

func (l oneRepo) Load(*transport.Endpoint) (storer.Storer, error) { return l.s, nil }

func newGitServer(t *testing.T, s storer.Storer, private bool) *gitServer {
	t.Helper()
	g := &gitServer{private: private, byHash: true}
	up := server.NewServer(oneRepo{s})
	ep, err := transport.NewEndpoint("/repo.git")
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			g.mu.Lock()
			g.auths = append(g.auths, req.Header.Get("Authorization"))
			g.mu.Unlock()
			if u, p, _ := req.BasicAuth(); g.private && (u != user || p != token) {
				w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Get("/repo.git/info/refs", func(w http.ResponseWriter, req *http.Request) {
		sess, err := up.NewUploadPackSession(ep, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ar, err := sess.AdvertisedReferencesContext(req.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ar.Prefix = [][]byte{[]byte("# service=git-upload-pack"), pktline.Flush}
		if g.byHash {
			_ = ar.Capabilities.Set(capability.AllowReachableSHA1InWant)
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		_ = ar.Encode(w)
	})
	r.Post("/repo.git/git-upload-pack", func(w http.ResponseWriter, req *http.Request) {
		upr := packp.NewUploadPackRequest()
		if err := upr.Decode(req.Body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		g.mu.Lock()
		g.depths = append(g.depths, upr.Depth)
		g.mu.Unlock()
		sess, err := up.NewUploadPackSession(ep, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp, err := sess.UploadPack(req.Context(), upr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
		_ = resp.Encode(w)
	})
	g.srv = httptest.NewServer(r)
	t.Cleanup(g.srv.Close)
	return g
}

func (g *gitServer) seen() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.auths...)
}

// fetcher is a Fetcher whose Git fetches ask for whole histories, as the
// test server serves no shallow fetch.
func fetcher() *Fetcher {
	f := New(http.DefaultClient, nil)
	f.depth = 0
	return f
}

func gitSpec(url, commit string, root *string) wire.RunSpec {
	var spec wire.RunSpec
	spec.Source = wire.RunSpecSource{Kind: wire.Git, Url: url}
	spec.Commit.Hash = commit
	spec.Stack.ProjectRoot = root
	return spec
}

// holdsNo checks that no file under dir holds secret.
func holdsNo(t *testing.T, dir, secret string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
			return err
		}
		b, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(b), secret) {
			t.Errorf("%s holds the credential", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestUnitFetchGit checks that a commit other than the branch's head is
// checked out from a private repository with the credential, which
// reaches the server and no file of the workspace; and that a wrong
// credential, a missing commit, an unreachable host, a bad hash and a
// project root that is not a directory each fail with the step named and
// no credential.
func TestUnitFetchGit(t *testing.T) {
	ctx := t.Context()
	r := newRepo(t)
	g := newGitServer(t, r.s, true)
	url := g.srv.URL + "/repo.git"
	cred := &wire.RunSpecSourceCredential{Username: user, Token: token}
	root := "infra"

	dir := filepath.Join(t.TempDir(), "ws")
	if err := fetcher().Fetch(ctx, dir, gitSpec(url, r.first, &root), cred); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"main.tf": "one\n", "infra/net.tf": "net\n"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != want {
			t.Errorf("%s: %q, %v", name, b, err)
		}
	}
	head, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
	if err != nil || strings.TrimSpace(string(head)) != r.first {
		t.Errorf("HEAD: %q, %v", head, err)
	}
	holdsNo(t, dir, token)
	holdsNo(t, dir, g.srv.URL)
	if auths := g.seen(); len(auths) == 0 || !strings.HasPrefix(auths[len(auths)-1], "Basic ") {
		t.Errorf("the credential did not reach the server: %q", auths)
	}

	host := strings.TrimPrefix(g.srv.URL, "http://")
	for _, c := range []struct {
		name string
		spec wire.RunSpec
		cred *wire.RunSpecSourceCredential
		err  error
		note string
	}{
		{
			"a wrong credential", gitSpec(url, r.second, nil),
			&wire.RunSpecSourceCredential{Username: user, Token: "ghs_wrong"}, transport.ErrAuthenticationRequired,
			"fetch commit " + r.second + " from " + host + ": authentication required",
		},
		{
			"no credential", gitSpec(url, r.second, nil), nil, transport.ErrAuthenticationRequired,
			"fetch commit " + r.second + " from " + host,
		},
		{
			"a missing commit", gitSpec(url, strings.Repeat("ab", 20), nil), cred, nil,
			"fetch commit " + strings.Repeat("ab", 20) + " from " + host,
		},
		{
			"an unreachable host", gitSpec("http://127.0.0.1:1/repo.git", r.second, nil), cred, nil,
			"fetch commit " + r.second + " from 127.0.0.1:1",
		},
		{"no hash", gitSpec(url, "main", nil), cred, ErrCommit, `not a commit hash: "main"`},
		{
			"a project root that is not there", gitSpec(url, r.second, ptr("modules")), cred,
			ErrProjectRoot, "the project root is not a directory of the workspace: modules",
		},
		{
			"a project root outside", gitSpec(url, r.second, ptr("../..")), cred, ErrProjectRoot,
			"../..",
		},
		{
			"a project root that is a file", gitSpec(url, r.second, ptr("main.tf")), cred,
			ErrProjectRoot, "main.tf",
		},
		{"a file URL", gitSpec("file:///etc", r.second, nil), cred, ErrURL, ""},
		{
			"a URL with a user", gitSpec("https://u:p@example.com/r.git", r.second, nil), cred, ErrURL,
			"",
		},
		{
			"another kind",
			wire.RunSpec{Source: wire.RunSpecSource{Kind: "svn", Url: url}},
			cred,
			ErrKind, `"svn"`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := fetcher().Fetch(ctx, filepath.Join(t.TempDir(), "ws"), c.spec, c.cred)
			if err == nil || c.err != nil && !errors.Is(err, c.err) ||
				!strings.Contains(err.Error(), c.note) || strings.Contains(err.Error(), token) {
				t.Fatalf("got %v, want %v with %q", err, c.err, c.note)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

// TestUnitFetchBranches checks that a commit is checked out from a server
// that serves no commit by its hash, from the branches it serves, and
// that a commit on none of them fails its checkout.
func TestUnitFetchBranches(t *testing.T) {
	r := newRepo(t)
	g := newGitServer(t, r.s, false)
	g.byHash = false
	url := g.srv.URL + "/repo.git"
	dir := filepath.Join(t.TempDir(), "ws")
	if err := fetcher().Fetch(t.Context(), dir, gitSpec(url, r.first, nil), nil); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "main.tf")); err != nil || string(b) != "one\n" {
		t.Fatalf("main.tf: %q, %v", b, err)
	}
	err := fetcher().Fetch(t.Context(), filepath.Join(t.TempDir(), "ws"),
		gitSpec(url, strings.Repeat("ab", 20), nil), nil)
	if err == nil || !strings.Contains(err.Error(), ": check out: ") {
		t.Fatalf("a commit on no branch: %v", err)
	}
}

// TestUnitFetchShallow checks that a real fetch asks for the commit alone,
// a depth of one, which the test server refuses as it serves no shallow
// fetch.
func TestUnitFetchShallow(t *testing.T) {
	r := newRepo(t)
	g := newGitServer(t, r.s, false)
	err := New(http.DefaultClient, nil).Fetch(t.Context(), filepath.Join(t.TempDir(), "ws"),
		gitSpec(g.srv.URL+"/repo.git", r.second, nil), nil)
	g.mu.Lock()
	defer g.mu.Unlock()
	if err == nil || len(g.depths) != 1 || g.depths[0] != packp.DepthCommits(1) {
		t.Fatalf("a shallow fetch: %v, depths %v", err, g.depths)
	}
}

// TestUnitFetchTrusts checks that a Git server whose certificate a
// private CA issued is refused, and reached once the Fetcher trusts the
// CA.
func TestUnitFetchTrusts(t *testing.T) {
	r := newRepo(t)
	g := newGitServer(t, r.s, false)
	srv := httptest.NewTLSServer(g.srv.Config.Handler)
	t.Cleanup(srv.Close)
	spec := gitSpec(srv.URL+"/repo.git", r.second, nil)
	err := fetcher().Fetch(t.Context(), filepath.Join(t.TempDir(), "ws"), spec, nil)
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("an untrusted certificate: %v", err)
	}
	f := New(http.DefaultClient,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	f.depth = 0
	if err := f.Fetch(t.Context(), filepath.Join(t.TempDir(), "ws"), spec, nil); err != nil {
		t.Fatal(err)
	}
}

// TestUnitRedacted checks that an error that holds the credential has it
// replaced, and still unwraps.
func TestUnitRedacted(t *testing.T) {
	err := redacted{
		err:    errors.Join(ErrDownload, errors.New("token "+token+" refused")),
		secret: token,
	}
	if got := err.Error(); strings.Contains(got, token) || !strings.Contains(got, "[credential]") ||
		!errors.Is(err, ErrDownload) {
		t.Fatalf("redacted: %s", got)
	}
}
