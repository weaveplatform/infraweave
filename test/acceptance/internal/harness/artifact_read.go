package harness

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
)

// ReadPlanArtifact opens the actual worker-uploaded plan as the server can:
// through its recorded object key and the run's column-sealed artifact key.
// Stored bytes are returned separately so encryption cannot mask a failed
// plaintext sanitization assertion.
func (w *World) ReadPlanArtifact(ctx context.Context, run string) (plain, stored []byte, err error) {
	runID := UUIDOf(w.IDs[run])
	account := UUIDOf(Home.Account)
	var object string
	var encryptedKey []byte
	if err := w.Row(ctx, `SELECT a.object_key,r.artifact_key_sealed
  FROM run_artifacts a JOIN runs r ON (r.account_id,r.id)=(a.account_id,a.run_id)
  WHERE a.account_id=$1 AND a.run_id=$2 AND a.kind='artifact' AND a.name='plan.json'
  ORDER BY a.state_version DESC,a.recorded_at DESC LIMIT 1`, []any{account, runID}, &object, &encryptedKey); err != nil {
		return nil, nil, err
	}
	keys, pool, err := w.Keyring(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer pool.Close()
	artifact, err := keys.OpenColumn(ctx, crypt.Column{Table: "runs", Name: "artifact_key_sealed", Key: []string{"account_id", "id"}}, []string{account, runID}, encryptedKey)
	if err != nil {
		return nil, nil, fmt.Errorf("open run artifact key: %w", err)
	}
	store, err := w.Suite.Deps.ObjectStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	stored, err = store.Read(ctx, w.Bucket, "run-artifacts/"+object)
	if err != nil {
		return nil, nil, err
	}
	key, err := seal.ArtifactKey(artifact.Reveal(), object)
	if err != nil {
		return nil, nil, fmt.Errorf("derive artifact content key: %w", err)
	}
	reader, err := seal.NewReader(bytes.NewReader(stored), key)
	if err != nil {
		return nil, nil, fmt.Errorf("open worker plan artifact: %w", err)
	}
	plain, err = io.ReadAll(reader)
	if err != nil {
		return nil, nil, fmt.Errorf("read worker plan artifact: %w", err)
	}
	return plain, stored, nil
}
