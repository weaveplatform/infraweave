package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/serve"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
)

// loadArgs parses args as the serve process mode does and decodes them.
func loadArgs(t *testing.T, args ...string) (*Config, error) {
	t.Helper()
	fs := pflag.NewFlagSet("serve", pflag.ContinueOnError)
	ServeFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return Load(fs)
}

func writeFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "infraweave.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUnitLoadDefaults(t *testing.T) {
	c, err := loadArgs(t)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*c, Defaults()) {
		t.Errorf("defaults %+v, want %+v", *c, Defaults())
	}
}

// TestUnitLoadKeysWithoutFlags checks that a key no flag binds is still set
// by the file and, over it, by the environment.
func TestUnitLoadKeysWithoutFlags(t *testing.T) {
	file := writeFile(t, `
server:
  rate_limit:
    standard:
      rate: 0.5
      burst: 7
    expensive:
      burst: 2
    disabled_callers: ["192.0.2.1"]
  idempotency:
    ttl: 1h
`)
	t.Setenv("INFRAWEAVE_SERVER_RATE_LIMIT_EXPENSIVE_BURST", "3")
	t.Setenv("INFRAWEAVE_SERVER_RATE_LIMIT_DISABLED_CALLERS", "192.0.2.1,198.51.100.7")
	c, err := loadArgs(t, "--config", file)
	if err != nil {
		t.Fatal(err)
	}
	want := Defaults().Server
	want.RateLimit = ratelimit.Config{
		Standard:        ratelimit.Limit{Rate: 0.5, Burst: 7},
		Expensive:       ratelimit.Limit{Rate: want.RateLimit.Expensive.Rate, Burst: 3},
		DisabledCallers: []string{"192.0.2.1", "198.51.100.7"},
	}
	want.Idempotency.TTL = time.Hour
	if !reflect.DeepEqual(c.Server, want) {
		t.Errorf("got %+v, want %+v", c.Server, want)
	}
	t.Setenv("INFRAWEAVE_SERVER_IDEMPOTENCY_TTL", "2h")
	if c, err := loadArgs(t); err != nil || c.Server.Idempotency.TTL != 2*time.Hour {
		t.Errorf("the environment alone: %+v %v", c, err)
	}
}

// TestUnitLoadQueue checks the queue section from the file and, for a queue
// whose name holds a hyphen, from the environment.
func TestUnitLoadQueue(t *testing.T) {
	file := writeFile(t, `
queue:
  lease: 1m
  queues:
    events:
      workers: 2
    vcs-inbox:
      workers: 1
`)
	t.Setenv("INFRAWEAVE_QUEUE_QUEUES_VCS_INBOX_WORKERS", "6")
	c, err := loadArgs(t, "--config", file)
	if err != nil {
		t.Fatal(err)
	}
	if c.Events.Retention != 72*time.Hour {
		t.Errorf("events.retention %s, want the default", c.Events.Retention)
	}
	if c.Queue.Lease != time.Minute || c.Queue.Workers(queue.Events) != 2 ||
		c.Queue.Workers(queue.VCSInbox) != 6 || c.Queue.Workers(queue.Default) != 8 {
		t.Errorf("queue settings %+v", c.Queue)
	}
}

// TestUnitLoadExpressions checks the expression limits: their defaults,
// the file and the environment over it, and an unknown key refused.
func TestUnitLoadExpressions(t *testing.T) {
	c, err := loadArgs(t)
	if err != nil {
		t.Fatal(err)
	}
	if c.Expressions != expr.Defaults() || c.Expressions.CostLimit != 1_000_000 ||
		c.Expressions.Timeout != time.Second || c.Expressions.MaxSource != 16<<10 ||
		c.Expressions.MaxResult != 1<<20 {
		t.Errorf("default expressions %+v", c.Expressions)
	}
	file := writeFile(t, `
expressions:
  cost_limit: 5000
  timeout: 250ms
  max_source: 2048
  max_result: 4096
`)
	t.Setenv("INFRAWEAVE_EXPRESSIONS_TIMEOUT", "2s")
	if c, err = loadArgs(t, "--config", file); err != nil {
		t.Fatal(err)
	}
	want := expr.Config{CostLimit: 5000, Timeout: 2 * time.Second, MaxSource: 2048, MaxResult: 4096}
	if c.Expressions != want {
		t.Errorf("expressions %+v, want %+v", c.Expressions, want)
	}
	if _, err := loadArgs(t, "--config", writeFile(t, "expressions:\n  depth: 3\n")); err == nil ||
		!strings.Contains(err.Error(), "depth") {
		t.Errorf("an unknown expressions key: %v", err)
	}
}

