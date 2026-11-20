package policyinput

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

type transaction struct{ pgx.Tx }

func (transaction) Commit(context.Context) error   { return nil }
func (transaction) Rollback(context.Context) error { return nil }

type beginner struct{}

func (beginner) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return transaction{}, nil }

type snapshotStore struct {
	run, previous lifecycle.Run
	phase         string
}

var errRead = errors.New("read failed")

func (s snapshotStore) Lock(context.Context, kernel.AccountID, kernel.ULID) (lifecycle.Run, error) {
	if s.phase == "lock" {
		return lifecycle.Run{}, errRead
	}
	return s.run, nil
}

func (s snapshotStore) PreviousPolicyRun(context.Context, lifecycle.Run) (lifecycle.Run, bool, error) {
	if s.phase == "previous" {
		return lifecycle.Run{}, false, errRead
	}
	return s.previous, !s.previous.ID.IsZero(), nil
}

func (s snapshotStore) PolicyEvidence(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]evidence.Snapshot, error) {
	if s.phase == "evidence" {
		return nil, errRead
	}
	if s.phase == "missing" {
		return map[kernel.ULID]evidence.Snapshot{}, nil
	}
	return map[kernel.ULID]evidence.Snapshot{s.run.ID: {Flags: []string{"now"}}, s.previous.ID: {Flags: []string{"before"}}}, nil
}

func TestUnitSnapshotPreservesReadFailuresAndTransactionRequirement(t *testing.T) {
	store := snapshotStore{run: lifecycle.Run{ID: kernel.NewULID()}, previous: lifecycle.Run{ID: kernel.NewULID()}}
	if _, err := Read(t.Context(), store, kernel.NewAccountID(), store.run.ID); !errors.Is(err, lifecycle.ErrNoTransaction) {
		t.Fatalf("snapshot escaped transaction: %v", err)
	}
	for _, phase := range []string{"", "lock", "previous", "evidence", "missing"} {
		t.Run(phase, func(t *testing.T) {
			store.phase = phase
			err := tx.Do(t.Context(), beginner{}, tx.Options{}, func(ctx context.Context) error {
				snapshot, err := Read(ctx, store, kernel.NewAccountID(), store.run.ID)
				if phase == "" &&
					(err != nil || snapshot.Previous == nil || !reflect.DeepEqual(snapshot.Flags, []string{"now"}) || !reflect.DeepEqual(snapshot.Previous.Flags, []string{"before"})) {
					t.Fatalf("snapshot: %+v %v", snapshot, err)
				}
				return err
			})
			if phase != "" && !errors.Is(err, errRead) && !errors.Is(err, kernel.ErrUnavailable) {
				t.Fatalf("operational read failure was lost: %v", err)
			}
		})
	}
}

func TestUnitFactsRetainsPersistedRuntimeAndStructuralMetadata(t *testing.T) {
	at := time.Unix(1000, 42).UTC()
	config := runtimeconfig.Configuration{
		AfterApply: []string{"aa"}, AfterDestroy: []string{"ad"}, AfterInit: []string{"ai"}, AfterPerform: []string{"ap"},
		AfterPlan: []string{"apl"}, AfterRun: []string{"ar"}, BeforeApply: []string{"ba"}, BeforeDestroy: []string{"bd"},
		BeforeInit: []string{"bi"}, BeforePerform: []string{"bp"}, BeforePlan: []string{"bpl"}, GitSparseCheckoutPaths: []string{"src"},
		ProjectRoot: "src", RunnerImage: "image", TerraformVersion: "1.17.2",
		Environment: []runtimeconfig.Variable{{Key: "SECRET", Value: ""}},
	}
	snapshot := Snapshot{Flags: []string{"flag"}, Run: lifecycle.Run{
		ID: kernel.NewULID(), Type: kernel.RunTypeTask, State: kernel.RunStatePerforming,
		CreatedAt: at, UpdatedAt: at, TriggeredBy: "alice", LocalWorkspace: "upload", DriftDetection: true,
		Task: &kernel.Task{Command: "echo task"}, ForceApply: "CASCADE",
		Commit:         stacksapi.Commit{AuthorName: "Alice", Branch: "main", Timestamp: at, Hash: "abc", Tag: "v1", URL: "url", Message: "message"},
		ResolvedConfig: &runtimeconfig.Record{Configuration: config},
	}}
	env := known(map[string]string{"SECRET": "actual-winning-value"})
	changes := known([]policyapi.ChangeFacts{{Address: "resource.main", Action: "added", Phase: "plan"}})
	facts := Facts(snapshot, env, changes)
	if facts.ID != snapshot.Run.ID || *facts.Command.Value != "echo task" || *facts.ForceApplyMode.Value != "CASCADE" || !*facts.BasedOnLocalWorkspace.Value ||
		!*facts.DriftDetection.Value ||
		facts.Commit.Value.At != at ||
		!reflect.DeepEqual(*facts.Flags.Value, snapshot.Flags) ||
		!reflect.DeepEqual(facts.Changes, changes) {
		t.Fatalf("persisted run facts lost: %+v", facts)
	}
	c := *facts.RuntimeConfig.Value
	if !reflect.DeepEqual(c.Environment, env) || c.ProjectRoot != config.ProjectRoot || c.RunnerImage != config.RunnerImage ||
		*c.TerraformVersion.Value != config.TerraformVersion {
		t.Fatalf("runtime metadata/actual values lost: %+v", c)
	}
	wantHooks := [][]string{
		config.AfterApply,
		config.AfterDestroy,
		config.AfterInit,
		config.AfterPerform,
		config.AfterPlan,
		config.AfterRun,
		config.BeforeApply,
		config.BeforeDestroy,
		config.BeforeInit,
		config.BeforePerform,
		config.BeforePlan,
		config.GitSparseCheckoutPaths,
	}
	for i, value := range []policyapi.Fact[[]string]{c.AfterApply, c.AfterDestroy, c.AfterInit, c.AfterPerform, c.AfterPlan, c.AfterRun, c.BeforeApply, c.BeforeDestroy, c.BeforeInit, c.BeforePerform, c.BeforePlan, c.GitSparseCheckoutPaths} {
		if value.Value == nil || !reflect.DeepEqual(*value.Value, wantHooks[i]) {
			t.Fatalf("runtime field %d lost: %+v", i, value)
		}
	}
	(*facts.Flags.Value)[0] = "changed"
	if snapshot.Flags[0] != "flag" {
		t.Fatal("input facts alias the committed flag snapshot")
	}
	snapshot.Run.ResolvedConfig, snapshot.Run.Task = nil, nil
	snapshot.Run.ForceApply = ""
	facts = Facts(snapshot, policyapi.Fact[map[string]string]{}, policyapi.Fact[[]policyapi.ChangeFacts]{})
	if facts.RuntimeConfig.Known || !facts.Command.Known || facts.Command.Value != nil || *facts.ForceApplyMode.Value != "DISABLED" || facts.UserProvidedMetadata.Known {
		t.Fatalf("unknown/disabled/null facts changed: %+v", facts)
	}
}
