package exec

import (
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/agent/workspace"
	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
)

// Uploads declares, puts and records a run's artifacts; *client.Client is
// one.
type Uploads interface {
	UploadURLs(
		ctx context.Context,
		token, run string,
		in wire.UploadURLsRequest,
	) ([]wire.SignedUpload, error)
	PutFile(ctx context.Context, u wire.SignedUpload, path string) error
	Artifacts(ctx context.Context, token, run string, in wire.ArtifactsRequest) error
}

// The names planning's results are saved under, which the server reads
// the plan's JSON and changes by.
//
// ref: decision 0008 § "Logs and artifacts"
const (
	PlanArtifact    = "plan.json"
	ChangesArtifact = "changes.json"
	WorkspaceName   = "workspace"
)

// errUpload marks uploads the worker API answered other than as declared.
var errUpload = errors.New("the worker API answered other uploads than declared")

// upload is one sealed file to declare, put and record.
type upload struct {
	kind wire.UploadKind
	name string
	path string
	size int64
	sum  string
}

// save keeps planning's results at version before its succeeded report:
// the plan's JSON and its changes, sealed with the run's artifact key so
// the server opens them, and, for a tracked run whose plan changes
// something, which may wait for the plan's confirmation and apply in
// another container, the workspace sealed to the pool's key, which the
// server cannot open. It answers the note of what failed, empty when
// nothing did.
//
// ref: decision 0008 § "Logs and artifacts"
// ref: decision 0008 § "The run specification and its secrets"
func (r *run) save(ctx context.Context, version int64, p plan.Plan) string {
	acct, err := kernel.ParseAccountID(r.h.Spec.Run.Account)
	if err != nil {
		return fmt.Sprintf("The run's specification names no account: %v.", err)
	}
	id, err := kernel.ParseULID(r.h.Run)
	if err != nil {
		return fmt.Sprintf("The run's ID is not a ULID: %v.", err)
	}
	key := func(name string) string {
		return kernel.RunObjectKey(acct, id, kernel.RunStatePlanning, version, name)
	}
	ups := make([]upload, 0, 3)
	for _, a := range []struct {
		name  string
		write func(io.Writer) error
	}{
		{PlanArtifact, func(w io.Writer) error { return copyFile(w, r.options.PlanJSON) }},
		{ChangesArtifact, func(w io.Writer) error { return plan.Write(w, p.Changes) }},
	} {
		u, err := r.sealArtifact(key(a.name), a.name, a.write)
		if err != nil {
			return fmt.Sprintf("The plan's %s could not be sealed: %v.", a.name, err)
		}
		ups = append(ups, u)
	}
	if r.h.Spec.Run.Type == wire.TRACKED && changes(p.Delta) {
		u, err := r.sealWorkspace(version)
		if err != nil {
			return fmt.Sprintf("The workspace could not be saved for the plan's confirmation: %v.",
				err)
		}
		ups = append(ups, u)
	}
	if err := r.send(ctx, version, ups); err != nil {
		return fmt.Sprintf("The plan's results could not be uploaded: %v.", err)
	}
	return ""
}

// changes reports whether a plan with delta d changes anything, as the
// server's plan decision counts it: a tracked run with changes waits for
// confirmation or applies, and any other finishes.
func changes(d plan.Delta) bool {
	return d.Add > 0 || d.Change > 0 || d.Delete > 0 || d.Import > 0 || d.Move > 0 ||
		d.Forget > 0 || d.Outputs
}

// copyFile copies the file at path to w.
func copyFile(w io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err //nolint:wrapcheck // names the path
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err //nolint:wrapcheck // the caller names the artifact
}

// sealArtifact writes name's content, sealed in the object stream under
// the content key of its object key, to a file of the run's temporary
// directory, and answers its upload.
func (r *run) sealArtifact(object, name string, write func(io.Writer) error) (upload, error) {
	content, err := seal.ArtifactKey(r.h.Secrets.ArtifactKey, object)
	if err != nil {
		return upload{}, err //nolint:wrapcheck // seal names the key
	}
	return r.sealed(wire.UploadKindArtifact, name, func(w io.Writer) error {
		sw, err := seal.NewWriter(w, content)
		if err != nil {
			return err //nolint:wrapcheck // seal names the key
		}
		if err := write(sw); err != nil {
			return err
		}
		return sw.Close() //nolint:wrapcheck // seal names the write
	})
}

