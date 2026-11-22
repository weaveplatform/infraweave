package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerReplayDeprecation(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(
		`^token "(\w+)" creates and replays a "(TASK|INITIALIZATION)" policy with its dated notice through both replicas$`,
		func(ctx context.Context, token, kind string) error {
			body, _ := json.Marshal(map[string]string{"name": "Replay dated notice", "type": kind, "body": "package infraweave\n"})
			header := sdk.Header{Name: sdk.IdempotencyKeyHeader, Value: "dated-policy-notice"}
			first, err := w.Do(ctx, "A", token, http.MethodPost, "/api/v1/policies", json.RawMessage(body), header)
			if err != nil {
				return err
			}
			again, err := w.Do(ctx, "B", token, http.MethodPost, "/api/v1/policies", json.RawMessage(body), header)
			if err != nil {
				return err
			}
			document := "task-policy-verification.md"
			if kind == "INITIALIZATION" {
				document = "initialization-policy-verification.md"
			}
			if first.Status != 201 || again.Status != 201 || again.Header.Get("Idempotency-Replayed") != "true" || !bytes.Equal(first.Body, again.Body) ||
				first.Header.Get("Deprecation") != "@1795219200" ||
				first.Header.Get("Deprecation") != again.Header.Get("Deprecation") ||
				first.Header.Get("Link") != again.Header.Get("Link") ||
				!strings.Contains(again.Header.Get("Link"), document+`#deprecation-notice>; rel="deprecation"`) ||
				again.Header.Get("Sunset") != "" {
				return fmt.Errorf("%w: %s creation/replay lost its body or dated deprecation metadata: %d/%d %v", harness.ErrStep, kind, first.Status, again.Status, again.Header)
			}
			var count int
			if err := w.Row(
				ctx,
				`SELECT count(*) FROM policies WHERE account_id=$1 AND name='Replay dated notice'`,
				[]any{harness.UUIDOf(harness.Home.Account)},
				&count,
			); err != nil {
				return err
			}
			if count != 1 {
				return fmt.Errorf("%w: deprecated creation replay left %d policies", harness.ErrStep, count)
			}
			return nil
		},
	)
}
