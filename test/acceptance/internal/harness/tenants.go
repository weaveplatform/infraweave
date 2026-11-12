package harness

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/pkg/sdk"
)

// Tenant is an account seeded through SQL with one secret API key, its
// first: no credential exists before it to create one through the API
// with. The key is space-admin on the account's top space, as an
// account's first admin is, unless member. The IDs are fixed so feature
// files can name the key's caller, api_key:<key>.
type Tenant struct {
	Org, Account, Key string
	OrgSlug, Slug     string
	member            bool
}

// The scenario's tenants: home, whose key every SDK client authenticates
// with; member, a key of home's account with no binding; sibling, another
// account of home's organization; and stranger, an account of another
// organization.
var (
	Home = Tenant{
		"01KACME0000000000000000001",
		"01KACME0000000000000000002",
		"01KACME0000000000000000003",
		"acme",
		"acme",
		false,
	}
	Member = Tenant{
		"01KACME0000000000000000001",
		"01KACME0000000000000000002",
		"01KACME000000000000000000A",
		"acme",
		"acme",
		true,
	}
	Sibling = Tenant{
		"01KACME0000000000000000001",
		"01KACME0000000000000000004",
		"01KACME0000000000000000005",
		"acme",
		"acme-dev",
		false,
	}
	stranger = Tenant{
		"01KG10BEX00000000000000001",
		"01KG10BEX00000000000000002",
		"01KG10BEX00000000000000003",
		"globex",
		"globex",
		false,
	}
	Tenants = map[string]Tenant{
		"home": Home, "member": Member, "sibling": Sibling, "stranger": stranger,
	}
)

// Secret is the key's secret, derived from its ID so no secret is
// written down: iwk_ and 32 bytes in base64url, the format of every API
// key secret.
//
// ref: decision 0006
func (t Tenant) Secret() string {
	h := sha256.Sum256([]byte("acceptance:" + t.Key))
	return "iwk_" + base64.RawURLEncoding.EncodeToString(h[:])
}

// caller is the key's caller, <kind>:<actor ID>, which keys the rate
// limits, idempotency keys and API disable switch.
//
// ref: decision 0003
func (t Tenant) caller() string { return string(kernel.PrincipalAPIKey) + ":" + t.Key }

func UUIDOf(ulid string) string {
	id, err := kernel.ParseULID(ulid)
	if err != nil {
		panic(fmt.Sprintf("tenant ID %q: %v", ulid, err))
	}
	return id.UUID()
}

// Querier runs SQL in a scenario's database; *Database and *OwnPostgres
// are each one.
type Querier interface {
	Query(ctx context.Context, sql string) (string, error)
}

// Seed inserts t's organization, with the Enterprise entitlement creating
// an organization gives it while billing is off, its account with the top
// space, legacy and system roles that creating an account gives it, and
// its key with its binding, or leaves them as they are, so every
// replica's launch may seed.
func (t Tenant) Seed(ctx context.Context, db Querier) error {
	digest := sha256.Sum256([]byte(t.Secret()))
	binding := fmt.Sprintf(`
		INSERT INTO role_bindings (account_id, id, actor_kind, actor_id, role_id, space_id, origin,
				created_by)
			SELECT '%[1]s', gen_random_uuid(), 'api_key', '%[2]s', id, '%[1]s', 'binding', 'acceptance'
			FROM roles WHERE account_id = '%[1]s' AND slug = 'space-admin'
			ON CONFLICT DO NOTHING;%[3]s`, UUIDOf(t.Account), UUIDOf(t.Key), AuthzChanged(t.Account))
	if t.member {
		binding = ""
	}
	_, err := db.Query(ctx, fmt.Sprintf(`
		INSERT INTO organizations (id, slug, name) VALUES ('%[1]s', '%[2]s', '%[2]s')
			ON CONFLICT DO NOTHING;
		INSERT INTO organization_entitlements (organization_id, tier) VALUES ('%[1]s', 'Enterprise')
			ON CONFLICT DO NOTHING;
		INSERT INTO accounts (organization_id, id, slug, name)
			VALUES ('%[1]s', '%[3]s', '%[4]s', '%[4]s') ON CONFLICT DO NOTHING;
		INSERT INTO spaces (account_id, id, slug, name, parent_id)
			VALUES ('%[3]s', '%[3]s', '%[4]s', '%[4]s', NULL),
				('%[3]s', gen_random_uuid(), 'legacy', 'legacy', '%[3]s')
			ON CONFLICT DO NOTHING;
		INSERT INTO space_closure (account_id, ancestor_id, descendant_id, depth)
			SELECT account_id, id, id, 0 FROM spaces WHERE account_id = '%[3]s'
			UNION ALL SELECT account_id, parent_id, id, 1 FROM spaces
				WHERE account_id = '%[3]s' AND parent_id IS NOT NULL
			ON CONFLICT DO NOTHING;
		INSERT INTO roles (account_id, id, slug, name, description, is_system)
			SELECT '%[3]s', gen_random_uuid(), slug, name, '', true FROM (VALUES
				('space-reader', 'Space reader'), ('space-writer', 'Space writer'),
				('space-admin', 'Space admin'), ('worker-pool-controller', 'Worker pool controller'),
				('drift-detection-manager', 'Drift detection manager')) AS r (slug, name)
			ON CONFLICT DO NOTHING;
		INSERT INTO api_keys (account_id, id, name, type, secret_hash, teams, creator)
			VALUES ('%[3]s', '%[5]s', 'acceptance', 'SECRET', '\x%[6]s', '{acceptance}', 'acceptance')
			ON CONFLICT DO NOTHING;%[7]s`,
		UUIDOf(t.Org), t.OrgSlug, UUIDOf(t.Account), t.Slug, UUIDOf(t.Key),
		hex.EncodeToString(digest[:]), binding))
	return err
}

