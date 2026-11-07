package ctl

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/weaveplatform/infraweave/pkg/sdk"
)

var (
	// ErrUnknownProfile is a profile the file does not hold.
	ErrUnknownProfile = errors.New("unknown profile")
	// ErrNoEndpoint is a command that calls the server with no endpoint
	// from a flag, the environment or the profile.
	ErrNoEndpoint = errors.New("no endpoint")
	// ErrNoCredential is whoami with neither an API key nor a token.
	ErrNoCredential = errors.New("no credential")
	// ErrInput is an argument, flag or standard input the command cannot
	// use.
	ErrInput = errors.New("invalid input")
)

// settingFlags maps each setting flag to its key. --config and --help are
// not settings and are never bound.
var settingFlags = map[string]string{
	"profile":  "profile",
	"endpoint": "endpoint",
	"api-key":  "key",
}

// settingEnv maps each key to its environment variable. The secret and the
// token have no flag, so neither appears in a process listing.
//
//nolint:gosec // variable names, not credentials
var settingEnv = map[string]string{
	"profile":  "INFRAWEAVECTL_PROFILE",
	"endpoint": "INFRAWEAVECTL_ENDPOINT",
	"key":      "INFRAWEAVECTL_API_KEY",
	"secret":   "INFRAWEAVECTL_API_KEY_SECRET",
	"token":    "INFRAWEAVECTL_TOKEN",
}

// Root is infraweavectl's command tree. It reads standard input and writes
// standard output through the command's InOrStdin and OutOrStdout.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "infraweavectl",
		Short:         "infraweavectl calls an infraweave server's API",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	pf := root.PersistentFlags()
	pf.String(
		configFlag,
		"",
		"profiles file (default the user config directory's infraweavectl/config.yaml)",
	)
	pf.String(
		"profile",
		"",
		"profile to use, instead of the file's current one (INFRAWEAVECTL_PROFILE)",
	)
	pf.String("endpoint", "", "server URL, its scheme and host (INFRAWEAVECTL_ENDPOINT)")
	pf.String(
		"api-key",
		"",
		"API key ID (INFRAWEAVECTL_API_KEY); its secret is INFRAWEAVECTL_API_KEY_SECRET",
	)
	root.AddCommand(profileCmd(), whoamiCmd(), versionCmd(), apiCmd(), operatorCmd())
	return root
}

// target is the endpoint and credential a command calls with: the
// profile's, each replaced by a flag or variable that sets it.
func target(fs *pflag.FlagSet) (Profile, error) {
	path, err := configPath(fs)
	if err != nil {
		return Profile{}, err
	}
	f, err := readFile(path)
	if err != nil {
		return Profile{}, err
	}
	v := viper.New()
	for name, key := range settingFlags {
		if fl := fs.Lookup(name); fl != nil {
			// BindPFlag fails only on a nil flag.
			_ = v.BindPFlag(key, fl)
		}
	}
	for key, env := range settingEnv {
		// BindEnv fails only without a key.
		_ = v.BindEnv(key, env)
	}
	name := v.GetString("profile")
	if name == "" {
		name = f.Current
	}
	var p Profile
	if name != "" {
		var ok bool
		if p, ok = f.Profiles[name]; !ok {
			return Profile{}, fmt.Errorf("%w %q in %s", ErrUnknownProfile, name, path)
		}
	}
	if e := v.GetString("endpoint"); e != "" {
		p.Endpoint = e
	}
	// A credential from a flag or variable replaces the profile's whole,
	// so a key never pairs with the profile's secret for another key.
	if k, t := v.GetString("key"), v.GetString("token"); k != "" || t != "" {
		p.Key, p.Secret, p.Token = k, v.GetString("secret"), t
	}
	if p.Endpoint == "" {
		return Profile{}, fmt.Errorf(
			"%w: set one with --endpoint, INFRAWEAVECTL_ENDPOINT or profile set",
			ErrNoEndpoint,
		)
	}
	return p, checkEndpoint(p.Endpoint)
}

