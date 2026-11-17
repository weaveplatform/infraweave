package pg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
)

func TestAccRepositoryRecordSealing(t *testing.T) {
	w := newWorld(t)
	run := mustCreate(t, w, kernel.RunTypeTracked, "repository-record")
	file := runtimeconfig.ParseFile([]byte("version: '2'\nstack_defaults: {environment: {TOKEN: repository-secret-567}, unknown: retained}\n"), "network", "terraform", "warn")
	if file.Refusal != nil {
		t.Fatal(file.Refusal)
	}
	record := resolvedRecord(t)
	record.Configuration.Environment = file.Defaults.Keys()
	record.Configuration.YAML = file.YAML
	record.Execution.Notices = file.Notices
	abort := errors.New("sealed specification failed")
	write := func(ctx context.Context) error {
		locked, err := w.store.Handed(ctx, run.ID)
		if err != nil {
			return err
		}
		if err := w.store.SetOverrides(ctx, locked, []lifecycle.Override{{Name: "TOKEN", Value: file.Defaults.Environment[0].Value}}); err != nil {
			return err
		}
		return w.store.RecordResolved(ctx, locked, record)
	}
	if err := tx.Do(t.Context(), w.db, tx.Options{}, func(ctx context.Context) error {
		if err := write(ctx); err != nil {
			return err
		}
		return abort
	}); !errors.Is(err, abort) {
		t.Fatalf("rollback: %v", err)
	}
	if ovs, err := w.store.Overrides(t.Context(), run); err != nil || len(ovs) != 0 {
		t.Fatalf("failed handover kept variables: %+v %v", ovs, err)
	}
	if stored, err := w.store.Handed(t.Context(), run.ID); err != nil || stored.ResolvedConfig != nil {
		t.Fatalf("failed handover kept record: %+v %v", stored.ResolvedConfig, err)
	}
	if err := tx.Do(t.Context(), w.db, tx.Options{}, write); err != nil {
		t.Fatal(err)
	}
	var raw, sealed []byte
	if err := w.db.QueryRow(t.Context(), `SELECT resolved_config, env_overrides_sealed FROM runs WHERE account_id=$1 AND id=$2`, w.acct, run.ID).Scan(&raw, &sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("repository-secret-567")) || bytes.Contains(sealed, []byte("repository-secret-567")) {
		t.Fatal("repository variable remained clear in storage")
	}
	ovs, err := w.store.Overrides(t.Context(), run)
	if err != nil || len(ovs) != 1 || ovs[0].Value != "repository-secret-567" {
		t.Fatalf("sealed repository value: %+v %v", ovs, err)
	}
	stored, err := w.store.Handed(t.Context(), run.ID)
	if err != nil || stored.ResolvedConfig.Configuration.YAML != file.YAML || len(stored.ResolvedConfig.Execution.Notices) != 1 {
		t.Fatalf("repository replay: %+v %v", stored.ResolvedConfig, err)
	}
	doc, _ := json.Marshal(stored.ResolvedConfig.Configuration)
	if bytes.Contains(doc, []byte("repository-secret-567")) {
		t.Fatal("public configuration contains a repository value")
	}
}
