package specref

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// FileFlowsSDK is the snapshot of the app SDK's identifiers, from which
// specref inventory --product flows writes the register.
const FileFlowsSDK = "sdk-identifiers.json"

// flowsSDKDeclarations is the SDK's declaration file inside its npm tarball,
// after walkTarball drops the leading package/ directory.
const flowsSDKDeclarations = "dist/v1/index.d.ts"

// flowctlAPI is the flowctl file that makes every RPC.
const flowctlAPI = "src/api.ts"

var (
	// ErrFlowsOverlay reports upstream data the overlay schemas reject.
	ErrFlowsOverlay = errors.New("does not match schemas.yaml")
	// ErrFlowsFetch reports an upstream source missing what fetch reads.
	ErrFlowsFetch = errors.New("flows fetch")
)

// FlowsFetchOptions say where the Flows sources live and which versions to
// pin.
type FlowsFetchOptions struct {
	Dir          string
	Codeload     string // base URL serving <repo>/tar.gz/<ref>
	NPM          string // npm registry base URL
	ProviderRepo string
	ProviderRef  string
	FlowctlRepo  string
	FlowctlRef   string
	SDKPackage   string
	SDKVersion   string
	Templates    string // the template index, templates.json
	Registry     string // an app registry's manifest URL
	Fetcher      *Fetcher
	Now          func() time.Time
}

// FetchFlows refreshes the Flows snapshots and the lock. It validates the
// published templates and the registry against schemas.yaml in Dir first,
// and writes nothing unless every source was fetched, parsed and valid.
func FetchFlows(ctx context.Context, o FlowsFetchOptions) error {
	for name, ref := range map[string]string{"provider": o.ProviderRef, "flowctl": o.FlowctlRef} {
		if !commitSHA.MatchString(ref) {
			return fmt.Errorf("%w: %s ref %q", ErrBadCommit, name, ref)
		}
	}
	overlay, err := readOverlay(o.Dir)
	if err != nil {
		return err
	}
	lock := FlowsLock{FetchedAt: o.Now().UTC().Format(time.RFC3339)}
	files := map[string][]byte{}
	if err := fetchFlowsClients(ctx, o, &lock, files); err != nil {
		return err
	}
	if lock.Templates, err = fetchTemplates(ctx, o, overlay); err != nil {
		return err
	}
	if err := fetchRegistry(ctx, o, overlay); err != nil {
		return err
	}
	lock.Registry = LockRegistry{Manifest: o.Registry}
	var lockBuf bytes.Buffer
	lockBuf.WriteString("# Written by `specref fetch --product flows`; pins every upstream input of this directory.\n")
	enc := yaml.NewEncoder(&lockBuf)
	enc.SetIndent(2)
	if err := enc.Encode(lock); err != nil {
		return fmt.Errorf("encode lock: %w", err)
	}
	files[FileLock] = lockBuf.Bytes()
	return writeFiles(o.Dir, files)
}

func readOverlay(dir string) (openapi3.Schemas, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open reference dir: %w", err)
	}
	defer root.Close()
	data, err := root.ReadFile(FileFlowsOverlay)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", FileFlowsOverlay, err)
	}
	return ParseFlowsOverlay(data)
}

func fetchFlowsClients(ctx context.Context, o FlowsFetchOptions, lock *FlowsLock, files map[string][]byte) error {
	providerTGZ, err := o.Fetcher.Get(ctx, fmt.Sprintf("%s/%s/tar.gz/%s", o.Codeload, o.ProviderRepo, o.ProviderRef))
	if err != nil {
		return err
	}
	provider, err := ReadFlowsProviderTarball(providerTGZ)
	if err != nil {
		return err
	}
	flowctlTGZ, err := o.Fetcher.Get(ctx, fmt.Sprintf("%s/%s/tar.gz/%s", o.Codeload, o.FlowctlRepo, o.FlowctlRef))
	if err != nil {
		return err
	}
	api, err := tarballFile(flowctlTGZ, flowctlAPI)
	if err != nil {
		return err
	}
	flowctl, err := ReadFlowctlAPI(api)
	if err != nil {
		return err
	}
	base := o.SDKPackage[strings.LastIndex(o.SDKPackage, "/")+1:]
	sdkTGZ, err := o.Fetcher.Get(ctx, fmt.Sprintf("%s/%s/-/%s-%s.tgz", o.NPM, o.SDKPackage, base, o.SDKVersion))
	if err != nil {
		return err
	}
	decl, err := tarballFile(sdkTGZ, flowsSDKDeclarations)
	if err != nil {
		return err
	}
	ids, err := ReadSDKIdentifiers(decl)
	if err != nil {
		return err
	}
	for name, v := range map[string]any{FileFlowsProvider: provider, FileFlowsFlowctl: flowctl, FileFlowsSDK: ids} {
		if files[name], err = marshalJSON(v); err != nil {
			return err
		}
	}
	lock.Provider = LockRepo{Repository: o.ProviderRepo, Ref: o.ProviderRef, SHA256: digest(providerTGZ)}
	lock.Flowctl = LockRepo{Repository: o.FlowctlRepo, Ref: o.FlowctlRef, SHA256: digest(flowctlTGZ)}
	lock.SDK = LockPackage{Package: o.SDKPackage, Version: o.SDKVersion, SHA256: digest(sdkTGZ)}
	return nil
}

