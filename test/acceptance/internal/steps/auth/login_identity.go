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
