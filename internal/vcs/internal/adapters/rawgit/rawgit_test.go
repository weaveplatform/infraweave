package rawgit

import (
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/util"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

// gitServer serves repositories over Git's smart HTTP protocol, as git
// http-backend does, with go-git's server, which fetches whole histories:
// it advertises no shallow capability. Unpeeled advertises each annotated
// tag without its peeled commit, as a server may.
type gitServer struct {
	repos    map[string]*memory.Storage
	unpeeled bool
}

func (g gitServer) routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/{repo}.git/info/refs", g.refs)
	r.Post("/{repo}.git/git-upload-pack", g.upload)
	return r
}

// session is an upload-pack session on the repository r's path names.
func (g gitServer) session(w http.ResponseWriter, r *http.Request) transport.UploadPackSession {
	st, ok := g.repos[chi.URLParam(r, "repo")]
	if !ok {
		http.NotFound(w, r)
		return nil
	}
	ep, err := transport.NewEndpoint("https://git.test/repo.git")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil
	}
	sess, err := server.NewServer(server.MapLoader{ep.String(): st}).NewUploadPackSession(ep, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil
	}
	return sess
}

// refs advertises the repository's refs, with each annotated tag's
// peeled commit, as Git does.
func (g gitServer) refs(w http.ResponseWriter, r *http.Request) {
	sess := g.session(w, r)
	if sess == nil {
		return
	}
	ar, err := sess.AdvertisedReferencesContext(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	st := g.repos[chi.URLParam(r, "repo")]
	for name, h := range ar.References {
		if tag, err := object.GetTag(st, h); err == nil && !g.unpeeled {
			ar.Peeled[name] = tag.Target
		}
	}
	ar.Prefix = [][]byte{[]byte("# service=git-upload-pack"), pktline.Flush}
	w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
	_ = ar.Encode(w)
}

// upload answers an upload request with its pack, or with an error line,
// as Git refuses a want it does not serve.
func (g gitServer) upload(w http.ResponseWriter, r *http.Request) {
	sess := g.session(w, r)
	if sess == nil {
		return
	}
	req := packp.NewUploadPackRequest()
	if err := req.Decode(r.Body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	resp, err := sess.UploadPack(r.Context(), req)
	if err != nil {
		_ = pktline.NewEncoder(w).EncodeString("ERR upload-pack: not our ref\n")
		return
	}
	defer resp.Close()
	_ = resp.Encode(w)
}

// repository is the contract's repository: main moves from first to base
// to head, the annotated tag v1 marks first, big holds a file one byte
// over the limit, and the branch tree names first's tree, not a commit.
type repository struct {
	store                   *memory.Storage
	first, base, head, huge string
}

func newRepository(t *testing.T) repository {
	t.Helper()
	store := memory.NewStorage()
	fs := memfs.New()
	repo, err := git.InitWithOptions(store, fs, git.InitOptions{DefaultBranch: plumbing.Main})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	commit := func(message string, files map[string]string, removed ...string) string {
		t.Helper()
		for p, content := range files {
			if err := util.WriteFile(fs, p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := wt.Add(p); err != nil {
				t.Fatal(err)
			}
		}
		for _, p := range removed {
			if _, err := wt.Remove(p); err != nil {
				t.Fatal(err)
			}
		}
		at = at.Add(time.Minute)
		h, err := wt.Commit(message, &git.CommitOptions{
			Author: &object.Signature{Name: "Ada", Email: "ada@example.com", When: at},
		})
		if err != nil {
			t.Fatal(err)
		}
		return h.String()
	}
	r := repository{store: store}
	r.first = commit("Add the stack\n\nWith its readme.", map[string]string{
		"README.md": "# infra\n",
		"main.tf":   "# one\n",
	})
	if _, err := repo.CreateTag("v1", plumbing.NewHash(r.first), &git.CreateTagOptions{
		Tagger:  &object.Signature{Name: "Ada", Email: "ada@example.com", When: at},
		Message: "v1",
	}); err != nil {
		t.Fatal(err)
	}
	r.base = commit("Change the stack", map[string]string{"main.tf": "# two\n"})
	r.head = commit("Configure the stack", map[string]string{
		".infraweave/config.yml": "version: 1\n",
		"main.tf":                "# three\n",
	}, "README.md")
	if err := wt.Checkout(
		&git.CheckoutOptions{Branch: "refs/heads/big", Create: true},
	); err != nil {
		t.Fatal(err)
	}
	r.huge = commit("Add a large file", map[string]string{
		"big.bin": strings.Repeat("x", port.MaxFileSize+1),
	})
	first, err := repo.CommitObject(plumbing.NewHash(r.first))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetReference(
		plumbing.NewHashReference("refs/heads/tree", first.TreeHash),
	); err != nil {
		t.Fatal(err)
	}
	return r
}

// serve serves the repositories under srv's TLS listener and returns a
// Source trusting it.
func serve(t *testing.T, h http.Handler) (*httptest.Server, *Source) {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	return srv, New(Options{RootCAs: roots})
}

// TestUnitSourceContract checks that Source keeps the contract over a
// repository served as Git serves one.
func TestUnitSourceContract(t *testing.T) {
	r := newRepository(t)
	srv, src := serve(t, gitServer{repos: map[string]*memory.Storage{"infra": r.store}}.routes())
	sourcetest.Run(t, src, sourcetest.Repository{
		Repo:    port.Repository{Namespace: "acme", Name: "infra", URL: srv.URL + "/infra.git"},
		Missing: port.Repository{Namespace: "acme", Name: "gone", URL: srv.URL + "/gone.git"},
		Branch:  "main",
		Tag:     "v1",
		Tagged:  r.first,
		Base:    r.base,
		Head:    r.head,
		File:    ".infraweave/config.yml",
		Content: []byte("version: 1\n"),
		Changed: []string{".infraweave/config.yml", "README.md", "main.tf"},
	})
	c, err := src.ResolveRef(t.Context(), port.Repository{URL: srv.URL + "/infra.git"}, r.first)
	if err != nil || c.Message != "Add the stack" || c.AuthorName != "Ada" ||
		c.AuthorLogin != nil ||
		c.Timestamp != time.Date(2026, 11, 10, 12, 1, 0, 0, time.UTC) {
		t.Errorf("ResolveRef(first) = %+v, %v", c, err)
	}
}

// TestUnitSourceRefs checks that a tag a server advertises unpeeled
// resolves to its commit, and that a ref naming no commit, a SHA-length
// ref that is not hex, and a file at an unknown ref are port.ErrNotFound.
func TestUnitSourceRefs(t *testing.T) {
	r := newRepository(t)
	srv, src := serve(t, gitServer{
		repos:    map[string]*memory.Storage{"infra": r.store},
		unpeeled: true,
	}.routes())
	repo := port.Repository{URL: srv.URL + "/infra.git"}
	if c, err := src.ResolveRef(t.Context(), repo, "v1"); err != nil || c.Hash != r.first ||
		c.Tag != "v1" {
		t.Errorf("ResolveRef(v1) = %+v, %v", c, err)
	}
	for _, ref := range []string{"tree", strings.Repeat("g", 40)} {
		if _, err := src.ResolveRef(t.Context(), repo, ref); !errors.Is(err, port.ErrNotFound) {
			t.Errorf("ResolveRef(%s) = %v", ref, err)
		}
	}
	if _, err := src.ReadFile(
		t.Context(),
		repo,
		"gone",
		"main.tf",
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("ReadFile at an unknown ref = %v", err)
	}
}

// TestUnitSourceTooLarge checks that a file over the limit is refused.
func TestUnitSourceTooLarge(t *testing.T) {
	r := newRepository(t)
	srv, src := serve(t, gitServer{repos: map[string]*memory.Storage{"infra": r.store}}.routes())
	repo := port.Repository{URL: srv.URL + "/infra.git"}
	if _, err := src.ReadFile(
		t.Context(),
		repo,
		"big",
		"big.bin",
	); !errors.Is(
		err,
		port.ErrTooLarge,
	) {
		t.Errorf("ReadFile(big.bin) = %v", err)
	}
	if _, err := src.ReadFile(t.Context(), repo, r.huge, ".infraweave"); err == nil {
		t.Error("ReadFile of a directory succeeded")
	}
}

// TestUnitSourceURL checks that a repository URL that is not https, or
// carries a credential, a query or a fragment, is refused before any
// request, and that the source URL is the repository's own.
func TestUnitSourceURL(t *testing.T) {
	src := New(Options{})
	for _, u := range []string{
		"",
		"http://git.example/acme/infra.git",
		"git@git.example:acme/infra.git",
		"https://user:secret@git.example/acme/infra.git",
		"https://git.example/acme/infra.git?ref=main",
		"https://git.example/acme/infra.git#main",
		"https:///acme/infra.git",
		"https://git.example/%zz",
	} {
		_, err := src.ResolveRef(t.Context(), port.Repository{URL: u}, "main")
		if !errors.Is(err, port.ErrRepositoryURL) {
			t.Errorf("ResolveRef(%q) = %v", u, err)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Errorf("ResolveRef(%q) = %v, naming the credential", u, err)
		}
	}
	repo := port.Repository{URL: "https://git.example/acme/infra.git"}
	if got := src.SourceURL(repo, strings.Repeat("a", 40)); got != repo.URL {
		t.Errorf("SourceURL = %q", got)
	}
}

// TestUnitSourceRedirect checks that a redirect off https, or one too
// many, is refused as a host Source cannot reach.
func TestUnitSourceRedirect(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/insecure.git/info/refs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://git.example/insecure.git/info/refs", http.StatusFound)
	})
	r.Get("/loop.git/info/refs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.String(), http.StatusFound)
	})
	srv, src := serve(t, r)
	for _, name := range []string{"insecure", "loop"} {
		_, err := src.ResolveRef(
			t.Context(),
			port.Repository{URL: srv.URL + "/" + name + ".git"},
			"main",
		)
		if !errors.Is(err, kernel.ErrUnavailable) {
			t.Errorf("ResolveRef(%s) = %v", name, err)
		}
	}
}