// tarballFile is one file of a tarball, by its path below the top directory.
func tarballFile(tgz []byte, name string) ([]byte, error) {
	var out []byte
	err := walkTarball(tgz, func(p string, content []byte) error {
		if p == name {
			out = content
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("%w: the tarball has no %s", ErrFlowsFetch, name)
	}
	return out, nil
}

// flowTemplate is one entry of the template index; flow is its definition.
type flowTemplate struct {
	Slug string `json:"slug"`
	Flow string `json:"flow"`
}

func fetchTemplates(ctx context.Context, o FlowsFetchOptions, overlay openapi3.Schemas) (LockTemplates, error) {
	out := LockTemplates{Index: o.Templates, Flows: map[string]string{}}
	data, err := o.Fetcher.Get(ctx, o.Templates)
	if err != nil {
		return out, err
	}
	var index []flowTemplate
	if err := json.Unmarshal(data, &index); err != nil {
		return out, fmt.Errorf("decode %s: %w", o.Templates, err)
	}
	if len(index) == 0 {
		return out, fmt.Errorf("%w: %s lists no templates", ErrFlowsFetch, o.Templates)
	}
	var errs []error
	for _, t := range index {
		if t.Slug == "" || t.Flow == "" {
			errs = append(errs, fmt.Errorf("%w: a template has no slug or flow", ErrFlowsFetch))
			continue
		}
		if err := ValidateFlowDefinition(overlay, []byte(t.Flow)); err != nil {
			errs = append(errs, fmt.Errorf("template %s: %w", t.Slug, err))
		}
		out.Flows[t.Slug] = digest([]byte(t.Flow))
	}
	return out, errors.Join(errs...)
}

// fetchRegistry validates a registry's manifest and the versions file of
// its first app. It follows the manifest's URL only on the registry's own
// host, so a manifest cannot point fetch elsewhere.
func fetchRegistry(ctx context.Context, o FlowsFetchOptions, overlay openapi3.Schemas) error {
	data, err := o.Fetcher.Get(ctx, o.Registry)
	if err != nil {
		return err
	}
	var manifest struct {
		Apps map[string]struct {
			AppMetadataURL string `json:"appMetadataUrl"`
		} `json:"apps"`
	}
	if err := validateJSON(overlay, "RegistryManifest", data); err != nil {
		return fmt.Errorf("registry %s: %w", o.Registry, err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("decode %s: %w", o.Registry, err)
	}
	keys := sortedKeys(manifest.Apps)
	if len(keys) == 0 {
		return fmt.Errorf("%w: registry %s lists no apps", ErrFlowsFetch, o.Registry)
	}
	versions := manifest.Apps[keys[0]].AppMetadataURL
	reg, err := url.Parse(o.Registry)
	if err != nil {
		return fmt.Errorf("registry URL: %w", err)
	}
	if u, err := url.Parse(versions); err != nil || u.Host != reg.Host || u.Scheme != reg.Scheme {
		return fmt.Errorf("%w: app %s's versions file %q is not on %s", ErrFlowsFetch, keys[0], versions, reg.Host)
	}
	if data, err = o.Fetcher.Get(ctx, versions); err != nil {
		return err
	}
	if err := validateJSON(overlay, "AppVersions", data); err != nil {
		return fmt.Errorf("app %s versions: %w", keys[0], err)
	}
	return nil
}

// ValidateFlowDefinition checks a flow's YAML definition against the
// overlay's FlowDefinition. An !expr scalar is read as its source text.
func ValidateFlowDefinition(overlay openapi3.Schemas, data []byte) error {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return fmt.Errorf("decode flow: %w", err)
	}
	v, err := yamlValue(&node)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode flow: %w", err)
	}
	return validateJSON(overlay, "FlowDefinition", raw)
}

func validateJSON(overlay openapi3.Schemas, name string, data []byte) error {
	s, ok := overlay[name]
	if !ok || s.Value == nil {
		return fmt.Errorf("%w: it has no %s", ErrFlowsOverlay, name)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if err := s.Value.VisitJSON(v, openapi3.MultiErrors()); err != nil {
		var me openapi3.MultiError
		if errors.As(err, &me) {
			msgs := make([]string, 0, len(me))
			for _, e := range me {
				msgs = append(msgs, oneLineError(e))
			}
			sort.Strings(msgs)
			return fmt.Errorf("%w: %s: %s", ErrFlowsOverlay, name, strings.Join(msgs, "; "))
		}
		return fmt.Errorf("%w: %s: %s", ErrFlowsOverlay, name, oneLineError(err))
	}
	return nil
}

func oneLineError(err error) string {
	var se *openapi3.SchemaError
	if errors.As(err, &se) {
		return "/" + strings.Join(se.JSONPointer(), "/") + ": " + se.Reason
	}
	return strings.Join(strings.Fields(err.Error()), " ")
}

// yamlValue converts a YAML node to JSON-shaped values, reading a tagged
// scalar such as !expr as its text.
func yamlValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return yamlValue(n.Content[0])
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			v, err := yamlValue(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			out[n.Content[i].Value] = v
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := yamlValue(c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.AliasNode:
		return yamlValue(n.Alias)
	}
	if strings.HasPrefix(n.Tag, "!") && !strings.HasPrefix(n.Tag, "!!") {
		return n.Value, nil
	}
	var v any
	if err := n.Decode(&v); err != nil {
		return nil, fmt.Errorf("line %d: %w", n.Line, err)
	}
	return v, nil
}
