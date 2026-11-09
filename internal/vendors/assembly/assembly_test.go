package assembly

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	vendorsapi "github.com/weaveplatform/infraweave/internal/vendors/api"
	"github.com/weaveplatform/infraweave/internal/vendors/internal/index"
	"github.com/weaveplatform/infraweave/internal/vendors/internal/index/indextest"
)

// TestMain migrates the platform stream and the vendors stream alone.
func TestMain(m *testing.M) {
	os.Exit(sqltest.Main(m, schema.Stream(), Stream()))
}

// TestUnitSettings reads the vendors' public indexes by default and
// refuses an index that is not an http or https URL, naming its key.
func TestUnitSettings(t *testing.T) {
	d := DefaultSettings()
	if err := d.Validate(); err != nil || d.TerraformIndex != index.DefaultTerraformURL ||
		d.OpenTofuIndex != index.DefaultOpenTofuURL {
		t.Errorf("defaults %+v: %v", d, err)
	}
	for key, s := range map[string]Settings{
		"vendors.terraform_index": {TerraformIndex: "ftp://mirror/terraform", OpenTofuIndex: d.OpenTofuIndex},
		"vendors.opentofu_index":  {TerraformIndex: d.TerraformIndex, OpenTofuIndex: "mirror"},
	} {
		if err := s.Validate(); !errors.Is(err, ErrConfig) ||
			!strings.Contains(err.Error(), key) {
			t.Errorf("%+v = %v, want ErrConfig naming %s", s, err, key)
		}
	}
	if err := (Settings{TerraformIndex: "http://[::1"}).Validate(); err == nil {
		t.Error("a URL that does not parse was accepted")
	}
}

// TestUnitDomain registers the refresh hourly on the maintenance queue,
// and a nil domain serves the zero partial.
func TestUnitDomain(t *testing.T) {
	var none *Domain
	if none.API() != (VendorsAPI{}) {
		t.Error("a nil domain serves operations")
	}
	tasks := (&Domain{}).Cron(slog.Default())
	if len(tasks) != 1 || tasks[0].Kind != Refresh ||
		tasks[0].Every != RefreshEvery ||
		Refresh.Queue != queue.Maintenance ||
		Refresh.Policy != queue.Ignore {
		t.Errorf("tasks %+v", tasks)
	}
}

// fixture serves the recorded index name with serve.
func fixture(t *testing.T, name string, serve func([]byte) http.Handler) string {
	t.Helper()
	f, err := indextest.Load("../internal/index/testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(serve(f.Body))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestAccRefresh runs the domain's refresh over the recorded indexes into
// PostgreSQL and answers the catalogue from what it stored; a refresh
// from an index that cannot be read keeps it.
func TestAccRefresh(t *testing.T) {
	ctx := t.Context()
	db, err := pgxpool.New(ctx, sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	settings := Settings{
		TerraformIndex: fixture(
			t,
			"terraform",
			func(b []byte) http.Handler { return indextest.HashiCorp(b) },
		),
		OpenTofuIndex: fixture(
			t,
			"opentofu",
			func(b []byte) http.Handler { return indextest.OpenTofu(b) },
		),
	}
	d := New(Deps{DB: db, Settings: settings})
	if err := d.Cron(slog.Default())[0].Handler(ctx, queue.Job{}); err != nil {
		t.Fatal(err)
	}
	c := d.Catalog()
	for kind, facts := range map[vendorsapi.Vendor]indextest.Facts{
		vendorsapi.Terraform: indextest.TerraformFacts,
		vendorsapi.OpenTofu:  indextest.OpenTofuFacts,
	} {
		versions, err := c.Versions(ctx, kind)
		if err != nil || len(versions) == 0 {
			t.Fatalf("%s: %v, %v", kind, versions, err)
		}
		for _, r := range facts.Resolves {
			if got, err := c.Effective(ctx, kind, r.Constraints); err != nil || got != r.Version {
				t.Errorf("%s %q = %q, %v; want %s", kind, r.Constraints, got, err, r.Version)
			}
		}
	}
	before, _ := c.Versions(ctx, vendorsapi.Terraform)

	var logs bytes.Buffer
	gone := New(Deps{DB: db, Settings: Settings{
		TerraformIndex: "http://127.0.0.1:1/terraform", OpenTofuIndex: settings.OpenTofuIndex,
	}})
	if err := gone.Cron(slog.New(slog.NewTextHandler(&logs, nil)))[0].Handler(
		ctx,
		queue.Job{},
	); err != nil {
		t.Fatal(err)
	}
	after, _ := gone.Catalog().Versions(ctx, vendorsapi.Terraform)
	if len(after) != len(before) || !strings.Contains(logs.String(), "vendor=terraform") {
		t.Errorf(
			"an unreachable index: %d releases, was %d; logged %s",
			len(after),
			len(before),
			logs.String(),
		)
	}
	if gone.API() == (VendorsAPI{}) {
		t.Error("a domain serves the zero partial")
	}
}
