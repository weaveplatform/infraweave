package policies

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func registerSampling(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(
		`^token "(\w+)" eventually reads run "([^"]+)" with "(retained|expired)" APPROVAL samples through ([AB])$`,
		func(ctx context.Context, token, run, state, replica string) error {
			return deps.Eventually(ctx, state+" public APPROVAL samples", func(ctx context.Context) (bool, error) {
				response, err := w.Do(ctx, replica, token, http.MethodGet, "/api/v1/runs/"+w.IDs[run], nil)
				if err != nil {
					return false, err
				}
				if response.Status != http.StatusOK {
					return false, fmt.Errorf("%w: read sampled run returned %d", harness.ErrStep, response.Status)
				}
				var view api.Run
				if err := json.Unmarshal(response.Body, &view); err != nil {
					return false, fmt.Errorf("decode sampled run: %w", err)
				}
				found := false
				for _, receipt := range view.PolicyReceipts {
					if receipt.PolicyType != api.PolicyTypeAPPROVAL {
						continue
					}
					found = true
					if receipt.Sampled != (state == "retained") || receipt.SampleExpired != (state == "expired") {
						return false, nil
					}
				}
				return found, nil
			})
		},
	)
	sc.Step(`^the retained inputs for run "([^"]+)" expire$`, func(ctx context.Context, run string) error {
		_, pool, err := w.Keyring(ctx)
		if err != nil {
			return err
		}
		defer pool.Close()
		return tx.Do(ctx, pool, tx.Options{}, func(ctx context.Context) error {
			tag, err := tx.Q(ctx, pool).Exec(ctx, `UPDATE policy_input_objects i SET expires_at=statement_timestamp()-interval '1 minute'
    FROM policy_evaluations e WHERE e.account_id=$1 AND e.run_id=$2 AND e.sample_input_id=i.id AND i.account_id=e.account_id`, harness.UUIDOf(harness.Home.Account), harness.UUIDOf(w.IDs[run]))
			if err != nil {
				return fmt.Errorf("age run sample ceiling: %w", err)
			}
			if tag.RowsAffected() == 0 {
				return fmt.Errorf("%w: no published run input to expire", harness.ErrStep)
			}
			return nil
		})
	})
	sc.Step(`^the input cleanup job removes the expired private data for run "([^"]+)"$`, func(ctx context.Context, run string) error {
		_, pool, err := w.Keyring(ctx)
		if err != nil {
			return err
		}
		defer pool.Close()
		q := queue.New(pool)
		if err := tx.Do(ctx, pool, tx.Options{}, func(ctx context.Context) error {
			cleanup := queue.Kind{Name: "policies.input-cleanup", Queue: queue.Maintenance, Policy: queue.Ignore}
			for _, kind := range []queue.Kind{cleanup, blobs.ExpireKind} {
				if _, err := q.EnqueueTx(ctx, kind, nil); err != nil {
					return fmt.Errorf("enqueue real input cleanup: %w", err)
				}
			}
			return nil
		}); err != nil {
			return err
		}
		return deps.Eventually(ctx, "input cleanup and physical blob deletion", func(ctx context.Context) (bool, error) {
			var expired, private int
			err := w.Row(ctx, `SELECT count(*) FILTER (WHERE i.state='expired' AND i.published_at IS NOT NULL),
    count(*) FILTER (WHERE i.staging_sealed IS NOT NULL OR b.key IS NOT NULL)
    FROM policy_evaluations e JOIN policy_input_objects i ON i.account_id=e.account_id AND i.id=e.sample_input_id
    LEFT JOIN blob_objects b ON b.store='policy-inputs' AND b.key=i.object_key WHERE e.account_id=$1 AND e.run_id=$2`,
				[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(w.IDs[run])}, &expired, &private)
			return expired > 0 && private == 0, err
		})
	})
	sc.Step(`^policy "([^"]+)" eventually has (\d+) retained TASK sample$`, func(ctx context.Context, name string, expected int) error {
		return deps.Eventually(ctx, fmt.Sprintf("%d retained TASK samples", expected), func(ctx context.Context) (bool, error) {
			var count int
			err := w.Row(ctx, `SELECT count(*) FROM policy_evaluations e JOIN policy_input_objects i
                ON i.account_id=e.account_id AND i.id=e.sample_input_id
                WHERE e.account_id=$1 AND e.policy_id=$2 AND e.type='TASK'
                AND i.state='live' AND e.blob_key IS NOT NULL`,
				[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(w.IDs[name])}, &count)
			return count == expected, err
		})
	})
	sc.Step(`^policy "([^"]+)" keeps "([^"]+)" and "([^"]+)" out of raw storage and logs$`, func(ctx context.Context, name, input, print string) error {
		var key string
		var contentKey []byte
		if err := w.Row(ctx, `SELECT e.blob_key,b.content_key_sealed FROM policy_evaluations e
			JOIN blob_objects b ON b.store='policy-inputs' AND b.key=e.blob_key AND b.state='live'
			JOIN policy_input_objects i ON i.account_id=e.account_id AND i.id=e.sample_input_id
			WHERE e.account_id=$1 AND e.policy_id=$2 AND i.staging_sealed IS NULL ORDER BY e.created_at DESC LIMIT 1`,
			[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(w.IDs[name])}, &key, &contentKey); err != nil {
			return err
		}
		store, err := w.Suite.Deps.ObjectStore(ctx)
		if err != nil {
			return err
		}
		raw, err := store.Read(ctx, w.Bucket, "policy-inputs/"+key)
		if err != nil {
			return err
		}
		for _, marker := range []string{input, print} {
			if bytes.Contains(raw, []byte(marker)) {
				return fmt.Errorf("%w: private sample entered raw object bytes", harness.ErrStep)
			}
			for _, server := range w.Servers {
				if bytes.Contains(server.Stdout(), []byte(marker)) || bytes.Contains(server.Stderr(), []byte(marker)) {
					return fmt.Errorf("%w: private sample entered server output", harness.ErrStep)
				}
			}
		}
		keys, pool, err := w.Keyring(ctx)
		if err != nil {
			return err
		}
		defer pool.Close()
		column := crypt.Column{Table: "blob_objects", Name: "content_key_sealed", Key: []string{"store", "key"}}
		aad, err := column.AAD([]string{"policy-inputs", key})
		if err != nil {
			return err
		}
		opened, err := keys.OpenObject(ctx, bytes.NewReader(raw), aad, contentKey)
		if err != nil {
			return err
		}
		plain, err := io.ReadAll(opened)
		if err != nil {
			return err
		}
		if !bytes.Contains(plain, []byte(input)) || !bytes.Contains(plain, []byte(print)) {
			return fmt.Errorf("%w: sealed sample lost actual input or captured print", harness.ErrStep)
		}
		for _, parts := range [][]string{{"run-artifacts", key}, {"policy-inputs", key + "/swapped"}} {
			wrong, err := column.AAD(parts)
			if err != nil {
				return err
			}
			if _, err := keys.OpenObject(ctx, bytes.NewReader(raw), wrong, contentKey); err == nil {
				return fmt.Errorf("%w: sample decrypted under a swapped store/key identity", harness.ErrStep)
			}
		}
		return nil
	})
	sc.Step(`^the latest TASK capture has a sanitized command and no "([^"]+)"$`, func(ctx context.Context, marker string) error {
		var key string
		var sealed []byte
		if err := deps.Eventually(ctx, "published TASK capture", func(ctx context.Context) (bool, error) {
			var count int
			err := w.Row(ctx, `SELECT count(*) FROM policy_input_capture_heads h JOIN policy_input_objects i
    ON i.account_id=h.account_id AND i.id=h.current_input_id
    WHERE h.account_id=$1 AND h.capture_kind='TASK' AND i.state='live'`, []any{harness.UUIDOf(harness.Home.Account)}, &count)
			return count == 1, err
		}); err != nil {
			return err
		}
		if err := w.Row(ctx, `SELECT i.object_key,b.content_key_sealed FROM policy_input_capture_heads h
   JOIN policy_input_objects i ON i.account_id=h.account_id AND i.id=h.current_input_id
   JOIN blob_objects b ON b.store='policy-inputs' AND b.key=i.object_key AND b.state='live'
   WHERE h.account_id=$1 AND h.capture_kind='TASK'`, []any{harness.UUIDOf(harness.Home.Account)}, &key, &sealed); err != nil {
			return err
		}
		store, err := w.Suite.Deps.ObjectStore(ctx)
		if err != nil {
			return err
		}
		raw, err := store.Read(ctx, w.Bucket, "policy-inputs/"+key)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte(marker)) {
			return fmt.Errorf("%w: capture plaintext in raw storage", harness.ErrStep)
		}
		keys, pool, err := w.Keyring(ctx)
		if err != nil {
			return err
		}
		defer pool.Close()
		aad, err := (crypt.Column{Table: "blob_objects", Name: "content_key_sealed", Key: []string{"store", "key"}}).AAD([]string{"policy-inputs", key})
		if err != nil {
			return err
		}
		reader, err := keys.OpenObject(ctx, bytes.NewReader(raw), aad, sealed)
		if err != nil {
			return err
		}
		plain, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		var document struct {
			Request struct {
				Command string `json:"command"`
			} `json:"request"`
			Stack struct {
				ID string `json:"id"`
			} `json:"stack"`
		}
		if err := json.Unmarshal(plain, &document); err != nil {
			return fmt.Errorf("decode capture shape: %w", err)
		}
		if bytes.Contains(plain, []byte(marker)) || document.Request.Command != "[redacted]" || document.Stack.ID != "infra" {
			return fmt.Errorf("%w: capture privacy or structural identity changed", harness.ErrStep)
		}
		return nil
	})
}
