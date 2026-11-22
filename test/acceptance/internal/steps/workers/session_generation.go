package workers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

type workerLiveness struct {
	seen          time.Time
	available     *time.Time
	busy, offline bool
	serial        int64
}

func (w *workerSteps) workerLiveness(ctx context.Context, name string) (workerLiveness, error) {
	var out workerLiveness
	err := w.w.Row(ctx, `SELECT last_seen_at,available_at,busy,offline,serial FROM workers
		WHERE account_id=$1 AND id=$2`, []any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(w.w.IDs[name])},
		&out.seen, &out.available, &out.busy, &out.offline, &out.serial)
	return out, err
}

func (w *workerSteps) keepLiveness(ctx context.Context, name, key string) error {
	out, err := w.workerLiveness(ctx, name)
	if err == nil {
		w.liveness[key] = out
	}
	return err
}

func (w *workerSteps) sameLiveness(ctx context.Context, name, key string) error {
	before, ok := w.liveness[key]
	if !ok {
		return fmt.Errorf("%w: no liveness snapshot", harness.ErrStep)
	}
	after, err := w.workerLiveness(ctx, name)
	if err != nil {
		return err
	}
	availableEqual := before.available == nil && after.available == nil || before.available != nil && after.available != nil && before.available.Equal(*after.available)
	if !before.seen.Equal(after.seen) || !availableEqual || before.busy != after.busy || before.offline != after.offline || before.serial != after.serial {
		return fmt.Errorf("%w: revoked session changed worker liveness", harness.ErrStep)
	}
	return nil
}

// Inspect only expected claims; the ordinary protocol requests independently
// verify these actual server-issued signatures, audiences, kinds and expiry.
func (w *workerSteps) tokenGeneration(name, certificate string) error {
	parts := strings.Split(w.w.Tokens[name], ".")
	if len(parts) != 3 {
		return fmt.Errorf("%w: worker token is not a JWT", harness.ErrStep)
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("decode worker claims: %w", err)
	}
	var claims map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&claims); err != nil {
		return fmt.Errorf("decode worker claims: %w", err)
	}
	serial, ok := claims["iw_worker_serial"].(json.Number)
	if !ok {
		return fmt.Errorf("%w: worker serial claim absent", harness.ErrStep)
	}
	value, err := serial.Int64()
	if err != nil || value <= 0 || value != w.certs[certificate].cert.SerialNumber.Int64() || claims["iw_kind"] != "worker" || claims["aud"] != "infraweave-worker" ||
		claims["sub"] != w.w.IDs[name] {
		return fmt.Errorf("%w: worker token generation or audience mismatch", harness.ErrStep)
	}
	return nil
}

func (w *workerSteps) keepRenewed(name, worker string) error {
	var out wire.PollResult
	r := w.w.Last()
	if r.Status != 200 || json.Unmarshal(r.Body, &out) != nil || out.SessionToken == nil || *out.SessionToken == "" || out.SessionExpiresAt == nil {
		return fmt.Errorf("%w: renewal response missing", harness.ErrStep)
	}
	w.w.Tokens[name] = *out.SessionToken
	w.w.IDs[name] = w.w.IDs[worker]
	return nil
}
