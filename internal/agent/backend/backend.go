package backend

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
)

// File is the backend file's name, which Configure writes at the project
// root.
//
// ref: decision 0010 § "How a run reaches it"
const File = "infraweave_backend.tf"

// Username is the HTTP Basic user name of a run's state calls, whose
// password is the run token.
const Username = "run"

// ErrAddress marks a worker API endpoint or a backend path Address cannot
// join.
var ErrAddress = errors.New("not a state backend address")

// Configure points the run h hands over at its stack's managed state
// backend: it writes File into root, the run's project root, and answers
// the phase environment's credentials. A run whose stack infraweave keeps
// no state for writes nothing and answers none.
func Configure(root string, h handoff.Handoff) ([]string, error) {
	if h.Spec.StateBackend == nil {
		return nil, nil
	}
	address, err := Address(h.Endpoint, h.Spec.StateBackend.Path)
	if err != nil {
		return nil, err
	}
	if err := write(filepath.Join(root, File), address); err != nil {
		return nil, err
	}
	return Environment(h.RunToken), nil
}

// Address is the backend's URL: p, the specification's stateBackend.path,
// under the server URL whose worker API is at endpoint, as a handoff names
// it. It keeps the endpoint's scheme, host, port and any path before
// client.BasePath, so the backend answers behind the same relay or private
// address as the worker API. An endpoint with user information, a query
// or a fragment is refused, since the backend file would carry it.
func Address(endpoint, p string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrAddress, err)
	}
	prefix, ok := strings.CutSuffix(u.Path, client.BasePath)
	switch {
	case u.Scheme != "http" && u.Scheme != "https", u.Host == "":
		return "", fmt.Errorf("%w: the endpoint %q is no HTTP URL", ErrAddress, endpoint)
	case u.User != nil, u.RawQuery != "", u.ForceQuery, u.Fragment != "":
		return "", fmt.Errorf("%w: the endpoint has more than a scheme, host and path",
			ErrAddress)
	case !ok:
		return "", fmt.Errorf("%w: the endpoint %q does not end in %s", ErrAddress, endpoint,
			client.BasePath)
	case !strings.HasPrefix(p, "/") || path.Clean(p) != p:
		return "", fmt.Errorf("%w: the path %q is not clean and absolute", ErrAddress, p)
	}
	out := url.URL{Scheme: u.Scheme, Host: u.Host, Path: prefix + p}
	return out.String(), nil
}

// Environment is the phase environment's credentials of the run whose run
// token is token, which Terraform's and OpenTofu's HTTP backend read. They
// are never written into the workspace, so a saved workspace or a log
// never holds the token.
//
// ref: decision 0010 § "How a run reaches it"
func Environment(token string) []string {
	return []string{"TF_HTTP_USERNAME=" + Username, "TF_HTTP_PASSWORD=" + token}
}

// write writes the backend file at name, pointing the state, lock and
// unlock calls at address. Whatever the repository commits under the name
// is removed first and the file created anew, so a symbolic link there is
// replaced rather than followed.
func write(name, address string) error {
	if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("replace %s: %w", File, err)
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("write %s: %w", File, err)
	}
	_, err = f.WriteString(render(address))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", File, err)
	}
	return nil
}

// render is the backend file's HCL for address. The defaults of
// lock_method and unlock_method are LOCK and UNLOCK already; naming them
// keeps the file independent of the tool's defaults.
func render(address string) string {
	a := quoted(address)
	return "# infraweave writes this file for the run. The run's credentials are in\n" +
		"# its environment, never here.\n" +
		"terraform {\n" +
		"  backend \"http\" {\n" +
		"    address        = " + a + "\n" +
		"    lock_address   = " + a + "\n" +
		"    unlock_address = " + a + "\n" +
		"    lock_method    = \"LOCK\"\n" +
		"    unlock_method  = \"UNLOCK\"\n" +
		"  }\n" +
		"}\n"
}

// quoted is s as an HCL string literal. An Address has no control
// characters, so escaping the backslash, the quote and the template
// sequences ${ and %{ is all a literal needs.
func quoted(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "${", "$${", "%{", "%%{").Replace(s) +
		`"`
}
