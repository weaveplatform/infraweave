package platform

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// purgeSteps give an account rows in the domains above access, and wait
// for the purges an account's or organization's deletion starts to remove
// them.
type purgeSteps struct {
	w *harness.World
	// held is each account's organization and account UUIDs, by its
	// slug, read while the account exists.
	held map[string][2]string
}

func (p *purgeSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^the account "([a-z0-9-]+)" holds a stack with a run, a context with elements attached to it, and an account limit$`,
		p.holds,
	)
	sc.Step(
		`^no stack, run, context or limit of the account "([a-z0-9-]+)" remains, and its organization's entitlement does$`,
		p.accountPurged,
	)
	sc.Step(
		`^no stack, run, context, limit or entitlement of the account "([a-z0-9-]+)" or its organization remains$`,
		p.organizationPurged,
	)
}

// holds writes a stack, a run of it with a transition that holds its run
// lock, is its tracked run and wrote an output, a context with an element
// attached to the stack, an element of the stack's own and a seats limit
// of the account slug names, as the stacks, runs, configuration and
// features domains would, and records its IDs.
func (p *purgeSteps) holds(ctx context.Context, slug string) error {
	out, err := p.w.Query(ctx, `SELECT organization_id || ' ' || id FROM accounts WHERE slug = `+
		harness.Literal(slug))
	if err != nil {
		return err
	}
	ids := strings.Fields(out)
	if len(ids) != 2 {
		return fmt.Errorf("%w: no account %q", harness.ErrStep, slug)
	}
	p.held[slug] = [2]string{ids[0], ids[1]}
	_, err = p.w.Query(ctx, fmt.Sprintf(`
		INSERT INTO stacks (account_id, id, slug, space_id, name, repository, branch, provider,
			vendor)
		VALUES ('%[2]s', gen_random_uuid(), 'infra', gen_random_uuid(), 'Infra', 'infra', 'main',
			'GITHUB', 'terraform');
		INSERT INTO contexts (account_id, id, slug, space_id, name)
		VALUES ('%[2]s', gen_random_uuid(), 'shared', gen_random_uuid(), 'Shared');
		INSERT INTO config_elements (account_id, id, context_id, stack_id, element, file_mount,
			value_sealed, checksum, write_only)
		SELECT c.account_id, gen_random_uuid(), c.id, NULL, 'A', false, '\x00'::bytea, '', false
			FROM contexts c WHERE c.account_id = '%[2]s'
		UNION ALL
		SELECT s.account_id, gen_random_uuid(), NULL, s.id, 'B', false, '\x00'::bytea, '', false
			FROM stacks s WHERE s.account_id = '%[2]s';
		INSERT INTO context_attachments (account_id, id, context_id, stack_id)
		SELECT c.account_id, gen_random_uuid(), c.id, s.id
			FROM contexts c JOIN stacks s ON s.account_id = c.account_id
			WHERE c.account_id = '%[2]s';
		INSERT INTO runs (account_id, id, stack_id, type, state, commit, triggered_by)
		SELECT account_id, gen_random_uuid(), id, 'TRACKED', 'APPLYING', '{"hash":"abc"}', 'alice'
			FROM stacks WHERE account_id = '%[2]s';
		INSERT INTO run_state_transitions (account_id, run_id, sequence, state, terminal)
		SELECT account_id, id, 1, 'QUEUED', false FROM runs WHERE account_id = '%[2]s';
		INSERT INTO stack_run_locks (account_id, stack_id, run_id)
		SELECT account_id, stack_id, id FROM runs WHERE account_id = '%[2]s';
		INSERT INTO stack_run_state (account_id, stack_id, tracked_run_id)
		SELECT account_id, stack_id, id FROM runs WHERE account_id = '%[2]s';
		INSERT INTO stack_outputs (account_id, stack_id, name, run_id, value)
		SELECT account_id, stack_id, 'url', id, '"https://example.com"' FROM runs
			WHERE account_id = '%[2]s';
		INSERT INTO account_limits (organization_id, account_id, limit_name, value)
		VALUES ('%[1]s', '%[2]s', 'seats', 1);`, ids[0], ids[1]))
	return err
}

// held counts what remains of the account acct in the stacks, runs and
// configuration domains, and of its limits.
func held(acct string) string {
	return fmt.Sprintf(`(SELECT count(*) FROM stacks WHERE account_id = '%[1]s')
		+ (SELECT count(*) FROM contexts WHERE account_id = '%[1]s')
		+ (SELECT count(*) FROM config_elements WHERE account_id = '%[1]s')
		+ (SELECT count(*) FROM context_attachments WHERE account_id = '%[1]s')
		+ (SELECT count(*) FROM runs WHERE account_id = '%[1]s')
		+ (SELECT count(*) FROM run_state_transitions WHERE account_id = '%[1]s')
		+ (SELECT count(*) FROM stack_run_locks WHERE account_id = '%[1]s')
		+ (SELECT count(*) FROM stack_run_state WHERE account_id = '%[1]s')
		+ (SELECT count(*) FROM stack_outputs WHERE account_id = '%[1]s')
		+ (SELECT count(*) FROM account_limits WHERE account_id = '%[1]s')`, acct)
}

// accountPurged waits for the purges of a deleted account to remove its
// stack, run and what the run holds, context, elements, attachment and
// limit, and checks that they
// left its organization's entitlement.
func (p *purgeSteps) accountPurged(ctx context.Context, slug string) error {
	ids, err := p.ids(slug)
	if err != nil {
		return err
	}
	if err := p.until(ctx, slug, "SELECT "+held(ids[1])); err != nil {
		return err
	}
	n, err := p.count(ctx, fmt.Sprintf(
		`SELECT count(*) FROM organization_entitlements WHERE organization_id = '%s'`, ids[0]))
	if err == nil && n != 1 {
		err = fmt.Errorf("%w: the organization of %q has %d entitlements", harness.ErrStep, slug, n)
	}
	return err
}

// organizationPurged waits for the purges of a deleted organization to
// remove what its account holds and the organization's entitlement.
func (p *purgeSteps) organizationPurged(ctx context.Context, slug string) error {
	ids, err := p.ids(slug)
	if err != nil {
		return err
	}
	return p.until(ctx, slug, fmt.Sprintf(`SELECT %s
		+ (SELECT count(*) FROM organization_entitlements WHERE organization_id = '%s')`,
		held(ids[1]), ids[0]))
}

func (p *purgeSteps) ids(slug string) ([2]string, error) {
	ids, ok := p.held[slug]
	if !ok {
		return ids, fmt.Errorf("%w: the account %q holds nothing yet", harness.ErrStep, slug)
	}
	return ids, nil
}

// until waits for query, a count of what remains, to answer 0.
func (p *purgeSteps) until(ctx context.Context, slug, query string) error {
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, "the purges of "+slug, func(ctx context.Context) (bool, error) {
		n, err := p.count(ctx, query)
		return err == nil && n == 0, err
	})
}

func (p *purgeSteps) count(ctx context.Context, query string) (int, error) {
	out, err := p.w.Query(ctx, query)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("%w: %q from %s", harness.ErrStep, out, query)
	}
	return n, nil
}
