package policies

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func registerSampling(sc *godog.ScenarioContext, w *harness.World) {
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
}
