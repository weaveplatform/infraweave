package specref

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrUnknownProduct is returned for a product name no reference has.
var ErrUnknownProduct = errors.New("specref: unknown product")

// Product is one Spacelift product's reference: a directory of pinned
// snapshots and a hand-written mapping, and the document and coverage report
// generated from them. Each product reads its own sources, keeps its own
// sources.lock.yaml and titles its own document and report; scaffold and
// parity read any product's document through Spec.
type Product struct {
	// Name selects the product on the command line: specref --product.
	Name string
	// Dir is the reference directory, relative to the repository root.
	Dir string
	// Spec is the generated document's file name in Dir.
	Spec string
	// Baseline is the parity baseline that accepts the reference operations
	// the served document does not serve unchanged.
	Baseline string

	generate func(ctx context.Context, dir string) (*Generated, error)
	pins     func(lock []byte) (map[string]string, error)
}

// Deploy is Spacelift's IaC orchestration product, projected from its
// GraphQL schema, the spacelift-api-bruno collection and its Terraform
// provider (report 02).
var Deploy = Product{
	Name:     "deploy",
	Dir:      "api/reference/spacelift",
	Spec:     FileSpec,
	Baseline: "api/parity-baseline.deploy.json",
	generate: func(ctx context.Context, dir string) (*Generated, error) {
		src, err := ReadSources(dir)
		if err != nil {
			return nil, err
		}
		return Generate(ctx, src)
	},
	pins: deployPins,
}

// Products lists every product with a reference, in a fixed order.
func Products() []Product { return []Product{Deploy} }

// ProductNamed is the product called name.
func ProductNamed(name string) (Product, error) {
	for _, p := range Products() {
		if p.Name == name {
			return p, nil
		}
	}
	names := make([]string, 0, len(Products()))
	for _, p := range Products() {
		names = append(names, p.Name)
	}
	return Product{}, fmt.Errorf("%w %q; want one of %s", ErrUnknownProduct, name,
		strings.Join(names, ", "))
}

// Generate builds the product's document and coverage report from the
// sources in dir, without writing them.
func (p Product) Generate(ctx context.Context, dir string) (*Generated, error) {
	return p.generate(ctx, dir)
}

// GenerateDir regenerates the product's reference in dir in place.
func (p Product) GenerateDir(ctx context.Context, dir string) (*Generated, error) {
	g, err := p.generate(ctx, dir)
	if err != nil {
		return nil, err
	}
	return g, writeFiles(dir, p.outputs(g))
}

// CheckDir fails with ErrDrift when the committed document or report in dir
// is not what the committed sources generate.
func (p Product) CheckDir(ctx context.Context, dir string) error {
	g, err := p.generate(ctx, dir)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open reference dir: %w", err)
	}
	defer root.Close()
	var drift []string
	outputs := p.outputs(g)
	for _, name := range sortedKeys(outputs) {
		got, err := root.ReadFile(name)
		if err != nil || !bytes.Equal(got, outputs[name]) {
			drift = append(drift, name)
		}
	}
	if len(drift) > 0 {
		return fmt.Errorf("%w: %s (run specref generate)", ErrDrift, strings.Join(drift, ", "))
	}
	return nil
}

func (p Product) outputs(g *Generated) map[string][]byte {
	return map[string][]byte{p.Spec: g.Spec, FileCoverage: g.Coverage}
}

// Pins are the fetch settings the product's sources.lock.yaml in dir pins,
// keyed by specref fetch's flag names, so a fetch nobody overrides
// refetches what the lock names. They are nil when dir has no lock yet.
func (p Product) Pins(dir string) (map[string]string, error) {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dir, err)
	}
	defer root.Close()
	data, err := root.ReadFile(FileLock)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read lock: %w", err)
	}
	return p.pins(data)
}

func deployPins(data []byte) (map[string]string, error) {
	var lock Lock
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("decode lock: %w", err)
	}
	return map[string]string{
		"endpoint":         lock.Introspection.Endpoint,
		"bruno-repo":       lock.Bruno.Repository,
		"bruno-ref":        lock.Bruno.Ref,
		"provider-repo":    lock.Provider.Repository,
		"provider-version": strings.TrimPrefix(lock.Provider.Ref, "v"),
	}, nil
}
