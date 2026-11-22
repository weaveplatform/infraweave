package pg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/policies/internal/sampling"
)

func (w *sampleWorld) uploadAll(t *testing.T) {
	t.Helper()
	for range 10 {
		jobs, err := w.q.Claim(t.Context(), sampling.UploadKind.Queue, []string{sampling.UploadKind.Name}, 1, time.Minute, "sample-cleanup-test")
		if err != nil {
			t.Fatal(err)
		}
		if len(jobs) == 0 {
			return
		}
		if err := w.samples.Upload(t.Context(), jobs[0]); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("upload deliveries did not settle")
}

func (w *sampleWorld) inputState(t *testing.T, id kernel.ULID, state string, published bool) {
	t.Helper()
	input, err := w.f.store.InspectInput(t.Context(), w.f.principal.AccountID, id, false)
	if err != nil || input.State != state || (input.PublishedAt != nil) != published || len(input.Staging.Reveal()) != 0 {
		t.Fatalf("input retirement: %s/%t want %s/%t: %v", input.State, input.PublishedAt != nil, state, published, err)
	}
	var staged bool
	if err := w.f.db.QueryRow(t.Context(), `SELECT staging_sealed IS NOT NULL FROM policy_input_objects WHERE account_id=$1 AND id=$2`, input.Account, id).
		Scan(&staged); err != nil ||
		staged {
		t.Fatalf("retired private staging remains: %t %v", staged, err)
	}
	if readable, err := w.objects.Available(t.Context(), objectstore.PolicyInputs, input.Key, w.f.now); err != nil || readable {
		t.Fatalf("retired object remains available: %t %v", readable, err)
	}
}

func TestAccSampleCleanupExpiresPublishedInputsAndErasesUnpublishedStaging(t *testing.T) {
	w := newSampleWorld(t)
	evaluation := w.evaluate(t)
	id := evaluation.Receipts[0].ID
	w.uploadAll(t)
	current, pending := w.captureHead(t)
	if current == nil || pending != nil {
		t.Fatal("real evaluation did not publish its independent capture")
	}
	w.f.now = time.Now().UTC().Add(sampling.Retention + time.Minute)
	if err := w.samples.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	w.inputState(t, id, "expired", true)
	w.inputState(t, *current, "expired", true)
	statuses, err := w.samples.SampleStatuses(t.Context(), w.f.principal.AccountID, []kernel.ULID{id})
	if err != nil || statuses[id].Sampled || !statuses[id].Expired {
		t.Fatalf("published sample lost its expired tombstone: %+v %v", statuses, err)
	}
	if _, err := w.samples.Read(t.Context(), w.f.principal.AccountID, id); !errors.Is(err, sampling.ErrNotFound) {
		t.Fatalf("expired sample can still be read: %v", err)
	}
	if err := w.samples.Cleanup(t.Context()); err != nil {
		t.Fatalf("repeated expiry is not resumable: %v", err)
	}

	unpublished := newSampleWorld(t)
	never := unpublished.evaluate(t).Receipts[0].ID
	// Cleanup must not need the very private bytes it is meant to erase.
	if _, err := unpublished.f.db.Exec(
		t.Context(),
		`UPDATE policy_input_objects SET staging_sealed=decode('00','hex') WHERE account_id=$1`,
		unpublished.f.principal.AccountID,
	); err != nil {
		t.Fatal(err)
	}
	unpublished.f.now = time.Now().UTC().Add(sampling.Retention + time.Minute)
	if err := unpublished.samples.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	unpublished.inputState(t, never, "failed", false)
	statuses, err = unpublished.samples.SampleStatuses(t.Context(), unpublished.f.principal.AccountID, []kernel.ULID{never})
	if err != nil || statuses[never].Sampled || statuses[never].Expired {
		t.Fatalf("unpublished evidence became an expired sample: %+v %v", statuses, err)
	}
}

func TestAccSampleCleanupReclaimsDeadCancelledAndPrunedUploadStaging(t *testing.T) {
	for _, state := range []string{"dead", "cancelled", "pruned"} {
		t.Run(state, func(t *testing.T) {
			w := newSampleWorld(t)
			id := w.evaluate(t).Receipts[0].ID
			job := w.claim(t)
			if err := w.q.Kill(t.Context(), job, errors.New("test delivery cannot continue")); err != nil {
				t.Fatal(err)
			}
			if state != "dead" {
				if err := w.q.Discard(t.Context(), job.ID); err != nil {
					t.Fatal(err)
				}
			}
			if state == "pruned" {
				if _, err := w.f.db.Exec(t.Context(), `DELETE FROM jobs WHERE id=$1`, job.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.samples.Cleanup(t.Context()); err != nil {
				t.Fatal(err)
			}
			w.inputState(t, id, "failed", false)
			// A different, still-pending capture upload is not swept merely
			// because the sample delivery has failed.
			_, pending := w.captureHead(t)
			if pending == nil {
				t.Fatal("live capture delivery was swept with failed sample")
			}
			input, err := w.f.store.Input(t.Context(), w.f.principal.AccountID, *pending, false)
			if err != nil || input.State != "pending" || len(input.Staging.Reveal()) == 0 {
				t.Fatalf("unrelated delivery was erased: %s %v", input.State, err)
			}
		})
	}
}

func TestAccPolicyDeletionRetiresSamplesWithoutResettingQuotaOrSharedCapture(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(map[bool]string{false: "unpublished", true: "published"}[published], func(t *testing.T) {
			w := newSampleWorld(t)
			receipt := w.evaluate(t).Receipts[0]
			if published {
				w.uploadAll(t)
			}
			if _, err := w.f.svc.Delete(t.Context(), w.f.principal, receipt.Policy.String(), kernel.Expect{}, true); err != nil {
				t.Fatal(err)
			}
			statuses, err := w.samples.SampleStatuses(t.Context(), w.f.principal.AccountID, []kernel.ULID{receipt.ID})
			if err != nil || statuses[receipt.ID].Sampled || statuses[receipt.ID].Expired != published {
				t.Fatalf("deleted policy input visible before cleanup: %+v %v", statuses, err)
			}
			jobs, err := w.q.Claim(t.Context(), sampling.DeletedPolicyKind.Queue, []string{sampling.DeletedPolicyKind.Name}, 1, time.Minute, "deleted-policy-test")
			if err != nil || len(jobs) != 1 {
				t.Fatalf("deletion did not enqueue owned retirement: %d %v", len(jobs), err)
			}
			if err := w.samples.RetirePolicySamples(t.Context(), jobs[0]); err != nil {
				t.Fatal(err)
			}
			if err := w.q.Complete(t.Context(), jobs[0]); err != nil {
				t.Fatal(err)
			}
			state := "failed"
			if published {
				state = "expired"
			}
			w.inputState(t, receipt.ID, state, published)
			w.uploadAll(t)
			current, pending := w.captureHead(t)
			if current == nil || pending != nil {
				t.Fatal("individual policy deletion erased shared capture")
			}
			capture, err := w.f.store.Input(t.Context(), w.f.principal.AccountID, *current, false)
			if err != nil || capture.State != "live" {
				t.Fatalf("shared capture no longer live: %s %v", capture.State, err)
			}
			var used, metadata int
			if err := w.f.db.QueryRow(t.Context(), `SELECT (SELECT used FROM policy_sample_budgets WHERE account_id=$1 AND bucket='OTHER'),(SELECT count(*) FROM policy_evaluations WHERE account_id=$1)`, w.f.principal.AccountID).
				Scan(&used, &metadata); err != nil ||
				used != 1 ||
				metadata != 1 {
				t.Fatalf("policy deletion reset quota or lost receipt metadata: %d/%d %v", used, metadata, err)
			}
		})
	}
}

func TestAccAccountEvidencePurgeRemovesAllOwnedReferencesAndLateJobsSettle(t *testing.T) {
	w := newSampleWorld(t)
	w.evaluate(t)
	job := w.claim(t)
	// The claimed uploader has not yet put bytes. Purge must remove its
	// private staging, and its resumed generation must settle as missing.
	account := w.f.principal.AccountID
	if err := w.samples.PurgeAccount(t.Context(), account); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"policy_input_objects", "policy_evaluations", "policy_input_capture_heads", "policy_sample_budgets"} {
		var count int
		if err := w.f.db.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE account_id=$1", account).Scan(&count); err != nil || count != 0 {
			t.Fatalf("account purge retained %s: %d %v", table, count, err)
		}
	}
	if err := w.samples.Upload(t.Context(), job); err != nil {
		t.Fatalf("late upload did not settle missing owner: %v", err)
	}
	w.uploadAll(t)
	if err := w.samples.PurgeAccount(context.WithoutCancel(t.Context()), account); err != nil {
		t.Fatalf("repeated account purge: %v", err)
	}
}
