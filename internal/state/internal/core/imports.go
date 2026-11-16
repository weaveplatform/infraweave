package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
)

// ActionImport is the audit action of a state import.
//
// ref: decision 0010 § "Upload, import and download URLs"
const ActionImport = "stack.state_import"

// invalidDetail is the detail of an import's *kernel.Invalid, whose
// field names the check that refused it.
const invalidDetail = "The state file cannot be imported."

// Blocked reports an import the stack's state refuses: the stack keeps
// no managed state, the caller does not hold its lock, or a run that may
// write its state is under way. Detail names what blocks it.
type Blocked struct{ Detail string }

func (b *Blocked) Error() string { return "state: " + b.Detail }

// Created opens st's default workspace, whose stored name is empty, in
// the creation's transaction, which the context carries, under the ID it
// keeps, so a stack whose state infraweave keeps lists it before any run
// locks it. When object is not empty it then imports the state file p
// uploaded under that object ID as the workspace's first version, its
// refusals pointing at stackObjectID; the stacks domain has decided
// STACK_MANAGED_STATE_IMPORT and that st keeps its state. It answers
// ErrNoTransaction outside a transaction.
//
// ref: decision 0010 § "Storage"
// ref: decision 0010 § "Upload, import and download URLs"
func (s *Service) Created(ctx context.Context, p kernel.Principal, st stacksapi.Stack, object string) error {
	if !tx.In(ctx) {
		return ErrNoTransaction
	}
	if err := s.store.OpenWorkspace(ctx, st.Account, st.ID, ""); err != nil {
		return err //nolint:wrapcheck // the store names the write
	}
	if object == "" {
		return nil
	}
	return s.importFile(ctx, p, accessapi.ClientAddr(ctx), st, object, "/stackObjectID")
}

// Import imports the state file p uploaded under object into the default
// workspace of p's account's stack slug, as a new current version marked
// imported by p, and records the import from remoteIP. It runs in one
// transaction that locks the stack's row FOR UPDATE, so no run is created
// on the stack and no state written to it until the import commits, and
// refuses, in order: a stack that keeps no managed state, one whose lock
// p does not hold and one with a tracked run, task or destroy run that
// has not ended, each a *Blocked; then an upload that is not p's or is
// gone, too large or no state, each a *kernel.Invalid pointing at
// /state. The route has decided STACK_MANAGED_STATE_IMPORT on the stack's
// space; stacksapi.ErrNotFound when p's account lacks the stack.
//
// ref: decision 0010 § "Upload, import and download URLs"
func (s *Service) Import(ctx context.Context, p kernel.Principal, remoteIP, slug, object string) error {
	st, err := s.stacks.BySlug(ctx, p.AccountID, slug)
	if err != nil {
		return err //nolint:wrapcheck // the stacks domain names the read
	}
	//nolint:wrapcheck // each step in the transaction names itself
	return tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		set, err := s.stacks.LockForRun(ctx, p.AccountID, st.ID)
		if err != nil {
			return err
		}
		if err := s.idle(ctx, p, set); err != nil {
			return err
		}
		return s.importFile(ctx, p, remoteIP, set.Stack, object, "/state")
	})
}

// idle refuses an import into set's stack unless it keeps its state, p
// holds its lock and no run that may write its state is under way: a
// proposed run writes none, so it does not count.
func (s *Service) idle(ctx context.Context, p kernel.Principal, set stacksapi.Settings) error {
	switch {
	case !set.ManagesState:
		return &Blocked{Detail: "The stack's state is not managed by infraweave."}
	case set.Lock == nil:
		return &Blocked{Detail: "The stack is not locked; lock it before importing its state."}
	case set.Lock.Holder != p.Caller():
		return &Blocked{Detail: fmt.Sprintf(
			"The stack is locked by %s; only the holder of its lock imports its state.",
			set.Lock.By)}
	}
	runs, err := s.runs.Unended(ctx, set.Account, set.ID)
	if err != nil {
		return err //nolint:wrapcheck // the runs domain names the read
	}
	for _, r := range runs {
		if r.Type.Blocking() {
			return &Blocked{Detail: fmt.Sprintf(
				"The stack's %s run %s has not finished; import its state once it has.",
				r.Type, r.ID)}
		}
	}
	return nil
}

