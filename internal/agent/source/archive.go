package source

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/weaveplatform/infraweave/internal/agent/workspace"
)

// The bounds of a local preview's archive: what it downloads, the bound of
// a saved workspace, and what it extracts to, so a small archive cannot
// fill the host's disk.
//
// ref: decision 0008 § "Logs and artifacts"
const (
	maxArchive   = 2 << 30
	maxExtracted = 8 << 30
)

var errOversized = errors.New("the archive is over its bound")

// archive extracts the tar.gz src answers into dir, as workspace.Extract
// does.
func (f *Fetcher) archive(ctx context.Context, dir, src string) error {
	if err := os.Mkdir(dir, 0o750); err != nil {
		return fmt.Errorf("the workspace: %w", err)
	}
	return f.download(ctx, src, func(body io.Reader) error { return f.extract(body, dir) })
}

// download hands read the body src answers, at most maxArchive bytes of
// it. No error names src, whose signature lets anyone holding it read
// the object.
func (f *Fetcher) download(ctx context.Context, src string, read func(io.Reader) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDownload, unwrapURL(err))
	}
	resp, err := f.doer.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDownload, unwrapURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: answered %s", ErrDownload, resp.Status)
	}
	body := &bounded{r: resp.Body, n: f.maxArchive}
	err = read(body)
	if body.n < 0 {
		return fmt.Errorf("%w: over %d bytes", ErrDownload, f.maxArchive)
	}
	return err
}

// unwrapURL is err without the URL a *url.Error adds.
func unwrapURL(err error) error {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		return ue.Err
	}
	return err
}

// extract writes the tar.gz r holds into dir, as workspace.Extract does.
func (f *Fetcher) extract(r io.Reader, dir string) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrArchive, err)
	}
	if err := workspace.Extract(zr, dir, f.maxExtracted); err != nil {
		return fmt.Errorf("%w: %w", ErrArchive, err)
	}
	return nil
}

// bounded reads r, failing once it has given more than n bytes.
type bounded struct {
	r io.Reader
	n int64
}

func (b *bounded) Read(p []byte) (int, error) {
	if int64(len(p)) > b.n+1 {
		p = p[:max(b.n+1, 0)]
	}
	n, err := b.r.Read(p)
	b.n -= int64(n)
	if b.n < 0 {
		return n, errOversized
	}
	return n, err //nolint:wrapcheck // the reader's own
}
