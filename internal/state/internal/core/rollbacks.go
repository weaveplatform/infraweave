package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
)

// ActionRollback is the audit action of a state rollback.
//
// ref: decision 0010 § "Rollback, searches and retention"
const ActionRollback = "stack.state_rollback"

// ErrNoVersion reports a rollback's target that the stack's default
// workspace does not hold: an ID that names no version of it, or one
// that retention has deleted.
var ErrNoVersion = errors.New("state: no such state version")

// Rollback writes a copy of version, a version of the default workspace
// of p's account's stack slug, as the workspace's new current version,
// marked as p's rollback from it, and records the rollback from
// remoteIP. It runs in one transaction that locks the stack's row FOR
// UPDATE, as an import does, and refuses in the same order: a stack that
// keeps no managed state, one whose lock p does not hold and one with a
// tracked run, task or destroy run that has not ended, each a *Blocked;
// then a version the workspace does not hold, ErrNoVersion; then the
// current version, a *Blocked. The copy is resealed into an object of
// its own and keeps the version's run, serial, lineage, encryption and
// import, so a rollback to an imported version answers who imported it
// and when. It answers the new version. The route has decided
// STACK_MANAGED_STATE_ROLLBACK on the stack's space;
// stacksapi.ErrNotFound when p's account lacks the stack.
//
// ref: decision 0010 § "Rollback, searches and retention"
func (s *Service) Rollback(ctx context.Context, p kernel.Principal, remoteIP, slug, version string) (Version, error) {
	st, err := s.stacks.BySlug(ctx, p.AccountID, slug)
	if err != nil {
		return Version{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	var out Version
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		set, err := s.stacks.LockForRun(ctx, p.AccountID, st.ID)
		if err != nil {
			return err //nolint:wrapcheck // the stacks domain names the lock
		}
		if err := s.idle(ctx, p, set, rollingBack); err != nil {
			return err
		}
		t := Target{Account: set.Account, Stack: set.ID}
		from, err := s.target(ctx, t, version)
		if err != nil {
			return err
		}
		v, err := s.copyOf(ctx, p, t, from)
		if err != nil {
			return err
		}
		if err := s.objects.Commit(ctx, objectstore.States, v.Key); err != nil {
			return err //nolint:wrapcheck // the objects name the key
		}
		if err := s.store.AddVersion(ctx, t.Account, v); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		if err := s.store.Audit(ctx, rolledBack(p, remoteIP, v)); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		// The row answers the time the database wrote it at, which the
		// search orders by.
		out, _, err = s.store.Version(ctx, t.Account, v.ID)
		return err //nolint:wrapcheck // the store names the read
	})
	return out, err //nolint:wrapcheck // each step in the transaction names itself
}

// target is the version raw names of t's workspace, which it locks, so
// no write moves its current version until the transaction ends:
// ErrNoVersion when raw names none of it, and a *Blocked when it is the
// current version, since a copy of it would change nothing.
func (s *Service) target(ctx context.Context, t Target, raw string) (Version, error) {
	id, err := kernel.ParseULID(raw)
	if err != nil {
		return Version{}, fmt.Errorf("%w: %q", ErrNoVersion, raw)
	}
	from, ok, err := s.store.Version(ctx, t.Account, id)
	switch {
	case err != nil:
		return Version{}, err //nolint:wrapcheck // the store names the read
	case !ok || from.Stack != t.Stack || from.Workspace != t.Workspace:
		return Version{}, fmt.Errorf("%w: %s", ErrNoVersion, id)
	}
	if _, err := s.store.LockWorkspace(ctx, t); err != nil {
		return Version{}, err //nolint:wrapcheck // the store names the lock
	}
	cur, ok, err := s.store.Current(ctx, t)
	switch {
	case err != nil:
		return Version{}, err //nolint:wrapcheck // the store names the read
	case ok && cur.ID == from.ID:
		return Version{}, &Blocked{Detail: fmt.Sprintf(
			"The state version %s is the stack's current version; roll back to an earlier one.",
			from.ID)}
	}
	return from, nil
}

// copyOf reseals from's contents into a new version of t's workspace, in
// its own object of the states store, which keeps from's run, serial,
// lineage, encryption and import, marked as p's rollback from it. An
// object that is gone, as retention deletes one, is ErrNoVersion.
func (s *Service) copyOf(ctx context.Context, p kernel.Principal, t Target, from Version) (Version, error) {
	r, err := s.objects.Open(ctx, objectstore.States, from.Key)
	if errors.Is(err, objectstore.ErrNotFound) {
		return Version{}, fmt.Errorf("%w: %s", ErrNoVersion, from.ID)
	}
	if err != nil {
		return Version{}, err //nolint:wrapcheck // the objects name the key
	}
	defer r.Close()
	v := Version{Version: stateapi.Version{
		ID: kernel.NewULID(), Stack: t.Stack, Workspace: t.Workspace,
		Serial: from.Serial, Lineage: from.Lineage, ClientEncrypted: from.ClientEncrypted,
		Run: from.Run, Imported: from.Imported, ImportedUsername: from.ImportedUsername,
		ImportedAt: from.ImportedAt,
		Rollback:   true, RollbackUsername: new(username(p)), RolledBackFrom: &from.ID,
	}}
	v.Key = versionKey(t, v.ID)
	obj, err := s.objects.Put(ctx, objectstore.States, v.Key, r, from.Size)
	if err != nil {
		return Version{}, err //nolint:wrapcheck // the objects name the key
	}
	v.Size, v.SHA256 = obj.Size, obj.SHA256[:]
	return v, nil
}

// rolledBack is the audit entry of p rolling v's stack's default
// workspace back to the version v copies, from remoteIP.
//
// ref: decision 0015 § "Audit trail"
func rolledBack(p kernel.Principal, remoteIP string, v Version) kernel.AuditEntry {
	// Every field is a string, so the metadata always encodes.
	meta, _ := json.Marshal(struct {
		Workspace string `json:"workspace"`
		Version   string `json:"versionId"`
		From      string `json:"rolledBackFrom"`
	}{DefaultName, v.ID.String(), v.RolledBackFrom.String()})
	return kernel.AuditEntry{
		AccountID: p.AccountID.ULID,
		Actor:     actorOf(p),
		Action:    ActionRollback,
		EventType: "UPDATE",
		AffectedResource: kernel.AuditResource{
			Type: ResourceStack, ID: v.Stack.String(), Metadata: meta,
		},
		RemoteIP: remoteIP,
	}
}
