package vendorsfixtures

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/weaveplatform/infraweave/internal/iac/version"
)

// Format is how a source's index lays out its releases.
type Format string

// The formats the vendors' indexes use.
const (
	// HashiCorp is HashiCorp's releases API: a JSON array of releases a
	// page, newest first, each page after the timestamp_created of the
	// last before it.
	HashiCorp Format = "hashicorp"
	// OpenTofu is OpenTofu's installer index: one object whose versions
	// each name an id and their files.
	OpenTofu Format = "opentofu"
	// GitHub is GitHub's REST API's releases of a repository: a JSON array
	// of releases a page, newest first, each page naming the next in its
	// Link header. A release's version is its tag_name after a leading v.
	GitHub Format = "github"
)

// Source is one index a fixture is cut from.
type Source struct {
	// Name is the fixture's file name, without .json.
	Name   string
	Format Format
	URL    string
	// Keep is the releases kept beside the newest stable release and the
	// newest prerelease: a fixed set, so a later fetch changes only those
	// two and the fetch date. A GitHub release is matched by its tag after
	// a leading v.
	Keep *regexp.Regexp
}

// Sources are the vendors' public indexes. Terraform's keeps every 1.5
// and 1.6 release, prereleases among them, which is more than one of the
// API's pages; OpenTofu's keeps every 1.6 release; Terragrunt's keeps
// every 0.73 release, its alpha, beta and release-candidate tags among
// them, and the alpha builds tagged as no version on 2025-02-27.
var Sources = []Source{
	{
		Name: "terraform", Format: HashiCorp,
		URL:  "https://api.releases.hashicorp.com/v1/releases/terraform",
		Keep: regexp.MustCompile(`^1\.[56]\.`),
	},
	{
		Name: "opentofu", Format: OpenTofu,
		URL:  "https://get.opentofu.org/tofu/api.json",
		Keep: regexp.MustCompile(`^1\.6\.`),
	},
	{
		Name: "terragrunt", Format: GitHub,
		URL:  "https://api.github.com/repos/gruntwork-io/terragrunt/releases",
		Keep: regexp.MustCompile(`^(0\.73\.|alpha20250227)`),
	},
}

// Fixture is a recorded index: where it came from, when, and its body as
// the index serves it, trimmed to the kept releases.
type Fixture struct {
	Source  string          `json:"source"`
	Fetched string          `json:"fetched"`
	Body    json.RawMessage `json:"body"`
}

// ErrFetch is an index that could not be read.
var ErrFetch = errors.New("fetch release index")

// page is HashiCorp's most releases a page.
const page = 20

// Run fetches each source with client and writes its fixture to dir as
// <name>.json, dated today.
func Run(ctx context.Context, client *http.Client, sources []Source, dir, today string) error {
	for _, s := range sources {
		var (
			body json.RawMessage
			err  error
		)
		switch s.Format {
		case HashiCorp:
			body, err = hashicorp(ctx, client, s)
		case OpenTofu:
			body, err = opentofu(ctx, client, s)
		case GitHub:
			body, err = github(ctx, client, s)
		default:
			err = fmt.Errorf("%w: %s has no format %q", ErrFetch, s.Name, s.Format)
		}
		if err != nil {
			return err
		}
		out, err := json.MarshalIndent(Fixture{Source: s.URL, Fetched: today, Body: body}, "", "  ")
		if err != nil {
			return fmt.Errorf("%s fixture: %w", s.Name, err)
		}
		path := filepath.Join(dir, s.Name+".json")
		if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
			return fmt.Errorf("%s fixture: %w", s.Name, err)
		}
	}
	return nil
}

// kept is the indexes of versions, in their order, that s keeps: its fixed
// set, the newest stable release and the newest prerelease.
func kept(s Source, versions []string) map[int]bool {
	out := map[int]bool{}
	newest := [2]int{-1, -1} // the stable release's, then the prerelease's
	var best [2]version.Version
	for i, raw := range versions {
		if s.Keep.MatchString(raw) {
			out[i] = true
		}
		v, err := version.Parse(raw)
		if err != nil {
			continue
		}
		slot := 0
		if v.Prerelease != "" {
			slot = 1
		}
		if newest[slot] < 0 || v.Compare(best[slot]) > 0 {
			newest[slot], best[slot] = i, v
		}
	}
	for _, i := range newest {
		if i >= 0 {
			out[i] = true
		}
	}
	return out
}

