package acceptance

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

// tenant is an account seeded through SQL with one secret API key, its
// first: no credential exists before it to create one through the API
// with. The IDs are fixed so feature files can name the key's caller,
// api_key:<key>.
type tenant struct {
	org, account, key string
	orgSlug, slug     string
}

// The scenario's tenants: home, whose key every SDK client authenticates
// with; sibling, another account of home's organization; and stranger,
// an account of another organization.
var (
	home = tenant{
		"01KACME0000000000000000001",
		"01KACME0000000000000000002",
		"01KACME0000000000000000003",
		"acme",
		"acme",
	}
	sibling = tenant{
		"01KACME0000000000000000001",
		"01KACME0000000000000000004",
		"01KACME0000000000000000005",
		"acme",
		"acme-dev",
	}
	stranger = tenant{
		"01KG10BEX00000000000000001",
		"01KG10BEX00000000000000002",
		"01KG10BEX00000000000000003",
		"globex",
		"globex",
	}
	tenants = map[string]tenant{"home": home, "sibling": sibling, "stranger": stranger}
)

// secret is the key's secret, derived from its ID so no secret is
// written down: iwk_ and 32 bytes in base64url (decision 0006).
func (t tenant) secret() string {
	h := sha256.Sum256([]byte("acceptance:" + t.key))
	return "iwk_" + base64.RawURLEncoding.EncodeToString(h[:])
}

// caller is the key's caller key for decision 0003's controls.
func (t tenant) caller() string { return string(kernel.PrincipalAPIKey) + ":" + t.key }

func uuidOf(ulid string) string {
	id, err := kernel.ParseULID(ulid)
	if err != nil {
		panic(fmt.Sprintf("tenant ID %q: %v", ulid, err))
	}
	return id.UUID()
}

// querier runs SQL in a scenario's database; *Database and *OwnPostgres
// are each one.
type querier interface {
	Query(ctx context.Context, sql string) (string, error)
}

// seed inserts t's organization, with the Enterprise entitlement creating
// an organization gives it while billing is off, its account with the top
// space, legacy and system roles that creating an account gives it, and
// its key, or
// leaves them as they are, so every replica's launch may seed.
func (t tenant) seed(ctx context.Context, db querier) error {
	digest := sha256.Sum256([]byte(t.secret()))
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
			ON CONFLICT DO NOTHING;`,
		uuidOf(t.org), t.orgSlug, uuidOf(t.account), t.slug, uuidOf(t.key),
		hex.EncodeToString(digest[:])))
	return err
}

// exchange trades t's key for an API token at POST /auth/token.
func (t tenant) exchange(ctx context.Context, c *sdk.Client) (string, error) {
	resp, err := c.Do(ctx, http.MethodPost, "/api/v1/auth/token",
		map[string]string{"id": t.key, "secret": t.secret()})
	if err != nil {
		return "", fmt.Errorf("exchange the %s key: %w", t.slug, err)
	}
	var u struct {
		JWT string `json:"jwt"`
	}
	if err := json.Unmarshal(resp.Body, &u); err != nil || u.JWT == "" {
		return "", fmt.Errorf("exchange the %s key: no jwt in %s: %w", t.slug, resp.Body, err)
	}
	return u.JWT, nil
}

// authenticated is a client of the API at base holding a token of t's
// key, which it seeds in db first, and the token.
func authenticated(
	ctx context.Context,
	db querier,
	base string,
	t tenant,
) (*sdk.Client, string, error) {
	if err := t.seed(ctx, db); err != nil {
		return nil, "", fmt.Errorf("seed the %s tenant: %w", t.slug, err)
	}
	anon, err := sdk.New(base, sdk.WithRetry(sdk.NoRetry))
	if err != nil {
		return nil, "", fmt.Errorf("sdk: %w", err)
	}
	tok, err := t.exchange(ctx, anon)
	if err != nil {
		return nil, "", err
	}
	c, err := sdk.New(base, sdk.WithRetry(sdk.NoRetry), sdk.WithToken(tok))
	if err != nil {
		return nil, "", fmt.Errorf("sdk: %w", err)
	}
	return c, tok, nil
}