// AuthzChanged is the SQL that marks the account's authorization changed,
// as the access domain's writes do: its version moves and authz:<account>
// goes out, so every replica drops the snapshot it holds at once.
func AuthzChanged(account string) string {
	return fmt.Sprintf(`
		UPDATE accounts SET authz_version = authz_version + 1 WHERE id = '%s';
		SELECT pg_notify('infraweave_invalidate', 'authz:%s');`, UUIDOf(account), account)
}

// Exchange trades t's key for an API token at POST /auth/token.
func (t Tenant) Exchange(ctx context.Context, c *sdk.Client) (string, error) {
	resp, err := c.Do(ctx, http.MethodPost, "/api/v1/auth/token",
		map[string]string{"id": t.Key, "secret": t.Secret()})
	if err != nil {
		return "", fmt.Errorf("exchange the %s key: %w", t.Slug, err)
	}
	var u struct {
		JWT string `json:"jwt"`
	}
	if err := json.Unmarshal(resp.Body, &u); err != nil || u.JWT == "" {
		return "", fmt.Errorf("exchange the %s key: no jwt in %s: %w", t.Slug, resp.Body, err)
	}
	return u.JWT, nil
}

// Authenticated is a client of the API at base holding a token of t's
// key, which it seeds in db first, and the token.
func Authenticated(
	ctx context.Context,
	db Querier,
	base string,
	t Tenant,
) (*sdk.Client, string, error) {
	if err := t.Seed(ctx, db); err != nil {
		return nil, "", fmt.Errorf("seed the %s tenant: %w", t.Slug, err)
	}
	anon, err := sdk.New(base, sdk.WithRetry(sdk.NoRetry))
	if err != nil {
		return nil, "", fmt.Errorf("sdk: %w", err)
	}
	tok, err := t.Exchange(ctx, anon)
	if err != nil {
		return nil, "", err
	}
	c, err := sdk.New(base, sdk.WithRetry(sdk.NoRetry), sdk.WithToken(tok))
	if err != nil {
		return nil, "", fmt.Errorf("sdk: %w", err)
	}
	return c, tok, nil
}

// MemberID and MemberSession are the home account's member and their
// browser session, seeded as #171's sign-in will create them.
const (
	MemberID      = "01KACME0000000000000000006"
	MemberSession = "01KACME0000000000000000007"
)

// OperatorKey is the scenario's operator key; its secret derives from it
// as a tenant's does, so no secret is written down.
const OperatorKey = "01KA2V0P3RAT0RKEY000000000"

func OperatorSecret() string {
	h := sha256.Sum256([]byte("acceptance:" + OperatorKey))
	return "iwk_" + base64.RawURLEncoding.EncodeToString(h[:])
}

// OperatorAuth is the auth settings that hold the operator key in
// auth.operator.keys.
func OperatorAuth() map[string]any {
	sum := sha256.Sum256([]byte(OperatorSecret()))
	return map[string]any{"operator": map[string]any{"keys": []map[string]any{{
		"id": OperatorKey, "secret_sha256": fmt.Sprintf("%x", sum),
	}}}}
}
