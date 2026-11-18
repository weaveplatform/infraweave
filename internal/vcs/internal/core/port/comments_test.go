package port_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitCommentMarker(t *testing.T) {
	t.Parallel()
	key := "plan:01TEST"
	marker, err := port.CommentMarker(key)
	if err != nil || marker != "<!-- infraweave:plan:01TEST -->" {
		t.Fatal(marker, err)
	}
	for _, body := range []string{marker, "heading\n" + marker, "heading\n" + marker + "\n"} {
		if !port.HasCommentMarker(body, key) {
			t.Fatalf("final footer not recognized: %q", body)
		}
	}
	for _, body := range []string{"> " + marker, "```\n" + marker + "\n```", "<details>\n" + marker + "\n</details>", marker + "\nother text", marker + "suffix", "prefix" + marker, marker + "\n\n", "<!-- infraweave:other -->"} {
		if port.HasCommentMarker(body, key) {
			t.Fatalf("log/quoted/different footer recognized: %q", body)
		}
	}
	for _, bad := range []string{"", "key\nnext", "key\rnext", "<!-- x -->", "key--x", "x>y", "space key", strings.Repeat("a", 257), "日本"} {
		if _, err := port.CommentMarker(bad); !errors.Is(err, port.ErrCommentInput) {
			t.Fatal("malformed key accepted", err)
		}
		if port.HasCommentMarker(marker, bad) {
			t.Fatal("malformed key matched")
		}
	}
}

func TestUnitCommentRequestLimits(t *testing.T) {
	t.Parallel()
	marker, _ := port.CommentMarker("plan:one") //nolint:errcheck // valid fixture
	body := "😀\n" + marker
	valid := port.CommentRequest{PullRequest: "1", DedupKey: "plan:one", Body: body}
	if err := port.ValidateCommentRequest(valid, port.CommentPresentation{MaxBytes: len(body), MaxRunes: len([]rune(body)), MaxUTF16: len([]rune(body)) + 1}); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []port.CommentPresentation{{MaxBytes: len(body) - 1}, {MaxRunes: len([]rune(body)) - 1}, {MaxUTF16: len([]rune(body))}} {
		if err := port.ValidateCommentRequest(valid, limit); !errors.Is(err, port.ErrCommentInput) {
			t.Fatal("over-budget accepted", err)
		}
	}
	for _, bad := range []port.CommentRequest{{PullRequest: "0", DedupKey: valid.DedupKey, Body: body}, {PullRequest: "1/2", DedupKey: valid.DedupKey, Body: body}, {PullRequest: "1", DedupKey: valid.DedupKey, Body: "log example: " + marker}, {PullRequest: "1", DedupKey: valid.DedupKey, Body: string([]byte{0xff}) + "\n" + marker}} {
		if err := port.ValidateCommentRequest(bad, port.CommentPresentation{}); !errors.Is(err, port.ErrCommentInput) {
			t.Fatal("bad request accepted", err)
		}
	}
}