// sealWorkspace saves the workspace sealed to the pool's key for the run
// at version, leaving out what workspace.Write does and naming each such
// entry in the run's log, and answers its upload. No file of it may hold
// the run token or the source credential's token.
func (r *run) sealWorkspace(version int64) (upload, error) {
	pool, err := ecdh.P256().NewPublicKey(r.h.PoolKey)
	if err != nil {
		return upload{}, fmt.Errorf("the pool's key: %w", err)
	}
	banned := [][]byte{[]byte(r.h.RunToken)}
	if c := r.h.Secrets.SourceCredential; c != nil {
		banned = append(banned, []byte(c.Token))
	}
	var left []string
	u, err := r.sealed(wire.UploadKindWorkspace, WorkspaceName, func(w io.Writer) error {
		var err error
		left, err = workspace.Write(w, r.d.Workspace, workspace.Options{
			Pool: pool, Run: r.h.Run, StateVersion: version, Forbidden: banned,
		})
		return err //nolint:wrapcheck // workspace names the entry
	})
	for _, p := range left {
		_, _ = fmt.Fprintf(r.log, "infraweave: the workspace's %s is not saved: it is a link "+
			"that leaves the workspace, a device, a pipe or a socket\n", p)
	}
	if err == nil {
		_, _ = fmt.Fprintf(r.log, "infraweave: saved the workspace for the plan's "+
			"confirmation, %d bytes sealed to the pool's key\n", u.size)
	}
	return u, err
}

// sealed writes what write writes to a new file of the run's temporary
// directory, counting and hashing it, and answers its upload of kind as
// name.
func (r *run) sealed(
	kind wire.UploadKind,
	name string,
	write func(io.Writer) error,
) (upload, error) {
	path := filepath.Join(r.temp, name+".sealed")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return upload{}, err //nolint:wrapcheck // names the path
	}
	sum := sha256.New()
	c := &counter{w: io.MultiWriter(f, sum)}
	err = write(c)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return upload{}, err
	}
	return upload{kind: kind, name: name, path: path, size: c.n, sum: digest(sum)}, nil
}

// digest is h's sum in hex.
func digest(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }

// counter counts what it writes to w.
type counter struct {
	w io.Writer
	n int64
}

func (c *counter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err //nolint:wrapcheck // the caller's writer
}

// send declares, puts and records ups at version, taking each step again
// while the worker API or the store cannot take it for now, for up to
// reportRetry, as a report is: a declaration again, which signs the same
// objects, every PUT again after a new declaration, and a record alone,
// which skips what was recorded.
func (r *run) send(ctx context.Context, version int64, ups []upload) error {
	decl := wire.UploadURLsRequest{StateVersion: version, Uploads: make([]wire.Upload, len(ups))}
	rec := wire.ArtifactsRequest{
		StateVersion: version, Artifacts: make([]wire.ArtifactRecord, len(ups)),
	}
	for i, u := range ups {
		decl.Uploads[i] = wire.Upload{Kind: u.kind, Name: u.name, Size: u.size, Sha256: u.sum}
		rec.Artifacts[i] = wire.ArtifactRecord{Kind: wire.ArtifactRecordKind(u.kind), Name: u.name}
	}
	deadline := time.Now().Add(reportRetry)
	wait := reportWait
	uploaded := false
	for {
		err := r.transfer(ctx, decl, rec, ups, &uploaded)
		switch {
		case err == nil:
			return nil
		case !transient(ctx, err), time.Now().Add(wait).After(deadline):
			return err
		}
		r.d.Log.WarnContext(ctx, "artifacts not taken", "run", r.h.Run, "stateVersion", version,
			"error", err, "retry", wait)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return fmt.Errorf("upload the artifacts: %w", ctx.Err())
		case <-t.C:
		}
		wait = min(wait*2, reportWaitMax)
	}
}

// transfer takes ups through the worker API once: their declaration and
// PUTs, unless uploaded says they were taken, and their record.
func (r *run) transfer(
	ctx context.Context,
	decl wire.UploadURLsRequest,
	rec wire.ArtifactsRequest,
	ups []upload,
	uploaded *bool,
) error {
	if !*uploaded {
		signed, err := r.d.Uploads.UploadURLs(ctx, r.h.RunToken, r.h.Run, decl)
		if err != nil {
			return err //nolint:wrapcheck // client names the call
		}
		if len(signed) != len(ups) {
			return errUpload
		}
		for i, s := range signed {
			if s.Kind != ups[i].kind || s.Name != ups[i].name {
				return errUpload
			}
			if err := r.d.Uploads.PutFile(ctx, s, ups[i].path); err != nil {
				return err //nolint:wrapcheck // client names the upload
			}
		}
		*uploaded = true
	}
	err := r.d.Uploads.Artifacts(ctx, r.h.RunToken, r.h.Run, rec)
	return err //nolint:wrapcheck // client names the call
}