// importFile reseals the state file p uploaded under object into st's
// default workspace as its new current version, marked imported by p,
// deletes the upload and records the import, all in the context's
// transaction, whatever the current version holds: an import is a
// person's act, recorded as one. The upload's row stays locked until the
// transaction ends, so a second import of it waits and then finds it
// gone. Its refusals are *kernel.Invalid at at: an object ID that names
// no upload of p's, a file over MaxUpload, and one that is neither a
// plain state of format version 4 nor client-encrypted. A refused file
// leaves the version's object pending, for blob.expire.
func (s *Service) importFile(ctx context.Context, p kernel.Principal, remoteIP string, st stacksapi.Stack, object, at string) error {
	id, err := kernel.ParseULID(object)
	if err != nil {
		return missing(at)
	}
	upload := UploadKey(st.Account, id, p.Caller())
	up, err := s.objects.LockLive(ctx, objectstore.Uploads, upload)
	switch {
	case errors.Is(err, objectstore.ErrNotFound):
		return missing(at)
	case err != nil:
		return err //nolint:wrapcheck // the objects name the key
	case up.Size > MaxUpload:
		return refused(at, kernel.ReasonMaximum,
			fmt.Sprintf("The state file is %d bytes; at most %d are imported.", up.Size, MaxUpload))
	}
	t := Target{Account: st.Account, Stack: st.ID}
	if err := s.store.OpenWorkspace(ctx, t.Account, t.Stack, t.Workspace); err != nil {
		return err //nolint:wrapcheck // the store names the write
	}
	if _, err := s.store.LockWorkspace(ctx, t); err != nil {
		return err //nolint:wrapcheck // the store names the lock
	}
	v, err := s.reseal(ctx, p, t, upload, up.Size, at)
	if err != nil {
		return err
	}
	if err := s.objects.Commit(ctx, objectstore.States, v.Key); err != nil {
		return err //nolint:wrapcheck // the objects name the key
	}
	if err := s.store.AddVersion(ctx, t.Account, v); err != nil {
		return err //nolint:wrapcheck // the store names the write
	}
	if err := s.objects.Delete(ctx, objectstore.Uploads, upload); err != nil {
		return err //nolint:wrapcheck // the objects name the key
	}
	return s.store.Audit(ctx, imported(p, remoteIP, v)) //nolint:wrapcheck // the store names it
}

// reseal streams the upload's size bytes into a new version of t's
// workspace, sealed in its own object of the states store, scanning them
// as they stream, and answers the version, marked imported by p now. A
// file that is no state an import takes is a *kernel.Invalid at at.
func (s *Service) reseal(ctx context.Context, p kernel.Principal, t Target, upload string, size int64, at string) (Version, error) {
	r, err := s.objects.Open(ctx, objectstore.Uploads, upload)
	if err != nil {
		return Version{}, err //nolint:wrapcheck // the objects name the key
	}
	defer r.Close()
	now := s.now()
	v := Version{Version: stateapi.Version{
		ID: kernel.NewULID(), Stack: t.Stack, Workspace: t.Workspace,
		Imported: true, ImportedUsername: new(username(p)), ImportedAt: &now,
	}}
	v.Key = versionKey(t, v.ID)
	sc := newScanner(r)
	obj, err := s.objects.Put(ctx, objectstore.States, v.Key, sc, size)
	m, scanErr := sc.result(err)
	switch {
	case err != nil:
		return Version{}, err //nolint:wrapcheck // the objects name the key
	case scanErr != nil || !m.importable():
		return Version{}, refused(at, kernel.ReasonRule,
			"The file is neither a JSON state of format version 4 nor a client-encrypted state.")
	}
	v.Size, v.SHA256 = obj.Size, obj.SHA256[:]
	v.Serial, v.Lineage, v.ClientEncrypted = m.serial, m.lineage, m.encrypted
	return v, nil
}

// missing is the refusal of an object ID that names no upload of the
// caller's: one another principal asked for, one imported already, or
// one expired, all alike, so a caller learns nothing of another's.
func missing(at string) error {
	return refused(at, kernel.ReasonNotFound, "No state file was uploaded under this object ID.")
}

// refused is a *kernel.Invalid at pointer for reason.
func refused(pointer, reason, message string) error {
	return &kernel.Invalid{Detail: invalidDetail, Errors: []kernel.FieldError{{
		Pointer: pointer, Reason: reason, Message: message,
	}}}
}

// username is the name an import and the audit trail record of p: its
// login, or its actor's ID when it has none.
func username(p kernel.Principal) string {
	if p.Login != "" {
		return p.Login
	}
	return p.ActorID.String()
}

// imported is the audit entry of p importing v into its stack, from
// remoteIP.
//
// ref: decision 0015 § "Audit trail"
func imported(p kernel.Principal, remoteIP string, v Version) kernel.AuditEntry {
	// Every field is a string or a boolean, so the metadata always
	// encodes.
	meta, _ := json.Marshal(struct {
		Workspace       string `json:"workspace"`
		Version         string `json:"versionId"`
		ClientEncrypted bool   `json:"clientEncrypted"`
	}{DefaultName, v.ID.String(), v.ClientEncrypted})
	return kernel.AuditEntry{
		AccountID: p.AccountID.ULID,
		Actor:     actorOf(p),
		Action:    ActionImport,
		EventType: "UPDATE",
		AffectedResource: kernel.AuditResource{
			Type: ResourceStack, ID: v.Stack.String(), Metadata: meta,
		},
		RemoteIP: remoteIP,
	}
}
