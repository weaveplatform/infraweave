package index

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/weaveplatform/infraweave/internal/iac/version"
)

// The vendors' public release indexes, which the vendors settings name
// unless the operator points them elsewhere.
const (
	DefaultTerraformURL = "https://api.releases.hashicorp.com/v1/releases/terraform"
	DefaultOpenTofuURL  = "https://get.opentofu.org/tofu/api.json"
)

// Release is a stable release an index lists.
type Release struct {
	Version version.Version
	// Published is when the vendor published it, or zero when the index
	// does not say.
	Published time.Time
}

// Reader reads a vendor's release index.
type Reader interface {
	// Releases are the index's stable releases, newest first: no
	// prerelease and none the vendor withdrew.
	Releases(ctx context.Context) ([]Release, error)
}

// ErrIndex is an index that could not be read or that lists no stable
// release; the error names the index and why.
var ErrIndex = errors.New("release index unreadable")

// Resolve is the newest of rs, newest first, that cs allows, and false
// when none does.
func Resolve(rs []Release, cs version.Constraints) (Release, bool) {
	for _, r := range rs {
		if cs.Allows(r.Version) {
			return r, true
		}
	}
	return Release{}, false
}

// newest is rs newest first with each version once, or ErrIndex when rs
// is empty, which an index with releases never is: a format the reader no
// longer understands shows as one listing nothing.
func newest(source string, rs []Release) ([]Release, error) {
	if len(rs) == 0 {
		return nil, fmt.Errorf("%w: %s lists no stable release", ErrIndex, source)
	}
	slices.SortStableFunc(rs, func(a, b Release) int { return b.Version.Compare(a.Version) })
	return slices.CompactFunc(rs, func(a, b Release) bool {
		return a.Version.Compare(b.Version) == 0
	}), nil
}

// stable is s as a release version, and false for a prerelease or a
// version that does not parse.
func stable(s string) (version.Version, bool) {
	v, err := version.Parse(s)
	return v, err == nil && v.Prerelease == ""
}

// maxBody bounds an index's body: HashiCorp's pages are tens of kilobytes
// and OpenTofu's whole index a few megabytes, so a larger one is not an
// index.
const maxBody = 64 << 20

// getJSON decodes the JSON body of a GET of u into out.
func getJSON(ctx context.Context, client *http.Client, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrIndex, u, err)
	}
	req.Header.Set("Accept", "application/json")
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrIndex, err)
	}
	defer resp.Body.Close() //nolint:errcheck // a read body's close has nothing to report
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s answered %s", ErrIndex, u, resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrIndex, u, err)
	}
	return nil
}

// HashiCorp reads HashiCorp's releases API, which lists a product's
// releases newest first, a page at a time, each page after the creation
// time of the last one before it.
type HashiCorp struct {
	URL    string
	Client *http.Client
}

// HashiCorpPage is how many releases HashiCorp's API lists a page, its
// most; a shorter page is the last.
const HashiCorpPage = 20

// maxPages bounds a read of HashiCorp's API, whose Terraform listing is
// some 25 pages, so a server that never ends its pages ends the read.
const maxPages = 500

// hashicorpRelease is the part of a release HashiCorp's API lists that
// the reader reads.
type hashicorpRelease struct {
	Version    string `json:"version"`
	Prerelease bool   `json:"is_prerelease"`     //nolint:tagliatelle // HashiCorp's spelling
	Created    string `json:"timestamp_created"` //nolint:tagliatelle // as above
	Status     struct {
		State string `json:"state"`
	} `json:"status"`
}

// Withdrawn is the state HashiCorp's API gives a release it withdrew.
const Withdrawn = "withdrawn"

// Releases reads every page of the index.
func (h HashiCorp) Releases(ctx context.Context) ([]Release, error) {
	var out []Release
	after := ""
	for range maxPages {
		u, err := url.Parse(h.URL)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrIndex, err)
		}
		q := u.Query()
		q.Set("limit", strconv.Itoa(HashiCorpPage))
		if after != "" {
			q.Set("after", after)
		}
		u.RawQuery = q.Encode()
		var page []hashicorpRelease
		if err := getJSON(ctx, h.Client, u.String(), &page); err != nil {
			return nil, err
		}
		for _, r := range page {
			v, ok := stable(r.Version)
			if !ok || r.Prerelease || r.Status.State == Withdrawn {
				continue
			}
			// A release whose time does not parse is listed without one.
			published, _ := time.Parse(time.RFC3339Nano, r.Created)
			out = append(out, Release{Version: v, Published: published})
		}
		if len(page) < HashiCorpPage {
			return newest(h.URL, out)
		}
		after = page[len(page)-1].Created
	}
	return nil, fmt.Errorf("%w: %s lists more than %d pages", ErrIndex, h.URL, maxPages)
}

// OpenTofu reads the index OpenTofu's installer reads: one document that
// lists every release's version, prereleases among them, and no dates.
type OpenTofu struct {
	URL    string
	Client *http.Client
}

// Releases reads the index.
func (o OpenTofu) Releases(ctx context.Context) ([]Release, error) {
	var idx struct {
		Versions []struct {
			ID string `json:"id"`
		} `json:"versions"`
	}
	if err := getJSON(ctx, o.Client, o.URL, &idx); err != nil {
		return nil, err
	}
	out := make([]Release, 0, len(idx.Versions))
	for _, r := range idx.Versions {
		if v, ok := stable(r.ID); ok {
			out = append(out, Release{Version: v})
		}
	}
	return newest(o.URL, out)
}
