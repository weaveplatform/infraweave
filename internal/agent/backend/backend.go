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
// root, or in each Terragrunt unit's directory.
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
// backend: it writes File into root, the run's project root under
// workspace, and answers the phase environment's credentials. A run whose
// stack infraweave keeps no state for writes nothing and answers none.
//
// A Terragrunt stack's run with RunAll writes File into each of the
// project's Units, naming its workspace, after checking every unit: one
// whose name is no workspace name, or that declares its own backend, is
// refused naming it, and nothing is written. The files a unit includes
// are read under workspace, which the repository's checkout fills.
// Without RunAll, only the project root's unit is checked and written,
// naming the default workspace; nested units and stacks are left alone.
//
// ref: decision 0010 § "How a run reaches it"
func Configure(workspace, root string, h handoff.Handoff) ([]string, error) {
	if h.Spec.StateBackend == nil {
		return nil, nil
	}
	units := []string{""}
	if h.Spec.Terragrunt != nil {
		var err error
		if units, err = checkUnits(workspace, root, h.Spec.Terragrunt.RunAll); err != nil {
			return nil, err
		}
	}
	for _, u := range units {
		address, err := Address(h.Endpoint, h.Spec.StateBackend.Path, u)
		if err != nil {
			return nil, err
		}
		err = write(filepath.Join(root, filepath.FromSlash(u), File), address)
		if err != nil && h.Spec.Terragrunt != nil {
			return nil, fmt.Errorf("%s: %w", unit(u), err)
		}
		if err != nil {
			return nil, err
		}
	}
	return Environment(h.RunToken), nil
}

// checkUnits checks the selected units: every discovered unit under runAll,
// otherwise the root alone, each with a valid workspace name and no backend.
func checkUnits(workspace, root string, runAll bool) ([]string, error) {
	units := []string{""}
	if runAll {
		var err error
		if units, err = Units(root); err != nil {
			return nil, err
		}
	}
	// Terragrunt's functions answer absolute paths, which the include
	// paths the check works out are built from.
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("the workspace: %w", err)
	}
	absRoot, _ := filepath.Abs(root) // the working directory answered a moment ago
	fsys, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("open the workspace: %w", err)
	}
	defer fsys.Close()
	p := &project{fs: fsys, workspace: abs, root: absRoot}
	for _, u := range units {
		if err := checkName(u); err != nil {
			return nil, err
		}
		if err := p.checkBackend(u); err != nil {
			return nil, err
		}
	}
	return units, nil
}

// Address is the URL of the backend's workspace: p, the specification's
// stateBackend.path, under the server URL whose worker API is at
// endpoint, as a handoff names it, and for a workspace other than the
// default, empty, its name as one escaped path segment under workspaces,
// a / in it %2F. It keeps the endpoint's scheme, host, port and any path
// before client.BasePath, so the backend answers behind the same relay or
// private address as the worker API. An endpoint with user information, a
// query or a fragment is refused, since the backend file would carry it.
//
// ref: decision 0010 § "Endpoints"
func Address(endpoint, p, workspace string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrAddress, err)
	}
	prefix, ok := strings.CutSuffix(u.Path, client.BasePath)
	rawPrefix, _ := strings.CutSuffix(u.EscapedPath(), client.BasePath)
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
	if workspace != "" {
		// RawPath keeps the workspace one segment: Path alone would
		// send each / in it as a separator.
		out.RawPath = rawPrefix + (&url.URL{Path: p}).EscapedPath() + "/workspaces/" +
			url.PathEscape(workspace)
		out.Path += "/workspaces/" + workspace
	}
	return out.String(), nil
}

// APIAddress is the REST API's base URL reached through the same relay or
// private address as the worker API. Address applies the backend's URL
// checks and preserves the same prefix for both APIs.
func APIAddress(endpoint string) (string, error) {
	// This path must match app.BaseURL in internal/app/router.go; the
	// agent cannot import the server's assembly.
	return Address(endpoint, "/api/v1", "")
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
