package gitlab_test

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/gitlab"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func commentRequest(key, body string) port.CommentRequest {
	marker, _ := port.CommentMarker(key) //nolint:errcheck // valid test fixture
	return port.CommentRequest{PullRequest: "7", DedupKey: key, Body: body + "\n" + marker}
}

func TestUnitComments(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	var provider port.Comments = l.gl
	r := commentRequest("plan:one", "first plan")
	first, err := provider.Post(t.Context(), l.repo.Repo, r)
	if err != nil || first.ID == "" {
		t.Fatal(first, err)
	}
	r.Body = commentRequest("plan:one", "changed plan").Body
	again, err := provider.Post(t.Context(), l.repo.Repo, r)
	if err != nil || again != first {
		t.Fatal("post did not recover", again, err)
	}
	got := l.fake.Comments("acme/platform", "infra", "7")
	if len(got) != 1 || got[0].Body == r.Body {
		t.Fatal("Post edited a recovered plan", got)
	}
	r.Existing = &first
	updated, err := provider.Upsert(t.Context(), l.repo.Repo, r)
	if err != nil || updated.ID != first.ID {
		t.Fatal("recorded edit", updated, err)
	}
	r.Existing = nil
	r.Body = commentRequest("plan:one", "notification refresh").Body
	recovered, err := provider.Upsert(t.Context(), l.repo.Repo, r)
	if err != nil || recovered.ID != first.ID {
		t.Fatal("marker edit", recovered, err)
	}
	stale := updated
	stale.ID = "999999"
	r.Existing = &stale
	r.Body = commentRequest("plan:one", "missing ref recovered").Body
	if ref, err := provider.Upsert(t.Context(), l.repo.Repo, r); err != nil || ref.ID != first.ID {
		t.Fatal("missing recorded identity", ref, err)
	}
	got = l.fake.Comments("acme/platform", "infra", "7")
	writes := l.fake.CommentWrites("acme/platform", "infra", "7")
	if len(got) != 1 || got[0].Body != r.Body || len(writes) != 4 || writes[0].Action != "create" || writes[1].Action != "update" || writes[2].Action != "update" ||
		writes[3].Action != "update" {
		t.Fatal("native edit order", got, writes)
	}
	r.Existing = nil
	r.PullRequest = "8"
	if _, err := provider.Post(t.Context(), l.repo.Repo, r); err != nil {
		t.Fatal(err)
	}
	if len(l.fake.Comments("acme/platform", "infra", "8")) != 1 || len(l.fake.Comments("acme/platform", "infra", "7")) != 1 {
		t.Fatal("pull request scope lost")
	}
	if _, err := provider.Post(t.Context(), l.repo.Missing, commentRequest("plan:foreign", "foreign")); !errors.Is(err, port.ErrNotFound) {
		t.Fatal("unreachable repository accepted", err)
	}
	second, err := provider.Post(t.Context(), l.repo.Repo, commentRequest("plan:two", "second run"))
	if err != nil || second.ID == "" {
		t.Fatal(second, err)
	}
	if err := provider.Minimize(t.Context(), l.repo.Repo, first); err != nil {
		t.Fatal(err)
	}
	if err := provider.Minimize(t.Context(), l.repo.Repo, first); err != nil {
		t.Fatal("minimize retry", err)
	}
	got = l.fake.Comments("acme/platform", "infra", "7")
	if len(got) != 2 || got[0].Minimized || got[1].Minimized {
		t.Fatal("unsupported minimization changed comments", got)
	}
	// Returned fixture snapshots cannot mutate the native server's body/state.
	got[0].Body = "tampered"
	if l.fake.Comments("acme/platform", "infra", "7")[0].Body == "tampered" {
		t.Fatal("snapshot aliases native state")
	}
}

