package runs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// previewSteps build local workspaces, put them where an upload's answer
// says, and check that a run's specification fetches one, keeping each
// archive's bytes by name.
type previewSteps struct {
	w        *harness.World
	archives map[string][]byte
}

func (p *previewSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^a local workspace "(\w+)" holding the files:$`, p.archive)
	sc.Step(`^the local workspace "(\w+)" is put at the answer's uploadUrl$`, p.put)
	sc.Step(`^the specification fetches its source from the local workspace "(\w+)"$`,
		p.source)
}

// archive builds a tar.gz of table's paths and contents as name, and
// keeps its size and SHA-256, in hex, as {name.size} and {name.sha256}.
func (p *previewSteps) archive(name string, table *godog.Table) error {
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	tw := tar.NewWriter(zw)
	for i, row := range table.Rows {
		if len(row.Cells) != 2 {
			return fmt.Errorf("%w: row %d: want path and content", harness.ErrStep, i+1)
		}
		body := row.Cells[1].Value
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: row.Cells[0].Value, Mode: 0o644, Size: int64(len(body)),
		}); err != nil {
			return fmt.Errorf("%w: %w", harness.ErrStep, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			return fmt.Errorf("%w: %w", harness.ErrStep, err)
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	sum := sha256.Sum256(b.Bytes())
	p.archives[name] = b.Bytes()
	p.w.IDs[name+".size"] = strconv.Itoa(b.Len())
	p.w.IDs[name+".sha256"] = hex.EncodeToString(sum[:])
	return nil
}

// put PUTs the local workspace name to the last answer's uploadUrl with
// its uploadHeaders, with no credential, and checks that the store took
// it.
func (p *previewSteps) put(ctx context.Context, name string) error {
	r := p.w.Last()
	var up struct {
		UploadURL     string `json:"uploadUrl"`
		UploadHeaders struct {
			Entries []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"entries"`
		} `json:"uploadHeaders"`
	}
	if r.Status != http.StatusCreated || json.Unmarshal(r.Body, &up) != nil ||
		up.UploadURL == "" {
		return fmt.Errorf("%w: the upload answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, up.UploadURL,
		bytes.NewReader(p.archives[name]))
	if err != nil {
		return fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	for _, h := range up.UploadHeaders.Entries {
		req.Header.Set(h.Key, h.Value)
	}
	resp, err := store.Do(req)
	if err != nil {
		return fmt.Errorf("%w: put the workspace: %w", harness.ErrStep, err)
	}
	answer, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%w: the workspace's PUT answered %d %s", harness.ErrStep,
			resp.StatusCode, answer)
	}
	return nil
}

// source checks that the last answer, a run's specification, fetches the
// run's source as an archive whose URL reads the local workspace name
// with no credential.
func (p *previewSteps) source(ctx context.Context, name string) error {
	var s wire.RunSpec
	r := p.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &s) != nil {
		return fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	if s.Source.Kind != wire.Archive || s.Source.Narrowed {
		return fmt.Errorf("%w: the source is %+v, want an archive", harness.ErrStep, s.Source)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Source.Url, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	resp, err := store.Do(req)
	if err != nil {
		return fmt.Errorf("%w: get the archive: %w", harness.ErrStep, err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || !bytes.Equal(body, p.archives[name]) {
		return fmt.Errorf("%w: the archive's GET answered %d with %d bytes, want the %d of %s",
			harness.ErrStep, resp.StatusCode, len(body), len(p.archives[name]), name)
	}
	return nil
}