func hashicorp(ctx context.Context, client *http.Client, s Source) (json.RawMessage, error) {
	var all []map[string]json.RawMessage
	after := ""
	for {
		u, err := url.Parse(s.URL)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrFetch, err)
		}
		q := u.Query()
		q.Set("limit", strconv.Itoa(page))
		if after != "" {
			q.Set("after", after)
		}
		u.RawQuery = q.Encode()
		var releases []map[string]json.RawMessage
		if _, err := get(ctx, client, u.String(), &releases); err != nil {
			return nil, err
		}
		all = append(all, releases...)
		if len(releases) < page {
			break
		}
		if err := json.Unmarshal(
			releases[len(releases)-1]["timestamp_created"],
			&after,
		); err != nil {
			return nil, fmt.Errorf("%w: %s: a release without timestamp_created", ErrFetch, s.URL)
		}
	}
	versions := make([]string, len(all))
	for i, r := range all {
		_ = json.Unmarshal(r["version"], &versions[i]) // a release without one is not kept
	}
	keep := kept(s, versions)
	out := []map[string]json.RawMessage{}
	for i, r := range all {
		if keep[i] {
			// A release's builds list every platform's download, which no
			// reader of the index reads, and make up most of its size.
			delete(r, "builds")
			out = append(out, r)
		}
	}
	return marshal(out)
}

func opentofu(ctx context.Context, client *http.Client, s Source) (json.RawMessage, error) {
	var idx map[string]json.RawMessage
	if _, err := get(ctx, client, s.URL, &idx); err != nil {
		return nil, err
	}
	var all []map[string]json.RawMessage
	if err := json.Unmarshal(idx["versions"], &all); err != nil {
		return nil, fmt.Errorf("%w: %s: no versions: %w", ErrFetch, s.URL, err)
	}
	versions := make([]string, len(all))
	for i, r := range all {
		_ = json.Unmarshal(r["id"], &versions[i]) // as above
	}
	keep := kept(s, versions)
	out := []map[string]json.RawMessage{}
	for i, r := range all {
		if keep[i] {
			// A release's files list every platform's download; two keep
			// the shape.
			var files []json.RawMessage
			if json.Unmarshal(r["files"], &files) == nil && len(files) > 2 {
				r["files"], _ = marshal(files[:2])
			}
			out = append(out, r)
		}
	}
	versionsJSON, _ := marshal(out)
	idx["versions"] = versionsJSON
	return marshal(idx)
}

// githubPages bounds the pages of a GitHub index, whose Terragrunt
// listing is some ten pages of a hundred, so an index whose pages never
// end ends the fetch.
const githubPages = 500

// githubDropped are the members of a GitHub release no reader reads,
// which make up most of its size: its release notes, its author, the
// reactions to it, and its assets, each platform's download with its
// uploader.
var githubDropped = []string{"body", "author", "reactions", "mentions_count", "assets"}

func github(ctx context.Context, client *http.Client, s Source) (json.RawMessage, error) {
	u, err := url.Parse(s.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFetch, err)
	}
	q := u.Query()
	q.Set("per_page", "100")
	u.RawQuery = q.Encode()
	var all []map[string]json.RawMessage
	next := u.String()
	for range githubPages {
		var releases []map[string]json.RawMessage
		header, err := get(ctx, client, next, &releases)
		if err != nil {
			return nil, err
		}
		all = append(all, releases...)
		if next = nextLink(header.Values("Link")); next == "" {
			break
		}
	}
	if next != "" {
		return nil, fmt.Errorf("%w: %s lists more than %d pages", ErrFetch, s.URL, githubPages)
	}
	versions := make([]string, len(all))
	for i, r := range all {
		_ = json.Unmarshal(r["tag_name"], &versions[i]) // as above
		versions[i] = strings.TrimPrefix(versions[i], "v")
	}
	keep := kept(s, versions)
	out := []map[string]json.RawMessage{}
	for i, r := range all {
		if keep[i] {
			for _, name := range githubDropped {
				delete(r, name)
			}
			out = append(out, r)
		}
	}
	return marshal(out)
}

// nextLink is the target of the rel="next" link of links, Link header
// values; empty when none names one.
func nextLink(links []string) string {
	for _, value := range links {
		for link := range strings.SplitSeq(value, ",") {
			target, params, _ := strings.Cut(strings.TrimSpace(link), ";")
			if strings.Contains(params, `rel="next"`) && strings.HasPrefix(target, "<") &&
				strings.HasSuffix(target, ">") {
				return target[1 : len(target)-1]
			}
		}
	}
	return ""
}

// marshal is v as JSON. Every value here is decoded JSON, which encodes.
func marshal(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFetch, err)
	}
	return b, nil
}

// get decodes the JSON body of a GET of u into out, and answers the
// response's header.
func get(ctx context.Context, client *http.Client, u string, out any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFetch, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFetch, err)
	}
	defer resp.Body.Close() //nolint:errcheck // a read body's close has nothing to report
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s answered %s", ErrFetch, u, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrFetch, u, err)
	}
	return resp.Header, nil
}
