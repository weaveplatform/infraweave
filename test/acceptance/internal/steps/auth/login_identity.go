package auth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func (b *browserSteps) registerLoginIdentity(sc *godog.ScenarioContext) {
	b.registerLoginDelivery(sc)
	sc.Step(`^the LOGIN policy evaluation budget is (\d+) ms$`, func(milliseconds int) {
		b.w.Settings["policy"] = map[string]any{"timeouts": map[string]any{"login": fmt.Sprintf("%dms", milliseconds)}}
	})
	sc.Step(`^both refused LOGIN exchanges commit "(RESULT|TIMEOUT)" evidence without sessions or grants$`,
		func(ctx context.Context, code string) error {
			var sessions, grants, flags, evidence int
			var noRun bool
			if err := b.w.Row(ctx, `SELECT
				(SELECT count(*) FROM sessions WHERE account_id=$1 AND actor_id=$2 AND revoked_at IS NULL),
				(SELECT count(*) FROM session_grants g JOIN sessions s ON s.account_id=g.account_id AND s.id=g.session_id WHERE s.account_id=$1 AND s.actor_id=$2),
				(SELECT count(*) FROM run_flags WHERE account_id=$1), count(*),
				coalesce(bool_and(NOT(document ? 'run')),false)
				FROM events WHERE account_id=$1 AND kind='policy.evaluated' AND document->>'type'='LOGIN'
				AND document->>'policy'=$3 AND document->>'outcome'='DENY' AND document->'failure'->>'code'=$4`,
				[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(harness.Member.Key), b.w.IDs["guard"], code},
				&sessions, &grants, &flags, &evidence, &noRun); err != nil {
				return err
			}
			if sessions != 0 || grants != 0 || flags != 0 || evidence != 2 || !noRun {
				return fmt.Errorf("%w: failed LOGIN effects: sessions=%d grants=%d flags=%d evidence=%d noRun=%t", harness.ErrStep, sessions, grants, flags, evidence, noRun)
			}
			return nil
		})
	// This is an identity-session harness, not evidence of a live IdP sign-in.
	// Local managed membership intentionally says nothing about this fact.
	sc.Step(`^the browser awaits account entry with upstream membership "(true|false|unknown)" and original teams '(.*)'$`,
		func(ctx context.Context, membership, rawTeams string) error {
			value, ok := b.w.Jar.Session()
			if !ok {
				return fmt.Errorf("%w: no authenticated identity session", harness.ErrStep)
			}
			var member *bool
			if membership != "unknown" {
				known, err := strconv.ParseBool(membership)
				if err != nil {
					return fmt.Errorf("decode original membership: %w", err)
				}
				member = &known
			}
			var teams []string
			if err := json.Unmarshal([]byte(rawTeams), &teams); err != nil {
				return fmt.Errorf("decode original identity teams: %w", err)
			}
			hash := sha256.Sum256([]byte(value))
			var id kernel.ULID
			return b.w.Row(ctx, `UPDATE sessions SET account_id=NULL, admin=false,
				teams=$4, original_member=$2, original_teams_known=true, original_teams=$3
				WHERE secret_hash=$1 AND actor_kind='user' AND channel='WEB' RETURNING id`,
				[]any{hash[:], member, teams, append([]string{}, teams...)}, &id)
		})
	sc.Step(`^the last refusal hides the other account's private policy message$`, func() error {
		if strings.Contains(string(b.w.Last().Body), "OTHER_ACCOUNT_PRIVATE_REASON") {
			return fmt.Errorf("%w: another account's denial detail escaped the switch boundary", harness.ErrStep)
		}
		return nil
	})
}
