package runs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// logSteps write a run's log as its worker does, through the worker API
// and the store's signed URLs, and read it back.
type logSteps struct {
	w *harness.World
}

// quoted is a step's string with Go's escapes, such as \n and \x1b,
// which a log's lines and escape sequences need.
const quoted = `"((?:[^"\\]|\\.)*)"`

// store is the client of the object store's signed URLs, which take no
// credential.
var store = &http.Client{Timeout: 30 * time.Second}

func (l *logSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^token "(\w+)" writes log chunk (\d+) of the run "(\w+)" in state version (\d+) `+
		`at (\d+) holding `+quoted+` through ([AB])$`, l.write)
	sc.Step(`^the answer's nextToken is kept as "(\w+)"$`, l.keep)
	sc.Step(`^token "(\w+)" eventually reads a download URL of the run "(\w+)"'s ([A-Z_]+) log `+
		`through ([AB])$`, l.eventually)
	sc.Step(`^the answer's url downloads `+quoted+`$`, l.downloads)
}

// unquote is s, a step's quoted string, with its escapes applied.
func unquote(s string) (string, error) {
	out, err := strconv.Unquote(`"` + s + `"`)
	if err != nil {
		return "", fmt.Errorf("%w: the string %q: %w", harness.ErrStep, s, err)
	}
	return out, nil
}

// write uploads text as log chunk seq of the run kept as run, in state
// version version, with the run token kept as token: it declares the
// chunk to upload-urls, PUTs it to the signed URL with the headers the
// answer names, and commits it to log-chunks as written at at, in Unix
// seconds. The commit's answer is the last.
func (l *logSteps) write(
	ctx context.Context,
	token string,
	seq int64,
	run string,
	version, at int64,
	text, replica string,
) error {
	body, err := unquote(text)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(body))
	path := "/worker/v1/runs/" + l.w.IDs[run]
	decl, err := json.Marshal(map[string]any{
		"stateVersion": version,
		"uploads": []map[string]any{{
			"kind": "log-chunk", "name": strconv.FormatInt(seq, 10), "size": len(body),
			"sha256": hex.EncodeToString(sum[:]),
		}},
	})
	if err != nil {
		return fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	r, err := l.w.Do(ctx, replica, token, http.MethodPost, path+"/upload-urls",
		json.RawMessage(decl))
	if err != nil && r.Status == 0 {
		return err //nolint:wrapcheck // names the request
	}
	var signed struct {
		Uploads []struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"uploads"`
	}
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &signed) != nil ||
		len(signed.Uploads) != 1 {
		return fmt.Errorf("%w: upload-urls answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, signed.Uploads[0].URL,
		strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	for k, v := range signed.Uploads[0].Headers {
		req.Header.Set(k, v)
	}
	resp, err := store.Do(req)
	if err != nil {
		return fmt.Errorf("%w: put the chunk: %w", harness.ErrStep, err)
	}
	answer, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%w: the chunk's PUT answered %d %s", harness.ErrStep, resp.StatusCode,
			answer)
	}
	commit, err := json.Marshal(map[string]any{
		"stateVersion": version,
		"chunks":       []map[string]any{{"seq": seq, "firstAt": at, "lastAt": at}},
	})
	if err != nil {
		return fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	r, err = l.w.Do(ctx, replica, token, http.MethodPost, path+"/log-chunks",
		json.RawMessage(commit))
	if err != nil && r.Status == 0 {
		return err //nolint:wrapcheck // names the request
	}
	if r.Status != http.StatusNoContent {
		return fmt.Errorf("%w: log-chunks answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	return nil
}

// keep keeps the last answer's nextToken as name, for a request to name
// as {name}.
func (l *logSteps) keep(name string) error {
	var page struct {
		NextToken string `json:"nextToken"`
	}
	last := l.w.Last()
	if json.Unmarshal(last.Body, &page) != nil || page.NextToken == "" {
		return fmt.Errorf("%w: no nextToken in %d %s", harness.ErrStep, last.Status, last.Body)
	}
	l.w.IDs[name] = page.NextToken
	return nil
}

// eventually reads the download URL of the run kept as run's log of
// state until it holds a URL, which the compaction the run's leaving the
// state enqueued writes.
func (l *logSteps) eventually(ctx context.Context, token, run, state, replica string) error {
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, "the "+state+" log of run "+run+" to download",
		func(ctx context.Context) (bool, error) {
			if err := l.w.SendAs(ctx, `token "`+token+`"`, http.MethodGet,
				"/api/v1/runs/"+l.w.IDs[run]+"/logs-download-url?state="+state, "",
				replica); err != nil {
				return false, err
			}
			var d struct {
				URL *string `json:"url"`
			}
			last := l.w.Last()
			if last.Status != http.StatusOK || json.Unmarshal(last.Body, &d) != nil {
				return false, fmt.Errorf("%w: no download URL in %d %s", harness.ErrStep,
					last.Status, last.Body)
			}
			return d.URL != nil, nil
		})
}

// downloads checks that the last answer's url serves text, with no
// credential.
func (l *logSteps) downloads(ctx context.Context, text string) error {
	want, err := unquote(text)
	if err != nil {
		return err
	}
	var d struct {
		URL string `json:"url"`
	}
	last := l.w.Last()
	if json.Unmarshal(last.Body, &d) != nil || d.URL == "" {
		return fmt.Errorf("%w: no url in %d %s", harness.ErrStep, last.Status, last.Body)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.URL, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	resp, err := store.Do(req)
	if err != nil {
		return fmt.Errorf("%w: download the log: %w", harness.ErrStep, err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK || string(got) != want {
		return fmt.Errorf("%w: the download answered %d %q, want %q: %w", harness.ErrStep,
			resp.StatusCode, got, want, err)
	}
	return nil
}