// checkEndpoint rejects an endpoint that is not an http or https origin,
// so a path in it never doubles the API's prefix.
func checkEndpoint(e string) error {
	u, err := url.Parse(e)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
		strings.Trim(u.Path, "/") != "" || u.RawQuery != "" {
		return fmt.Errorf("%w: endpoint %q is not an http or https origin", ErrInput, e)
	}
	return nil
}

// client is an SDK client of the target's server with its credential.
func client(fs *pflag.FlagSet) (*sdk.Client, error) {
	p, err := target(fs)
	if err != nil {
		return nil, err
	}
	var opts []sdk.Option
	switch {
	case p.Key != "":
		opts = append(opts, sdk.WithAPIKey(p.Key, p.Secret))
	case p.Token != "":
		opts = append(opts, sdk.WithToken(p.Token))
	}
	return sdk.New(p.Endpoint, opts...) //nolint:wrapcheck // the SDK names itself
}

// call sends method to path with body, prints the answer and returns the
// SDK's error for a status of 400 or more, after printing its problem.
func call(cmd *cobra.Command, method, path string, body any, headers ...sdk.Header) error {
	c, err := client(cmd.Flags())
	if err != nil {
		return err
	}
	r, err := c.Do(cmd.Context(), method, path, body, headers...)
	if perr := show(cmd.OutOrStdout(), answer(r, err)); perr != nil {
		return perr
	}
	return err //nolint:wrapcheck // the SDK's typed problem, as it is
}

// answer is the body to print for a Do call: the answer's, else the
// problem the API key's exchange answered before the request was sent.
func answer(r *sdk.Response, err error) []byte {
	if r != nil {
		return r.Body
	}
	if p, ok := sdk.AsProblem(err); ok {
		// A Problem has only strings and an int, so it always encodes.
		b, _ := json.Marshal(p)
		return b
	}
	return nil
}

// list prints every item of a cursor-paginated list at path as one JSON
// array.
func list(cmd *cobra.Command, path string) error {
	c, err := client(cmd.Flags())
	if err != nil {
		return err
	}
	items := []json.RawMessage{}
	next := ""
	for {
		p := path
		if next != "" {
			p += "?cursor=" + url.QueryEscape(next)
		}
		r, err := c.Do(cmd.Context(), http.MethodGet, p, nil)
		if err != nil {
			if perr := show(cmd.OutOrStdout(), answer(r, err)); perr != nil {
				return perr
			}
			return err //nolint:wrapcheck // as above
		}
		var page struct {
			Items      []json.RawMessage `json:"items"`
			NextCursor string            `json:"nextCursor"`
		}
		if err := json.Unmarshal(r.Body, &page); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
		items = append(items, page.Items...)
		if next = page.NextCursor; next == "" {
			break
		}
	}
	b, err := json.Marshal(items)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return show(cmd.OutOrStdout(), b)
}

// show writes an answer's body, indented when it is JSON, and nothing
// for an empty one.
func show(w io.Writer, body []byte) error {
	if len(body) == 0 {
		return nil
	}
	var b bytes.Buffer
	if json.Indent(&b, body, "", "  ") != nil {
		b.Reset()
		b.Write(body)
	}
	if !bytes.HasSuffix(b.Bytes(), []byte("\n")) {
		b.WriteByte('\n')
	}
	if _, err := w.Write(b.Bytes()); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// stdin is standard input's first line, trimmed, which holds a secret
// that must not appear in a flag. what names it in the error for an empty
// one.
func stdin(cmd *cobra.Command, what string) (string, error) {
	b, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", fmt.Errorf("read %s: %w", what, err)
	}
	s, _, _ := strings.Cut(string(b), "\n")
	if s = strings.TrimSpace(s); s == "" {
		return "", fmt.Errorf("%w: standard input holds no %s", ErrInput, what)
	}
	return s, nil
}

// seg is s escaped as one path segment.
func seg(s string) string { return url.PathEscape(s) }
