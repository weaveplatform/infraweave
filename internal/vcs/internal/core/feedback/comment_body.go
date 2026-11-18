package feedback

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var errCommentBudget = errors.New("VCS comment body budget cannot hold its heading and marker")

// PlanText is already masked by the worker. CutLines counts fully omitted
// lines; PartialLine records a shortened first retained line independently.
type PlanText struct {
	Text        string
	CutLines    int
	PartialLine bool
	Unavailable bool
}

type planHeading struct {
	Public  string
	Stack   string
	Run     kernel.ULID
	State   kernel.RunState
	Version int64
}

func planCommentBody(h planHeading, log PlanText, presentation port.CommentPresentation) (string, error) {
	key := "plan:" + h.Run.String()
	marker, err := port.CommentMarker(key)
	if err != nil || !utf8.ValidString(log.Text) {
		return "", port.ErrCommentInput
	}
	base := strings.TrimRight(h.Public, "/") + "/api/v1/"
	runURL := base + "runs/" + h.Run.String()
	stackURL := base + "stacks/" + url.PathEscape(h.Stack)
	logURL := runURL + "/logs-download-url?state=PLANNING&stateVersion=" + strconv.FormatInt(h.Version, 10)
	heading := fmt.Sprintf("### [%s](%s) · [%s](%s): %s\n\n", markdownText(h.Stack), stackURL, h.Run, runURL, h.State)
	if log.Unavailable && log.Text == "" {
		log.Text = "infraweave: this planning log is no longer available."
	}
	render := func(offset int) string {
		text := log.Text[offset:]
		cut := log.CutLines + strings.Count(log.Text[:offset], "\n")
		partial := offset == 0 && log.PartialLine || offset > 0 && log.Text[offset-1] != '\n'
		notice := ""
		if cut > 0 || partial {
			notice = fmt.Sprintf("%d lines omitted", cut)
			if partial {
				notice += "; first shown line shortened"
			}
			notice += ". [Full planning log](" + logURL + ").\n\n"
		}
		fence := commentFence(text)
		block := fence + "text\n" + text + "\n" + fence
		if presentation.Details {
			block = "<details>\n<summary>Planning log</summary>\n\n" + block + "\n\n</details>"
		}
		return heading + notice + block + "\n\n" + marker
	}
	valid := func(body string) bool {
		return port.ValidateCommentRequest(port.CommentRequest{PullRequest: "1", DedupKey: key, Body: body}, presentation) == nil
	}
	if body := render(0); valid(body) {
		return body, nil
	}
	if !valid(render(len(log.Text))) {
		return "", errCommentBudget
	}
	// Search byte offsets, rounding forward to a UTF-8 boundary. The retained
	// suffix and its required fence shrink together. Reserve notice digits too.
	lo, hi := 0, len(log.Text)
	for lo < hi {
		mid := lo + (hi-lo)/2
		offset := mid
		for offset < len(log.Text) && !utf8.RuneStart(log.Text[offset]) {
			offset++
		}
		if valid(render(offset)) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	for lo < len(log.Text) && !utf8.RuneStart(log.Text[lo]) {
		lo++
	}
	// Prefer whole lines when their suffix still fits. A single oversized line
	// retains its UTF-8-safe tail and explicitly reports the shortened line.
	if end := strings.IndexByte(log.Text[lo:], '\n'); end >= 0 && lo+end+1 < len(log.Text) {
		lo += end + 1
	}
	body := render(lo)
	if !valid(body) {
		return "", errCommentBudget
	}
	return body, nil
}

func commentFence(text string) string {
	longest, current := 2, 0
	for _, c := range text {
		if c == '`' {
			current++
			longest = max(longest, current)
		} else {
			current = 0
		}
	}
	return strings.Repeat("`", longest+1)
}

func markdownText(text string) string {
	return strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "`", "\\`", "\n", " ", "\r", " ").Replace(text)
}
