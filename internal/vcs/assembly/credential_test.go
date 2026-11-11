package assembly

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// TestMain migrates the platform stream, for the jobs Release enqueues,
// and the vcs stream.
func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream(), pg.Stream())) }

// stackBook answers Settings with each stack's, by its ID.
type stackBook struct {
	stacksapi.Stacks
	settings map[kernel.ULID]stacksapi.Settings
}

func (s stackBook) Settings(
	_ context.Context,
	_ kernel.AccountID,
	id kernel.ULID,
) (stacksapi.Settings, error) {
	st, ok := s.settings[id]
	if !ok {
		return stacksapi.Settings{}, stacksapi.ErrNotFound
	}
	return st, nil
}

// world is the domain over a database and every provider's fake, with
// one integration and one stack on each, logging into logged.
type world struct {
	t      *testing.T
	db     *pgxpool.Pool
	domain *Domain
	acct   kernel.AccountID
	stacks stackBook
	store  *pg.Store
	keys   *crypt.Keyring
	logged bytes.Buffer
	cas    [][]byte

	github *githubfake.Fake
	gitlab *gitlabfake.Fake
	dc     *bitbucketdcfake.Fake
}

func newWorld(t *testing.T) *world {
	t.Helper()
	db, err := pgxpool.New(t.Context(), sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	p, err := kek.NewLocal(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypt.Load(t.Context(), db, p)
	if err != nil {
		t.Fatal(err)
	}
	return &world{
		t: t, db: db, acct: kernel.NewAccountID(), store: pg.New(db, keys), keys: keys,
		stacks: stackBook{settings: map[kernel.ULID]stacksapi.Settings{}},
	}
}

// integration keeps an integration of provider with token as its
// credential and a stack on namespace/name through it, and answers the
// stack's ID.
func (w *world) integration(
	provider string,
	set port.Settings,
	token []byte,
	path ...string,
) kernel.ULID {
	w.t.Helper()
	id := kernel.NewULID()
	in := integration.Integration{
		Account:  w.acct,
		ID:       id,
		Provider: provider,
		Slug:     "i-" + strconv.Itoa(len(w.stacks.settings)),
		Name:     provider,
		Space:    w.acct.ULID,
		Settings: set,
		Labels:   []string{},
		Checks:   integration.Checks{Mode: "INDIVIDUAL"},
	}
	if err := w.store.Create(w.t.Context(), in, integration.Secrets{
		Credential: token, WebhookSecret: []byte("hook"),
	}); err != nil {
		w.t.Fatal(err)
	}
	stack := kernel.NewULID()
	w.stacks.settings[stack] = stacksapi.Settings{
		Provider: provider, Namespace: path[0], Repository: path[1], Branch: "main",
		VCSIntegration: &id,
	}
	return stack
}

// start constructs the domain once every fake is up, trusting their
// certificates.
func (w *world) start(cloudAPI string) {
	w.t.Helper()
	ca := filepath.Join(w.t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, bytes.Join(w.cas, nil), 0o600); err != nil {
		w.t.Fatal(err)
	}
	d, err := New(Deps{
		DB: w.db, Keys: w.keys, Stacks: w.stacks,
		Settings: Settings{CAFile: ca, BitbucketCloud: BitbucketCloud{APIHost: cloudAPI}},
		Log:      slog.New(slog.NewTextHandler(&w.logged, nil)),
	})
	if err != nil {
		w.t.Fatal(err)
	}
	w.domain = d
}

// release releases run in a transaction, as the run's end does.
func (w *world) release(run kernel.ULID) {
	w.t.Helper()
	if err := tx.Do(w.t.Context(), w.db, tx.Options{}, func(ctx context.Context) error {
		return w.domain.Sources().Release(ctx, w.acct, run)
	}); err != nil {
		w.t.Fatal(err)
	}
}

// revoke runs every pending Revoke job, as the scheduler does, and
// answers how many there were.
func (w *world) revoke() int {
	w.t.Helper()
	rows, err := w.db.Query(w.t.Context(),
		`SELECT payload FROM jobs WHERE kind = $1 AND state = 'pending'`, Revoke.Name)
	if err != nil {
		w.t.Fatal(err)
	}
	var payloads [][]byte
	for rows.Next() {
		var p []byte
		if err := rows.Scan(&p); err != nil {
			w.t.Fatal(err)
		}
		payloads = append(payloads, p)
	}
	jobs := w.domain.Jobs()
	for _, p := range payloads {
		if err := jobs[0].Handler(w.t.Context(), queue.Job{Payload: p}); err != nil {
			w.t.Fatal(err)
		}
	}
	return len(payloads)
}

// TestAccCredential checks each provider's run credential against its
// fake: GitHub's installation token reads the run's repository alone with
// contents: read; GitLab's and Bitbucket Data Center's minted tokens read
// it alone, read-only, and are revoked once after the run is released
// twice; a GitLab integration below Maintainer, Bitbucket Cloud and Azure
// DevOps lend their own token, which the log notes; raw Git needs none;
// and no token reaches the log.
func TestAccCredential(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()

	gh := githubfake.New()
	ghs := gh.Start()
	t.Cleanup(ghs.Close)
	app := gh.AddApp("infraweave", "acme")
	gh.Install(app.ID, "acme", "Organization", "infra", "web")
	gh.Repo("acme", "infra").Commit("main", "first", map[string]string{"main.tf": "#\n"})
	w.github, w.cas = gh, append(w.cas, ghs.CA)
	ghStack := w.integration(integration.GitHubEnterprise,
		port.Settings{APIHost: ghs.URL, AppID: strconv.FormatInt(app.ID, 10)}, app.PrivateKey,
		"acme", "infra")

	gl := gitlabfake.New()
	gls := gl.Start()
	t.Cleanup(gls.Close)
	maintainer, developer := gl.AddToken("dev", "api"), gl.AddToken("guest", "api")
	gl.AddMember("acme", "infra", "dev", gitlabfake.Maintainer)
	gl.AddMember("acme", "infra", "guest", gitlabfake.Developer)
	gl.Repo("acme", "infra").Commit("main", "first", map[string]string{"main.tf": "#\n"})
	w.gitlab, w.cas = gl, append(w.cas, gls.CA)
	glStack := w.integration(integration.GitLab, port.Settings{APIHost: gls.URL},
		[]byte(maintainer), "acme", "infra")
	guestStack := w.integration(integration.GitLab, port.Settings{APIHost: gls.URL},
		[]byte(developer), "acme", "infra")

	dc := bitbucketdcfake.New()
	dcs := dc.Start()
	t.Cleanup(dcs.Close)
	admin := dc.AddToken("admin")
	dc.Grant("ACME", "infra", "admin", bitbucketdcfake.Admin)
	dc.Repo("ACME", "infra").Commit("main", "first", map[string]string{"main.tf": "#\n"})
	w.dc, w.cas = dc, append(w.cas, dcs.CA)
	dcStack := w.integration(integration.BitbucketDC,
		port.Settings{APIHost: dcs.URL, UserFacingHost: dcs.URL, Username: "admin"},
		[]byte(admin), "ACME", "infra")

	cloud := bitbucketcloudfake.New()
	clouds := cloud.Start()
	t.Cleanup(clouds.Close)
	cloudToken := cloud.AddToken("dev@acme.example")
	w.cas = append(w.cas, clouds.CA)
	cloudStack := w.integration(integration.BitbucketCloud,
		port.Settings{Email: "dev@acme.example"}, []byte(cloudToken), "acme", "infra")

	az := azuredevopsfake.New()
	azs := az.Start()
	t.Cleanup(azs.Close)
	pat := az.AddToken("dev")
	w.cas = append(w.cas, azs.CA)
	azStack := w.integration(integration.AzureDevOps,
		port.Settings{OrganizationURL: azs.OrganizationURL, UserFacingHost: azs.OrganizationURL},
		[]byte(pat), "Platform", "infra")

	git := kernel.NewULID()
	url := "https://git.example/acme/infra.git"
	w.stacks.settings[git] = stacksapi.Settings{Provider: integration.Git, RepositoryURL: &url}
	w.start(clouds.APIHost)

	credential := func(stack, run kernel.ULID) *vcsapi.Credential {
		t.Helper()
		c, err := w.domain.Sources().Credential(ctx, w.acct, stack, run)
		if err != nil {
			t.Fatalf("credential of %s: %v", w.stacks.settings[stack].Provider, err)
		}
		return c
	}

	if c := credential(ghStack, kernel.NewULID()); !c.Narrowed || c.Username != "x-access-token" {
		t.Errorf("GitHub = %v", c)
	}
	reqs := w.github.TokenRequests()
	if i := len(reqs) - 1; i < 0 || !slices.Equal(reqs[i].Repositories, []string{"infra"}) ||
		len(reqs[i].Permissions) != 1 || reqs[i].Permissions["contents"] != "read" {
		t.Errorf("GitHub token requests = %+v", reqs)
	}

	glRun, dcRun := kernel.NewULID(), kernel.NewULID()
	if c := credential(glStack, glRun); !c.Narrowed || c.ExpiresAt == nil ||
		string(c.Token) == maintainer {
		t.Errorf("GitLab = %v", c)
	}
	minted := w.gitlab.Minted()
	if len(minted) != 1 || minted[0].Project != "acme/infra" ||
		!slices.Equal(minted[0].Scopes, []string{"read_repository"}) ||
		minted[0].AccessLevel != gitlabfake.Reporter || minted[0].Name != "infraweave-run-"+glRun.String() {
		t.Errorf("GitLab minted %+v", minted)
	}
	if c := credential(dcStack, dcRun); !c.Narrowed || string(c.Token) == admin {
		t.Errorf("Bitbucket Data Center = %v", c)
	}
	dcMinted := w.dc.Minted()
	if len(dcMinted) != 1 || dcMinted[0].Slug != "infra" ||
		!slices.Equal(
			dcMinted[0].Permissions,
			[]string{"REPO_READ"},
		) || dcMinted[0].ExpiryDays != 1 {
		t.Errorf("Bitbucket Data Center minted %+v", dcMinted)
	}

	for name, want := range map[kernel.ULID]string{
		guestStack: developer, cloudStack: cloudToken, azStack: pat,
	} {
		c := credential(name, kernel.NewULID())
		if c.Narrowed || string(c.Token) != want || c.ExpiresAt != nil {
			t.Errorf("%s's lent credential = %v", w.stacks.settings[name].Provider, c)
		}
	}
	if c := credential(git, kernel.NewULID()); c != nil {
		t.Errorf("raw Git = %v", c)
	}

	for _, run := range []kernel.ULID{glRun, glRun, dcRun} {
		w.release(run)
	}
	if n := w.revoke(); n != 2 {
		t.Errorf("%d revoke jobs, want one per run", n)
	}
	if m := w.gitlab.Minted(); !m[0].Revoked {
		t.Errorf("GitLab's token is not revoked: %+v", m)
	}
	if m := w.dc.Minted(); !m[0].Revoked {
		t.Errorf("Bitbucket Data Center's token is not revoked: %+v", m)
	}
	var left int
	if err := w.db.QueryRow(ctx, `SELECT count(*) FROM vcs_run_tokens`).Scan(&left); err != nil ||
		left != 0 {
		t.Errorf("%d run tokens left, %v", left, err)
	}

	log := w.logged.String()
	if n := bytes.Count([]byte(log), []byte("own token")); n != 3 {
		t.Errorf("%d fallbacks logged, want 3: %s", n, log)
	}
	for _, secret := range []string{maintainer, developer, admin, cloudToken, pat} {
		if bytes.Contains([]byte(log), []byte(secret)) {
			t.Errorf("the log carries a token: %s", log)
		}
	}
}
