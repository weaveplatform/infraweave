package vcslab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/private"
)

// ErrHook is deliberately independent of provider response bodies or credentials.
var ErrHook = errors.New("vcs lab: GitHub App hook lifecycle failed")

// HookConfig contains precisely the plaintext properties returned by GitHub
// that the lab may change and restore. It cannot serialize a masked secret.
type HookConfig struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"` //nolint:tagliatelle // GitHub's native field name.
	InsecureSSL string `json:"insecure_ssl"` //nolint:tagliatelle // GitHub's native field name.
}

// AppHooks is an App-JWT-authenticated configuration client. It must omit the
// secret member entirely on every PATCH, including restore.
type AppHooks interface {
	Identity() string
	Get(context.Context) (HookConfig, error)
	Patch(context.Context, HookConfig) error
}

func (h HookConfig) valid() bool {
	u, err := url.Parse(h.URL)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" &&
		(h.ContentType == "json" || h.ContentType == "form") && h.InsecureSSL == "0"
}

// ParkingConfig is the dedicated App's explicitly attested baseline. It is
// operator configuration, never inferred from an arbitrary current destination.
func ParkingConfig(destination string) (HookConfig, error) {
	h := HookConfig{URL: destination, ContentType: "json", InsecureSSL: "0"}
	if !h.valid() {
		return HookConfig{}, fmt.Errorf("%w: invalid parked App configuration", ErrHook)
	}
	return h, nil
}

// HookLease points one already-active dedicated App at a scenario's integration.
// Journal is private and created before the first PATCH. Readiness attests the
// maintainer configured the derived secret; a signed delivery tests that fact.
type HookLease struct {
	client   AppHooks
	original HookConfig
	target   HookConfig
	journal  string
	identity string
}

type hookJournal struct {
	Identity string     `json:"identity"`
	Original HookConfig `json:"original"`
	Target   HookConfig `json:"target"`
}

// LeaseHook preserves GitHub's existing secret. It refuses an unknown setup
// before mutation, and restores even when PATCH's response is lost after effect.
func LeaseHook(ctx context.Context, client AppHooks, key AppKey, readiness string, expected HookConfig, target, journal string) (*HookLease, error) {
	if key.secret == "" || readiness == "" || readiness != key.Readiness || client.Identity() != key.Readiness {
		return nil, fmt.Errorf("%w: App signing-key setup is not ready", ErrHook)
	}
	original, err := client.Get(ctx)
	if err != nil || !original.valid() || !expected.valid() || original != expected {
		return nil, fmt.Errorf("%w: read restorable App configuration", ErrHook)
	}
	next := HookConfig{URL: target, ContentType: "json", InsecureSSL: "0"}
	if !next.valid() {
		return nil, fmt.Errorf("%w: invalid temporary destination", ErrHook)
	}
	lease := &HookLease{client: client, original: original, target: next, journal: journal, identity: key.Readiness}
	data, err := json.Marshal(hookJournal{Identity: key.Readiness, Original: original, Target: next})
	if err != nil {
		return nil, fmt.Errorf("%w: encode cleanup journal", ErrHook)
	}
	f, err := os.OpenFile(journal, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("%w: create cleanup journal", ErrHook)
	}
	if err := private.Restrict(journal); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%w: restrict cleanup journal", ErrHook)
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return nil, fmt.Errorf("%w: write cleanup journal", ErrHook)
	}
	if err := client.Patch(ctx, next); err != nil {
		return nil, errors.Join(fmt.Errorf("%w: point App webhook", ErrHook), lease.Restore(ctx))
	}
	got, err := client.Get(ctx)
	if err != nil || got != next {
		return nil, errors.Join(fmt.Errorf("%w: verify temporary configuration", ErrHook), lease.Restore(ctx))
	}
	return lease, nil
}

// Restore uses fresh time for cleanup after cancellation, verifies every changed
// property, and removes the journal only after confirmed success. GitHub's
// permanent App hook returns to its parked baseline; it is never deleted.
func (l *HookLease) Restore(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if l.identity == "" || l.client.Identity() != l.identity {
		return fmt.Errorf("%w: cleanup App identity changed", ErrHook)
	}
	current, err := l.client.Get(ctx)
	if err != nil {
		return fmt.Errorf("%w: read current cleanup owner", ErrHook)
	}
	if current != l.target && current != l.original {
		return fmt.Errorf("%w: current hook belongs to another session", ErrHook)
	}
	// GET followed by PATCH is not a native compare-and-swap. Workflow and
	// scenario serialization prevent other lab owners; this check prevents a
	// stale journal from knowingly replacing somebody else's configuration.
	if current != l.original {
		if err := l.client.Patch(ctx, l.original); err != nil {
			return fmt.Errorf("%w: restore App configuration", ErrHook)
		}
	}
	got, err := l.client.Get(ctx)
	if err != nil || got != l.original {
		return fmt.Errorf("%w: verify restored App configuration", ErrHook)
	}
	if err := os.Remove(l.journal); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: remove cleanup journal", ErrHook)
	}
	return nil
}

// RestoreHook resumes cleanup from a private journal left by a failed scenario.
func RestoreHook(ctx context.Context, client AppHooks, journal string) error {
	data, err := os.ReadFile(journal)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var config hookJournal
	if err != nil || json.Unmarshal(data, &config) != nil || config.Identity == "" || config.Identity != client.Identity() || !config.Original.valid() || !config.Target.valid() {
		return fmt.Errorf("%w: read cleanup journal", ErrHook)
	}
	return (&HookLease{client: client, original: config.Original, target: config.Target, journal: journal, identity: config.Identity}).Restore(ctx)
}
