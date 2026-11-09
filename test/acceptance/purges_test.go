package acceptance

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// purgeSteps give an account rows in the domains above access, and wait
// for the purges an account's or organization's deletion starts to remove
// them.
type purgeSteps struct {
	s *servedSteps
	// held is each account's organization and account UUIDs, by its
	// slug, read while the account exists.
	held map[string][2]string
}

func (p *purgeSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the account "([a-z0-9-]+)" holds a stack and an account limit$`, p.holds)
	sc.Step(
		`^no stack or limit of the account "([a-z0-9-]+)" remains, and its organization's entitlement does$`,
		p.accountPurged,
	)
	sc.Step(
		`^no stack, limit or entitlement of the account "([a-z0-9-]+)" or its organization remains$`,
		p.organizationPurged,
	)
}

// holds writes a stack and a seats limit of the account slug names, as
// the stacks and features domains would, and records its IDs.
func (p *purgeSteps) holds(ctx context.Context, slug string) error {
	out, err := p.s.query(ctx, `SELECT organization_id || ' ' || id FROM accounts WHERE slug = `+
		literal(slug))
	if err != nil {
		return err
	}
	ids := strings.Fields(out)
	if len(ids) != 2 {
		return fmt.Errorf("%w: no account %q", errStep, slug)
	}
	p.held[slug] = [2]string{ids[0], ids[1]}
	_, err = p.s.query(ctx, fmt.Sprintf(`
		INSERT INTO stacks (account_id, id, slug, space_id, name, repository, branch, provider,
			vendor)
		VALUES ('%[2]s', gen_random_uuid(), 'infra', gen_random_uuid(), 'Infra', 'infra', 'main',
			'GITHUB', 'terraform');
		INSERT INTO account_limits (organization_id, account_id, limit_name, value)
		VALUES ('%[1]s', '%[2]s', 'seats', 1);`, ids[0], ids[1]))
	return err
}

// accountPurged waits for the purges of a deleted account to remove its
// stack and limit, and checks that they left its organization's
// entitlement.
func (p *purgeSteps) accountPurged(ctx context.Context, slug string) error {
	ids, err := p.ids(slug)
	if err != nil {
		return err
	}
	if err := p.until(ctx, slug, fmt.Sprintf(`SELECT
		(SELECT count(*) FROM stacks WHERE account_id = '%[1]s')
		+ (SELECT count(*) FROM account_limits WHERE account_id = '%[1]s')`, ids[1])); err != nil {
		return err
	}
	n, err := p.count(ctx, fmt.Sprintf(
		`SELECT count(*) FROM organization_entitlements WHERE organization_id = '%s'`, ids[0]))
	if err == nil && n != 1 {
		err = fmt.Errorf("%w: the organization of %q has %d entitlements", errStep, slug, n)
	}
	return err
}

// organizationPurged waits for the purges of a deleted organization to
// remove its account's stack and limit and the organization's entitlement.
func (p *purgeSteps) organizationPurged(ctx context.Context, slug string) error {
	ids, err := p.ids(slug)
	if err != nil {
		return err
	}
	return p.until(ctx, slug, fmt.Sprintf(`SELECT
		(SELECT count(*) FROM stacks WHERE account_id = '%[2]s')
		+ (SELECT count(*) FROM account_limits WHERE account_id = '%[2]s')
		+ (SELECT count(*) FROM organization_entitlements WHERE organization_id = '%[1]s')`,
		ids[0], ids[1]))
}

func (p *purgeSteps) ids(slug string) ([2]string, error) {
	ids, ok := p.held[slug]
	if !ok {
		return ids, fmt.Errorf("%w: the account %q holds nothing yet", errStep, slug)
	}
	return ids, nil
}

// until waits for query, a count of what remains, to answer 0.
func (p *purgeSteps) until(ctx context.Context, slug, query string) error {
	//nolint:wrapcheck // names what it waited for
	return Eventually(ctx, "the purges of "+slug, func(ctx context.Context) (bool, error) {
		n, err := p.count(ctx, query)
		return err == nil && n == 0, err
	})
}

func (p *purgeSteps) count(ctx context.Context, query string) (int, error) {
	out, err := p.s.query(ctx, query)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("%w: %q from %s", errStep, out, query)
	}
	return n, nil
}