func TestUnitLoadFeatures(t *testing.T) {
	file := writeFile(t, "features:\n  disabled: [ai-integrations, scans]\n")
	c, err := loadArgs(t, "--config", file)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Features.Disabled, []string{"ai-integrations", "scans"}) {
		t.Errorf("features.disabled %v", c.Features.Disabled)
	}
	t.Setenv("INFRAWEAVE_FEATURES_DISABLED", "intent,scans")
	if c, err = loadArgs(t, "--config", file); err != nil ||
		!slices.Equal(c.Features.Disabled, []string{"intent", "scans"}) {
		t.Errorf("features.disabled from the environment %v, %v", c.Features.Disabled, err)
	}
}

// The connection string has no flag, so it never shows in a process listing;
// the environment sets it over the file.
func TestUnitLoadDatabase(t *testing.T) {
	file := writeFile(t, `
database:
  url: postgres://file@db.example/infraweave
  max_conns: 5
  statement_timeout: 10s
`)
	const url = "postgres://env@db.example/infraweave"
	t.Setenv("INFRAWEAVE_DATABASE_URL", url)
	t.Setenv("INFRAWEAVE_DATABASE_IDLE_IN_TRANSACTION_TIMEOUT", "2m")
	c, err := loadArgs(t, "--config", file)
	if err != nil {
		t.Fatal(err)
	}
	want := Defaults().Database
	want.URL, want.MaxConns = url, 5
	want.StatementTimeout, want.IdleInTransactionTimeout = 10*time.Second, 2*time.Minute
	if c.Database != want {
		t.Errorf("got %+v, want %+v", c.Database, want)
	}
}

func TestUnitLoadPrecedence(t *testing.T) {
	file := writeFile(t, `
server:
  listen: ":1001"
  admin_listen: ":1002"
  shutdown:
    timeout: 1s
`)
	t.Setenv("INFRAWEAVE_SERVER_ADMIN_LISTEN", ":2002")
	t.Setenv("INFRAWEAVE_SERVER_SHUTDOWN_TIMEOUT", "2s")
	c, err := loadArgs(t, "--config", file, "--shutdown-timeout", "3s")
	if err != nil {
		t.Fatal(err)
	}
	// listen: the file alone; admin_listen: the environment over the file;
	// timeout: the flag over both.
	if c.Server.Listen != ":1001" || c.Server.AdminListen != ":2002" ||
		c.Server.Shutdown.Timeout != 3*time.Second {
		t.Errorf("got %+v", c.Server)
	}
}

