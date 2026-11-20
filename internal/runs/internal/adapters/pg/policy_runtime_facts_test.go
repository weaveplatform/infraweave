package pg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/policyinput"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
)

func TestAccPolicyInputOpensRecordedPreviousRuntimeValues(t *testing.T) {
	w := newWorld(t)
	previous := mustCreate(t, w, kernel.RunTypeTracked, "historical-runtime")
	record := &runtimeconfig.Record{Configuration: runtimeconfig.Configuration{
		Environment:      []runtimeconfig.Variable{{Key: "TOKEN"}, {Key: "EMPTY"}},
		TerraformVersion: "1.17.2", ProjectRoot: "recorded-root", RunnerImage: "recorded-image",
	}}
	if err := w.in(t, func(ctx context.Context) error {
		if err := w.store.RecordResolved(ctx, previous, record); err != nil {
			return err
		}
		return w.store.SetOverrides(ctx, previous, []lifecycle.Override{
			{Name: "TOKEN", Value: "earlier-value"},
			{Name: "TOKEN", Value: "recorded-winning-value"},
			{Name: "EMPTY", Value: ""},
			{Name: "PROPOSAL_ONLY", Value: "not-a-runtime-setting"},
		})
	}); err != nil {
		t.Fatal(err)
	}
	w.now = w.now.Add(time.Second)
	current := mustCreate(t, w, kernel.RunTypeTracked, "later-runtime")
	if err := w.in(t, func(ctx context.Context) error {
		return w.store.SetOverrides(ctx, current, []lifecycle.Override{{Name: "TOKEN", Value: "newer-value"}})
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.in(t, func(ctx context.Context) error {
		snapshot, err := policyinput.Read(ctx, w.store, w.acct, current.ID)
		if err != nil {
			return err
		}
		if snapshot.Previous == nil || !snapshot.Previous.Environment.Known || snapshot.Previous.Environment.Value == nil {
			return errors.New("previous runtime values were not materialized from sealed storage")
		}
		want := map[string]string{"TOKEN": "recorded-winning-value", "EMPTY": ""}
		if !reflect.DeepEqual(*snapshot.Previous.Environment.Value, want) || snapshot.Environment.Known {
			return errors.New("previous runtime used current values, lost empty values, or included proposal-only keys")
		}
		facts := policyinput.Facts(*snapshot.Previous, policyapi.Fact[map[string]string]{}, policyapi.Fact[[]policyapi.ChangeFacts]{})
		if facts.RuntimeConfig.Value == nil || !reflect.DeepEqual(facts.RuntimeConfig.Value.Environment, snapshot.Previous.Environment) {
			return errors.New("typed facts lost the recorded runtime environment")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var public, sealed []byte
	if err := w.db.QueryRow(t.Context(), `SELECT resolved_config,env_overrides_sealed FROM runs WHERE account_id=$1 AND id=$2`, w.acct, previous.ID).
		Scan(&public, &sealed); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(public) || bytes.Contains(public, []byte("recorded-winning-value")) || bytes.Contains(sealed, []byte("recorded-winning-value")) {
		t.Fatal("runtime values escaped the sealed column")
	}
	if _, err := w.db.Exec(t.Context(), `UPDATE runs SET env_overrides_sealed=$3 WHERE account_id=$1 AND id=$2`, w.acct, previous.ID, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := w.in(t, func(ctx context.Context) error {
		_, err := policyinput.Read(ctx, w.store, w.acct, current.ID)
		return err
	}); err == nil || errors.Is(err, policyapi.ErrInput) {
		t.Fatalf("sealed-storage failure did not remain operational: %v", err)
	}
}