// TestUnitSourceUnavailable checks that a host that fails to answer is
// kernel.ErrUnavailable, that one that denies the repository, as a host
// answers for a private one, is port.ErrNotFound, and that an answer that
// is not Git's is neither.
func TestUnitSourceUnavailable(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/down.git/info/refs", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	})
	r.Get("/private.git/info/refs", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "sign in", http.StatusUnauthorized)
	})
	r.Get("/garbage.git/info/refs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not git"))
	})
	srv, src := serve(t, r)
	resolve := func(url string) error {
		_, err := src.ResolveRef(t.Context(), port.Repository{URL: url}, "main")
		return err
	}
	if err := resolve(srv.URL + "/down.git"); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("a 502 = %v", err)
	}
	if err := resolve(srv.URL + "/private.git"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("a 401 = %v", err)
	}
	err := resolve(srv.URL + "/garbage.git")
	if err == nil || errors.Is(err, kernel.ErrUnavailable) || errors.Is(err, port.ErrNotFound) {
		t.Errorf("an answer that is not Git's = %v", err)
	}
	closed := httptest.NewTLSServer(http.NotFoundHandler())
	closed.Close()
	if err := resolve(closed.URL + "/infra.git"); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("a closed host = %v", err)
	}
}

// TestUnitSourceUpload checks that a host failing the fetch after
// advertising its refs is kernel.ErrUnavailable, and that one refusing a
// ref it advertised keeps its cause alone.
func TestUnitSourceUpload(t *testing.T) {
	repo := newRepository(t)
	g := gitServer{repos: map[string]*memory.Storage{"infra": repo.store}}
	r := chi.NewRouter()
	r.Get("/{repo}.git/info/refs", g.refs)
	r.Post("/down.git/git-upload-pack", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	})
	r.Post("/refusing.git/git-upload-pack", func(w http.ResponseWriter, _ *http.Request) {
		_ = pktline.NewEncoder(w).EncodeString("ERR upload-pack: refused\n")
	})
	g.repos["down"], g.repos["refusing"] = repo.store, repo.store
	srv, src := serve(t, r)
	_, err := src.ResolveRef(t.Context(), port.Repository{URL: srv.URL + "/down.git"}, "main")
	if !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("ResolveRef on a failing host = %v", err)
	}
	_, err = src.ResolveRef(t.Context(), port.Repository{URL: srv.URL + "/refusing.git"}, "main")
	if err == nil || errors.Is(err, kernel.ErrUnavailable) || errors.Is(err, port.ErrNotFound) {
		t.Errorf("ResolveRef on a refusing host = %v", err)
	}
}
