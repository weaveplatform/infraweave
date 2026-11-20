package runstest

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/policyinput"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// EvidenceDeps contains real persistence and public owner ports constructed by
// internal/app. This package constructs no other domain and starts no server.
type EvidenceDeps struct {
	DB     tx.Querier
	Keys   *crypt.Keyring
	Stacks stacksapi.Stacks
	Events *outbox.Outbox
	Jobs   *queue.Queue
	At     time.Time
}

// Input exercises the production run snapshot and policy-owned builder.
func Input(ctx context.Context, d EvidenceDeps, builder policyapi.RunInputBuilder, facts policyapi.RunInputFacts, id kernel.ULID) (json.RawMessage, error) {
	store := pg.New(d.DB, d.Keys)
	snapshot, err := policyinput.Read(ctx, store, facts.Account, id)
	if err != nil {
		return nil, err //nolint:wrapcheck // the production snapshot names the read
	}
	facts.Run = policyinput.Facts(snapshot, policyapi.Fact[map[string]string]{}, policyapi.Fact[[]policyapi.ChangeFacts]{})
	if facts.Type == policyapi.Plan && snapshot.Previous != nil {
		previous := policyinput.Facts(*snapshot.Previous, policyapi.Fact[map[string]string]{}, policyapi.Fact[[]policyapi.ChangeFacts]{})
		facts.PreviousRun = policyapi.Fact[policyapi.RunFacts]{Known: true, Value: &previous}
	}
	return builder.BuildRunInput(ctx, facts) //nolint:wrapcheck // the policy owner names invalid facts
}

// Ready exercises the existing QUEUED→READY scheduler transition with evidence
// whose actual approval was checked by the composition test. It is test support,
// not a public approval endpoint or a substitute for workflow gate integration.
func Ready(ctx context.Context, d EvidenceDeps, account kernel.AccountID, id kernel.ULID, version int64, receipts []policyapi.Receipt) error {
	store := pg.New(d.DB, d.Keys)
	lc := lifecycle.New(lifecycle.Deps{Store: store, Evidence: store, Stacks: d.Stacks, Events: d.Events, Jobs: d.Jobs, Now: func() time.Time { return d.At }})
	_, err := lc.Transition(ctx, lifecycle.Request{
		Account: account, Run: id, Version: version,
		To: kernel.RunStateReady, Driver: machine.DriverScheduler,
		Checked:  []machine.Guard{machine.GuardStackLockFree, machine.GuardApproved, machine.GuardUpstreamFinished, machine.GuardExternalSettled, machine.GuardStackEnabled},
		Receipts: receipts,
	})
	return err //nolint:wrapcheck // the production transition names the refusal
}

// RecordRuntimeEnvironment supplies an explicitly labelled post-handover
// storage fixture through the real owner store and keyring. It does not claim
// a public worker handover or policy-aware report executed.
func RecordRuntimeEnvironment(ctx context.Context, d EvidenceDeps, account kernel.AccountID, id kernel.ULID, values map[string]string) error {
	store := pg.New(d.DB, d.Keys)
	run, err := store.Lock(ctx, account, id)
	if err != nil {
		return err //nolint:wrapcheck // the production owner names the read
	}
	record := &runtimeconfig.Record{}
	overrides := make([]lifecycle.Override, 0, len(values))
	for _, name := range slices.Sorted(maps.Keys(values)) {
		record.Configuration.Environment = append(record.Configuration.Environment, runtimeconfig.Variable{Key: name})
		overrides = append(overrides, lifecycle.Override{Name: name, Value: values[name]})
	}
	if err := store.RecordResolved(ctx, run, record); err != nil {
		return err //nolint:wrapcheck // the production owner names the write
	}
	return store.SetOverrides(ctx, run, overrides) //nolint:wrapcheck // the owner seals the fixture values
}
