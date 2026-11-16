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
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/iac/version"
)

// The vendors' public release indexes, which the vendors settings name
// unless the operator points them elsewhere.
const (
	DefaultTerraformURL  = "https://api.releases.hashicorp.com/v1/releases/terraform"
	DefaultOpenTofuURL   = "https://get.opentofu.org/tofu/api.json"
	DefaultTerragruntURL = "https://api.github.com/repos/gruntwork-io/terragrunt/releases"
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

// maxBody bounds an index's body: HashiCorp's pages are tens of
// kilobytes, GitHub's a megabyte or two with their release notes, and
// OpenTofu's whole index a few megabytes, so a larger one is not an index.
const maxBody = 64 << 20

// getJSON decodes the JSON body of a GET of u into out, and answers the
// response's header.
func getJSON(ctx context.Context, client *http.Client, u string, out any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrIndex, u, err)
	}
	req.Header.Set("Accept", "application/json")
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrIndex, err)
	}
	defer resp.Body.Close() //nolint:errcheck // a read body's close has nothing to report
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s answered %s", ErrIndex, u, resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrIndex, u, err)
	}
	return resp.Header, nil
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
		if _, err := getJSON(ctx, h.Client, u.String(), &page); err != nil {
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
	if _, err := getJSON(ctx, o.Client, o.URL, &idx); err != nil {
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

// GitHub reads a repository's releases from GitHub's REST API, where
// Terragrunt publishes its own: a page at a time, newest first, each page
// naming the next in its Link header. A mirror may serve every release on
// one page, naming no next.
type GitHub struct {
	URL    string
	Client *http.Client
}

// GitHubPage is how many releases the reader asks GitHub for a page, its
// most. Terragrunt's thousand releases are then ten requests a refresh,
// well inside GitHub's sixty an hour for a caller without a token.
const GitHubPage = 100

// githubRelease is the part of a release GitHub's API lists that the
// reader reads. A tag is a version after a leading v, such as v0.93.3;
// Terragrunt's alpha and beta builds are tagged as no version at all,
// such as alpha-2026040801, and its release candidates as prereleases,
// such as v1.2.0-rc1, and GitHub marks both prereleases.
type githubRelease struct {
	Tag        string `json:"tag_name"` //nolint:tagliatelle // GitHub's spelling
	Prerelease bool   `json:"prerelease"`
	Draft      bool   `json:"draft"`
	Published  string `json:"published_at"` //nolint:tagliatelle // as above
}

// Releases reads every page of the index.
func (g GitHub) Releases(ctx context.Context) ([]Release, error) {
	base, err := url.Parse(g.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrIndex, err)
	}
	q := base.Query()
	q.Set("per_page", strconv.Itoa(GitHubPage))
	base.RawQuery = q.Encode()
	var out []Release
	pages := 0
	for at := base; at != nil; {
		var page []githubRelease
		header, err := getJSON(ctx, g.Client, at.String(), &page)
		if err != nil {
			return nil, err
		}
		for _, r := range page {
			v, ok := stable(strings.TrimPrefix(r.Tag, "v"))
			if !ok || r.Prerelease || r.Draft {
				continue
			}
			// A release whose time does not parse is listed without one.
			published, _ := time.Parse(time.RFC3339Nano, r.Published)
			out = append(out, Release{Version: v, Published: published})
		}
		pages++
		if at, err = nextPage(base, at, header.Values("Link")); err != nil {
			return nil, err
		}
		if at != nil && pages == maxPages {
			return nil, fmt.Errorf("%w: %s lists more than %d pages", ErrIndex, g.URL, maxPages)
		}
	}
	return newest(g.URL, out)
}

// nextPage is the target of the rel="next" link of links, the Link header
// values of the page at, resolved against at; nil when none names one. A
// next page on another scheme or host than base's, the index's, is
// ErrIndex, so an index cannot point the server's requests at another
// address.
func nextPage(base, at *url.URL, links []string) (*url.URL, error) {
	for _, value := range links {
		for link := range strings.SplitSeq(value, ",") {
			target, params, _ := strings.Cut(strings.TrimSpace(link), ";")
			if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") ||
				!relNext(params) {
				continue
			}
			u, err := at.Parse(target[1 : len(target)-1])
			if err != nil {
				return nil, fmt.Errorf("%w: %s names a next page that is no URL: %w", ErrIndex,
					base.Redacted(), err)
			}
			if u.Scheme != base.Scheme || u.Host != base.Host {
				return nil, fmt.Errorf("%w: %s names a next page at %s://%s", ErrIndex,
					base.Redacted(), u.Scheme, u.Host)
			}
			return u, nil
		}
	}
	return nil, nil //nolint:nilnil // no next page
}

// relNext reports whether params, a link's parameters after its target,
// relate it as next.
func relNext(params string) bool {
	for param := range strings.SplitSeq(params, ";") {
		name, value, _ := strings.Cut(strings.TrimSpace(param), "=")
		if strings.EqualFold(strings.TrimSpace(name), "rel") &&
			slices.Contains(strings.Fields(strings.Trim(strings.TrimSpace(value), `"`)), "next") {
			return true
		}
	}
	return false
}
