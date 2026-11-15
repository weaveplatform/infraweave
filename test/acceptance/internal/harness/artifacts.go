package harness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
)

// storage PUTs an artifact to its signed URL, which the object store
// answers, not a server.
var storage = &http.Client{Timeout: 30 * time.Second}

// RecordArtifact does with body what a runner does with an artifact the
// run of the account acct uploads in state at state version version as
// name: it seals body under the content key derived from the run's
// artifact key and the object's key, which the runner builds as the
// server does, declares it to upload-urls, checks that the answer names
// the same key, PUTs it to the signed URL and commits it to artifacts,
// each call with the run token kept as token through replica.
func (w *World) RecordArtifact(
	ctx context.Context,
	replica, token string,
	acct kernel.AccountID,
	run kernel.ULID,
	state kernel.RunState,
	version int64,
	name string,
	artifactKey, body []byte,
) error {
	object := kernel.RunObjectKey(acct, run, state, version, name)
	content, err := seal.ArtifactKey(artifactKey, object)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStep, err)
	}
	var sealed bytes.Buffer
	sw, err := seal.NewWriter(&sealed, content)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStep, err)
	}
	if _, err := sw.Write(body); err != nil {
		return fmt.Errorf("%w: %w", ErrStep, err)
	}
	if err := sw.Close(); err != nil {
		return fmt.Errorf("%w: %w", ErrStep, err)
	}
	sum := sha256.Sum256(sealed.Bytes())
	path := "/worker/v1/runs/" + run.String()
	r, err := w.Do(ctx, replica, token, http.MethodPost, path+"/upload-urls", map[string]any{
		"stateVersion": version,
		"uploads": []map[string]any{{
			"kind": "artifact", "name": name, "size": sealed.Len(),
			"sha256": hex.EncodeToString(sum[:]),
		}},
	})
	if err != nil && r.Status == 0 {
		return err
	}
	var signed struct {
		Uploads []struct {
			Key     string            `json:"key"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"uploads"`
	}
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &signed) != nil ||
		len(signed.Uploads) != 1 {
		return fmt.Errorf("%w: upload-urls answered %d %s", ErrStep, r.Status, r.Body)
	}
	up := signed.Uploads[0]
	if up.Key != object {
		return fmt.Errorf("%w: the upload's key is %s, want %s", ErrStep, up.Key, object)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, up.URL,
		bytes.NewReader(sealed.Bytes()))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStep, err)
	}
	for k, v := range up.Headers {
		req.Header.Set(k, v)
	}
	resp, err := storage.Do(req)
	if err != nil {
		return fmt.Errorf("%w: put %s: %w", ErrStep, name, err)
	}
	answer, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%w: the PUT of %s answered %d %s", ErrStep, name, resp.StatusCode,
			answer)
	}
	r, err = w.Do(ctx, replica, token, http.MethodPost, path+"/artifacts", map[string]any{
		"stateVersion": version,
		"artifacts":    []map[string]any{{"kind": "artifact", "name": name}},
	})
	if err != nil && r.Status == 0 {
		return err
	}
	if r.Status != http.StatusNoContent {
		return fmt.Errorf("%w: artifacts answered %d %s", ErrStep, r.Status, r.Body)
	}
	return nil
}