func TestUnitCommentRecoveryWindow(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	var provider port.Comments = l.gl
	old := commentRequest("notification:old", "old")
	oldRef, err := provider.Post(t.Context(), l.repo.Repo, old)
	if err != nil {
		t.Fatal(err)
	}
	for n := range 1045 {
		l.fake.SeedComment("acme/platform", "infra", "7", "human discussion "+strconv.Itoa(n))
	}
	fresh := commentRequest("plan:fresh", "fresh")
	// An unrelated busy PR cannot prevent a fresh run's comment merely because
	// its recovery search deliberately inspects a finite newest-first window.
	freshRef, err := provider.Post(t.Context(), l.repo.Repo, fresh)
	if err != nil || freshRef.ID == "" {
		t.Fatal("busy request fresh create", freshRef, err)
	}
	old.Existing = &oldRef
	old.Body = commentRequest("notification:old", "edited beyond window").Body
	if _, err := provider.Upsert(t.Context(), l.repo.Repo, old); err != nil {
		t.Fatal("recorded edit beyond window", err)
	}
	l.fake.FailNextCommentCreate()
	lost := commentRequest("plan:lost", "lost create response")
	if _, err := provider.Post(t.Context(), l.repo.Repo, lost); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatal("unknown result not preserved", err)
	}
	before := len(l.fake.Comments("acme/platform", "infra", "7"))
	if _, err := provider.Post(t.Context(), l.repo.Repo, lost); err != nil {
		t.Fatal("recent marker recovery", err)
	}
	if len(l.fake.Comments("acme/platform", "infra", "7")) != before {
		t.Fatal("lost create duplicated")
	}
	marker, _ := port.CommentMarker("plan:quoted") //nolint:errcheck // fixture
	l.fake.SeedComment("acme/platform", "infra", "7", "```\n"+marker+"\n```")
	before = len(l.fake.Comments("acme/platform", "infra", "7"))
	if _, err := provider.Post(t.Context(), l.repo.Repo, commentRequest("plan:quoted", "real plan")); err != nil {
		t.Fatal(err)
	}
	if len(l.fake.Comments("acme/platform", "infra", "7")) != before+1 {
		t.Fatal("quoted log marker mistaken for footer")
	}
}

func TestUnitCommentRefusals(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	var provider port.Comments = l.gl
	before := l.fake.Requests()
	bad := commentRequest("plan:one", "body")
	bad.DedupKey = "bad\nkey"
	if _, err := provider.Post(t.Context(), l.repo.Repo, bad); !errors.Is(err, port.ErrCommentInput) || l.fake.Requests() != before {
		t.Fatal("bad input performed IO", err)
	}
	bad = commentRequest("plan:one", strings.Repeat("x", 1_000_001))
	if _, err := provider.Upsert(t.Context(), l.repo.Repo, bad); !errors.Is(err, port.ErrCommentInput) || l.fake.Requests() != before {
		t.Fatal("oversized input performed IO", err)
	}
	badRepo := l.repo.Repo
	in := *badRepo.Integration
	in.Credential = []byte("invalid")
	badRepo.Integration = &in
	if _, err := provider.Post(t.Context(), badRepo, commentRequest("plan:auth", "body")); !errors.Is(err, port.ErrCredential) {
		t.Fatal("credential refusal lost", err)
	}
	l.fake.Fail(gitlabfake.RateLimited, time.Minute)
	if _, err := provider.Post(t.Context(), l.repo.Repo, commentRequest("plan:rate", "body")); err == nil {
		t.Fatal("rate limit ignored")
	} else if _, ok := errors.AsType[*port.RateLimited](err); !ok {
		t.Fatal("rate limit lost", err)
	}
}

func TestUnitCommentTimeout(t *testing.T) {
	l := newLab(t)
	provider := gitlab.NewOver(memory{l.fake.Handler()})
	synctest.Test(t, func(t *testing.T) {
		l.fake.Fail(gitlabfake.Hang, 0)
		start := time.Now()
		_, err := provider.Post(context.Background(), l.repo.Repo, commentRequest("plan:timeout", "body"))
		if !errors.Is(err, kernel.ErrUnavailable) || time.Since(start) != gitlab.Timeout {
			t.Fatalf("unbounded comment call: %v after %s", err, time.Since(start))
		}
	})
}

func TestUnitCommentProtocolRefusals(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/notes") {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Next-Page", "1")
			_, _ = w.Write([]byte(`[]`))
			return
		}
		l.fake.Handler().ServeHTTP(w, r)
	})
	provider := gitlab.NewOver(memory{h})
	if _, err := provider.Post(t.Context(), l.repo.Repo, commentRequest("plan:partial", "body")); !errors.Is(err, port.ErrCommentSearchIncomplete) {
		t.Fatal("unreliable search allowed create", err)
	}
	if len(l.fake.Comments("acme/platform", "infra", "7")) != 0 {
		t.Fatal("partial search created native comment")
	}
	h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/notes") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		l.fake.Handler().ServeHTTP(w, r)
	})
	provider = gitlab.NewOver(memory{h})
	if _, err := provider.Post(t.Context(), l.repo.Repo, commentRequest("plan:invalid-success", "body")); !errors.Is(err, port.ErrCommentResponse) {
		t.Fatal("incomplete native success accepted", err)
	}
}