// TestUnitLoadLiveness checks that the worker and run liveness settings
// and the sample retention decode from the file and, over it, the
// environment.
func TestUnitLoadLiveness(t *testing.T) {
	t.Setenv("INFRAWEAVE_RUN_LOST_AFTER", "2h")
	t.Setenv("INFRAWEAVE_WORKER_SAMPLES_RETENTION", "72h")
	c, err := loadArgs(t, "--config", writeFile(t, `
worker:
  offline_after: 5m
  samples:
    retention: 24h
run:
  lost_after: 90m
  preparing_timeout_default: 10m
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Worker.OfflineAfter != 5*time.Minute || c.Run.LostAfter != 2*time.Hour ||
		c.Run.PreparingTimeoutDefault != 10*time.Minute ||
		c.Worker.Samples.Retention != 72*time.Hour {
		t.Errorf("worker %+v, run %+v", c.Worker, c.Run)
	}
}

func TestUnitLoadRejects(t *testing.T) {
	cases := []struct {
		name string
		file string
		args []string
		want string
	}{
		{"unknown key", "server:\n  listn: \":1\"\n", nil, "listn"},
		{"unknown section", "storage:\n  bucket: x\n", nil, "storage"},
		{"unknown features key", "features:\n  enabled: [scans]\n", nil, "enabled"},
		{"invalid listener", "", []string{"--listen", "nowhere"}, "server.listen"},
		{
			"invalid admin listener",
			"server:\n  admin_listen: \":99999\"\n",
			nil,
			"server.admin_listen",
		},
		{"bad duration", "server:\n  shutdown:\n    timeout: soon\n", nil, "timeout"},
		{
			"unknown rate-limit key",
			"server:\n  rate_limit:\n    standard:\n      per_minute: 5\n",
			nil,
			"per_minute",
		},
		{
			"empty bucket",
			"server:\n  rate_limit:\n    expensive:\n      burst: 0\n",
			nil,
			"server.rate_limit.expensive",
		},
		{
			"no key lifetime",
			"server:\n  idempotency:\n    ttl: 0s\n",
			nil,
			"server.idempotency.ttl",
		},
		{"not YAML", "server: [\n", nil, "read config"},
		{"unknown log format", "log:\n  format: xml\n", nil, "log.format"},
		{"unknown log level", "", []string{"--log-level", "loud"}, "log.level"},
		{
			"negative ready delay",
			"server:\n  shutdown:\n    ready_delay: -1s\n",
			nil,
			"ready_delay",
		},
		{"unknown database key", "database:\n  host: db\n", nil, "host"},
		{"unknown queue", "queue:\n  queues:\n    iot:\n      workers: 1\n", nil, "iot"},
		{"bad queue lease", "queue:\n  lease: 0s\n", nil, "queue.lease"},
		{"no event retention", "events:\n  retention: 0s\n", nil, "events.retention"},
		{"no cost limit", "expressions:\n  cost_limit: 0\n", nil, "expressions.cost_limit"},
		{"no timeout", "expressions:\n  timeout: 0s\n", nil, "expressions.timeout"},
		{"no source size", "expressions:\n  max_source: 0\n", nil, "expressions.max_source"},
		{"no result size", "expressions:\n  max_result: -1\n", nil, "expressions.max_result"},
		{"unknown migrate mode", "database:\n  migrate: never\n", nil, "database.migrate"},
		{"unknown IAM provider", "database:\n  iam_auth: ibm\n", nil, "database.iam_auth"},
		{
			"IAM token without TLS",
			"database:\n  url: postgres://iw@db/x?sslmode=disable\n  iam_auth: aws\n",
			nil,
			"database.url's sslmode",
		},
		{"no database connections", "database:\n  max_conns: 0\n", nil, "database.max_conns"},
		{
			"replica without a primary",
			"database:\n  read_url: postgres://db/x\n",
			nil,
			"needs database.url",
		},
		{"no body bound", "server:\n  max_body_bytes: 0\n", nil, "server.max_body_bytes"},
		{
			"public URL with a path",
			"server:\n  public_url: https://iw.example.com/api\n",
			nil,
			"server.public_url",
		},
		{
			"public URL without a scheme",
			"server:\n  public_url: iw.example.com\n",
			nil,
			"server.public_url",
		},
		{
			"no API token lifetime",
			"auth:\n  tokens:\n    api:\n      ttl: 0s\n",
			nil,
			"auth.tokens.api.ttl",
		},
		{"unknown token kind", "auth:\n  tokens:\n    browser:\n      ttl: 1h\n", nil, "browser"},
		{
			"unknown signing backend",
			"auth:\n  signing:\n    backend: hsm\n",
			nil,
			"auth.signing.backend",
		},
		{
			"unknown rotation",
			"auth:\n  signing:\n    rotation: weekly\n",
			nil,
			"auth.signing.rotation",
		},
		{"relative issuer", "auth:\n  api_issuer: /iss\n", nil, "auth.api_issuer"},
		{"short poll hold", "worker:\n  poll_timeout_max: 1s\n", nil, "worker.poll_timeout_max"},
		{
			"offline within a held poll",
			"worker:\n  offline_after: 2m\n",
			nil,
			"worker.offline_after 2m0s is not greater than worker.poll_timeout_max 2m0s",
		},
		{
			"short sample retention",
			"worker:\n  samples:\n    retention: 30s\n",
			nil,
			"worker.samples.retention 30s is under 1m0s",
		},
		{
			"lost before offline",
			"run:\n  lost_after: 3m\n",
			nil,
			"run.lost_after 3m0s is not greater than worker.offline_after 3m0s",
		},
		{
			"short preparing timeout",
			"run:\n  preparing_timeout_default: 500ms\n",
			nil,
			"run.preparing_timeout_default",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := tc.args
			if tc.file != "" {
				args = append(args, "--config", writeFile(t, tc.file))
			}
			_, err := loadArgs(t, args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error naming %q", err, tc.want)
			}
		})
	}
}

func TestUnitLoadMissingFile(t *testing.T) {
	if _, err := loadArgs(t, "--config", filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("a missing --config file was accepted")
	}
}

// TestUnitLoadUnboundFlag checks that ServeFlags defines every settingFlags
// entry, which renaming a flag would break, and that a command with fewer
// flags loads.
func TestUnitLoadUnboundFlag(t *testing.T) {
	fs := pflag.NewFlagSet("serve", pflag.ContinueOnError)
	ServeFlags(fs)
	for name := range settingFlags {
		if fs.Lookup(name) == nil {
			t.Errorf("ServeFlags does not define --%s", name)
		}
	}

	// migrate takes --config only; the file and the environment still set
	// every key.
	fs = pflag.NewFlagSet("migrate", pflag.ContinueOnError)
	FileFlag(fs)
	t.Setenv("INFRAWEAVE_SERVER_LISTEN", ":1001")
	if err := fs.Parse(
		[]string{"--config", writeFile(t, "database:\n  migrate: check\n")},
	); err != nil {
		t.Fatal(err)
	}
	c, err := Load(fs)
	if err != nil || c.Server.Listen != ":1001" || c.Database.Migrate != "check" {
		t.Errorf("Load = %+v, %v", c, err)
	}

	// scheduler has no API listener, so no --listen.
	fs = pflag.NewFlagSet("scheduler", pflag.ContinueOnError)
	AdminFlags(fs)
	if fs.Lookup("listen") != nil {
		t.Error("AdminFlags defines --listen")
	}
	t.Setenv("INFRAWEAVE_LOG_FORMAT", "text")
	if err := fs.Parse([]string{"--admin-listen", ":1002", "--log-level", "debug"}); err != nil {
		t.Fatal(err)
	}
	c, err = Load(fs)
	if err != nil || c.Server.AdminListen != ":1002" ||
		c.Log != (observe.LogConfig{Format: "text", Level: "debug"}) {
		t.Errorf("Load = %+v, %v", c, err)
	}
}

func TestUnitServerValidate(t *testing.T) {
	if err := Defaults().Server.Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	cases := []struct {
		name string
		edit func(*Server)
		want string
	}{
		{"no port", func(s *Server) { s.Listen = "localhost" }, `server.listen "localhost"`},
		{
			"empty bucket",
			func(s *Server) { s.RateLimit.Expensive.Burst = 0 },
			"server.rate_limit.expensive: rate must be positive",
		},
		{
			"no key lifetime",
			func(s *Server) { s.Idempotency.TTL = 0 },
			"server.idempotency.ttl must be positive",
		},
		{
			"a negative body bound",
			func(s *Server) { s.MaxBodyBytes = -1 },
			"server.max_body_bytes must be positive, got -1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Defaults().Server
			tc.edit(&s)
			err := s.Validate()
			if !errors.Is(err, serve.ErrConfig) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an invalid configuration naming %q", err, tc.want)
			}
		})
	}
}

// TestUnitLoadAuth checks that each auth setting and server.public_url
// override their defaults from the file and the environment.
func TestUnitLoadAuth(t *testing.T) {
	file := writeFile(t, `
server:
  public_url: https://iw.example.com
auth:
  api_issuer: https://issuer.example.com
  tokens:
    api:
      ttl: 2h
  signing:
    rotation: manual
`)
	t.Setenv("INFRAWEAVE_AUTH_TOKENS_API_TTL", "3h")
	c, err := loadArgs(t, "--config", file)
	if err != nil {
		t.Fatal(err)
	}
	auth := c.Auth
	if c.Server.PublicURL != "https://iw.example.com" ||
		auth.APIIssuer != "https://issuer.example.com" || auth.Tokens.API.TTL != 3*time.Hour ||
		auth.Signing.Rotation != "manual" || auth.Signing.Backend != "local" {
		t.Errorf("auth = %+v, public URL %q", c.Auth, c.Server.PublicURL)
	}
	if err := c.Server.RequirePublicURL(); err != nil {
		t.Error(err)
	}
	if err := Defaults().Server.RequirePublicURL(); !errors.Is(err, serve.ErrConfig) {
		t.Errorf("RequirePublicURL with none = %v, want ErrConfig", err)
	}
}

// TestUnitLoadSecrets checks that INFRAWEAVE_SECRETS_KEK supplies the
// local KEK without hiding the secrets.kek keys the file and the other
// variables set, and that no file can set the KEK itself.
func TestUnitLoadSecrets(t *testing.T) {
	file := writeFile(t, `
secrets:
  kek:
    provider: local
`)
	t.Setenv("INFRAWEAVE_SECRETS_KEK", "a2V5")
	c, err := loadArgs(t, "--config", file)
	if err != nil {
		t.Fatal(err)
	}
	if c.Secrets.KEK.Provider != "local" || c.Secrets.KEK.Local.Key != "a2V5" {
		t.Errorf("secrets %+v", c.Secrets)
	}

	t.Setenv("INFRAWEAVE_SECRETS_KEK", "")
	t.Setenv("INFRAWEAVE_SECRETS_KEK_PROVIDER", "local")
	t.Setenv("INFRAWEAVE_SECRETS_KEK_LOCAL_KEY_FILE", "/run/secrets/kek")
	c, err = loadArgs(t)
	if err != nil {
		t.Fatal(err)
	}
	if c.Secrets.KEK.Provider != "local" || c.Secrets.KEK.Local.KeyFile != "/run/secrets/kek" {
		t.Errorf("secrets from the environment %+v", c.Secrets)
	}

	for name, body := range map[string]string{
		"a key in the file": "secrets:\n  kek:\n    local:\n      key: a2V5\n",
		"unknown provider":  "secrets:\n  kek:\n    provider: rot13\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("INFRAWEAVE_SECRETS_KEK_PROVIDER", "")
			t.Setenv("INFRAWEAVE_SECRETS_KEK_LOCAL_KEY_FILE", "")
			if _, err := loadArgs(t, "--config", writeFile(t, body)); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// TestUnitLoadObjectStore checks the objectstore keys from the file and
// the environment, a store's hyphenated retention key among them, and
// that Load refuses a setting objectstore refuses.
func TestUnitLoadObjectStore(t *testing.T) {
	file := writeFile(t, `
objectstore:
  driver: s3
  s3:
    bucket: infraweave-objects
    region: eu-west-1
    encryption: kms
    kms_key_id: alias/infraweave
  buckets:
    states: infraweave-states
  retention:
    inbox: 72h
  part_size: 33554432
  signed_url_ttl: 5m
`)
	t.Setenv("INFRAWEAVE_OBJECTSTORE_RETENTION_RUN_LOGS", "720h")
	c, err := loadArgs(t, "--config", file)
	if err != nil {
		t.Fatal(err)
	}
	o := c.ObjectStore
	if o.Driver != "s3" || o.S3.Bucket != "infraweave-objects" ||
		o.S3.KMSKeyID != "alias/infraweave" {
		t.Errorf("objectstore %+v", o)
	}
	if o.Buckets.States != "infraweave-states" || o.PartSize != 32<<20 ||
		o.SignedURLTTL != 5*time.Minute {
		t.Errorf("objectstore %+v", o)
	}
	if o.Retention.Inbox != 72*time.Hour || o.Retention.RunLogs != 720*time.Hour ||
		o.Retention.Uploads != 24*time.Hour {
		t.Errorf("retention %+v", o.Retention)
	}
	if _, err := loadArgs(
		t,
		"--config",
		writeFile(t, "objectstore:\n  driver: ftp\n"),
	); err == nil ||
		!strings.Contains(err.Error(), "objectstore.driver") {
		t.Errorf("an unknown driver: %v", err)
	}
}

// TestUnitLoadKEK checks the file infraweave secrets rewrap --to reads:
// the keys under secrets.kek, decoded strictly and validated.
func TestUnitLoadKEK(t *testing.T) {
	c, err := LoadKEK(
		writeFile(t, "provider: vault\nvault:\n  address: http://vault:8200\n  key_name: kek\n"),
	)
	if err != nil || c.Provider != "vault" || c.Vault.Address != "http://vault:8200" ||
		c.Vault.Mount != kek.Defaults().Vault.Mount {
		t.Errorf("LoadKEK: %+v %v", c, err)
	}
	for name, file := range map[string]string{
		"missing":          filepath.Join(t.TempDir(), "none.yaml"),
		"an unknown key":   writeFile(t, "provider: local\nlocal:\n  key: a2V5\n"),
		"invalid settings": writeFile(t, "provider: vault\n"),
	} {
		if _, err := LoadKEK(file); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
