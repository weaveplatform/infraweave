package specref

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// The inventory is two files: the register docs/data/graphql-inventory.json,
// one record per named type of the schema, and the data-model inventory
// report docs/research/reference/29-data-model-inventory.md, which says what
// the register holds and counts it. Everything either prints comes from the
// schema and the lock, including the research baseline (the fetch date, not
// today), so regenerating from unchanged snapshots is byte-identical and
// make verify-reference can diff both.
//
// Descriptions are left out on purpose: they are Spacelift's prose and carry
// no stated licence, whereas type, field and value names are facts about the
// interface.

// inventoryRegister is the register's document; its shape is
// docs/data/schema/graphql-inventory.schema.json.
type inventoryRegister struct {
	//nolint:tagliatelle // $schema is the register convention of docs/data
	Schema  string          `json:"$schema"`
	Records []inventoryType `json:"records"`
}

type inventoryType struct {
	ID            string           `json:"id"`
	Kind          string           `json:"kind"`
	Implements    []string         `json:"implements,omitempty"`
	ImplementedBy []string         `json:"implementedBy,omitempty"`
	Fields        []inventoryField `json:"fields,omitempty"`
	Values        []inventoryValue `json:"values,omitempty"`
	Members       []string         `json:"members,omitempty"`
}

// inventoryField is an output field, an input field or an argument. Only an
// output field has args and deprecation; only an input field or an argument
// has a default. The snapshot's introspection query does not ask for input
// field deprecation, so input fields carry none.
type inventoryField struct {
	Name              string           `json:"name"`
	Args              []inventoryField `json:"args,omitempty"`
	Type              string           `json:"type"`
	Nullable          bool             `json:"nullable"`
	Default           *string          `json:"default,omitempty"`
	Deprecated        bool             `json:"deprecated,omitempty"`
	DeprecationReason string           `json:"deprecationReason,omitempty"`
}

type inventoryValue struct {
	Name              string `json:"name"`
	Deprecated        bool   `json:"deprecated,omitempty"`
	DeprecationReason string `json:"deprecationReason,omitempty"`
}

// inventoryKinds orders the register's records and the report's summary.
var inventoryKinds = []struct{ kind, title string }{
	{KindObject, "Object types"},
	{KindInterface, "Interfaces"},
	{KindUnion, "Unions"},
	{KindInputObject, "Input objects"},
	{KindEnum, "Enums"},
	{KindScalar, "Scalars"},
}

// RegisterSchema is the $schema member of the inventory register, relative to
// docs/data as docscheck requires.
const RegisterSchema = "schema/graphql-inventory.schema.json"

// inventoryTypes groups the schema's named types by kind, each group sorted
// by name. GraphQL's own __Schema, __Type, … describe introspection, not
// Spacelift, so they are left out.
func inventoryTypes(s *Schema) map[string][]*Type {
	types := make([]*Type, 0, len(s.Types))
	for _, t := range s.Types {
		if !strings.HasPrefix(t.Name, "__") {
			types = append(types, t)
		}
	}
	sort.Slice(types, func(i, j int) bool { return types[i].Name < types[j].Name })
	byKind := map[string][]*Type{}
	for _, t := range types {
		byKind[t.Kind] = append(byKind[t.Kind], t)
	}
	return byKind
}

// InventoryRegister renders the register: two-space indented JSON with a
// trailing newline, records ordered by kind as inventoryKinds lists them and
// then by name, fields and values in upstream order.
func InventoryRegister(s *Schema) []byte {
	byKind := inventoryTypes(s)
	doc := inventoryRegister{Schema: RegisterSchema, Records: []inventoryType{}}
	for _, k := range inventoryKinds {
		for _, t := range byKind[k.kind] {
			doc.Records = append(doc.Records, inventoryRecord(t))
		}
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	// Defaults and deprecation reasons hold <, > and &, which the default
	// escaping would turn into < and friends.
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	// The document is strings, bools and slices of them, which always encode.
	_ = enc.Encode(doc)
	return b.Bytes()
}

func inventoryRecord(t *Type) inventoryType {
	r := inventoryType{ID: t.Name, Kind: t.Kind}
	switch t.Kind {
	case KindObject, KindInterface:
		r.Implements = refNames(t.Interfaces)
		r.ImplementedBy = refNames(t.PossibleTypes)
		for _, f := range t.Fields {
			out := inventoryField{
				Name:       f.Name,
				Type:       f.Type.String(),
				Nullable:   !f.Type.NonNull(),
				Deprecated: f.IsDeprecated,
			}
			if f.IsDeprecated {
				out.DeprecationReason = strings.TrimSpace(f.DeprecationReason)
			}
			for _, a := range f.Args {
				out.Args = append(out.Args, inventoryInput(a))
			}
			r.Fields = append(r.Fields, out)
		}
	case KindUnion:
		r.Members = refNames(t.PossibleTypes)
	case KindInputObject:
		for _, f := range t.InputFields {
			r.Fields = append(r.Fields, inventoryInput(f))
		}
	case KindEnum:
		for _, v := range t.EnumValues {
			out := inventoryValue{Name: v.Name, Deprecated: v.IsDeprecated}
			if v.IsDeprecated {
				out.DeprecationReason = strings.TrimSpace(v.DeprecationReason)
			}
			r.Values = append(r.Values, out)
		}
	}
	return r
}

func inventoryInput(v InputValue) inventoryField {
	return inventoryField{
		Name:     v.Name,
		Type:     v.Type.String(),
		Nullable: !v.Type.NonNull(),
		Default:  v.DefaultValue,
	}
}

// Inventory renders the data-model inventory report. Its links assume it
// lives in docs/research/reference and the register in docs/data.
func Inventory(s *Schema, lock *Lock) []byte {
	byKind := inventoryTypes(s)
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	pins := inventoryPins(lock)
	w("# 29: Data model inventory\n\n")
	w(
		"Checked against: the introspection snapshot `%s` (fetched %s), pinned in [`sources.lock.yaml`](../../../api/reference/spacelift/sources.lock.yaml).\n\n",
		pins.short,
		pins.fetched,
	)
	w("Research baseline: **%s**.\n\n", pins.date)
	w(
		"Generated by `make reference-inventory` from `api/reference/spacelift/introspection.json`, together with the register [`graphql-inventory.json`](../../data/graphql-inventory.json); do not edit either.\n\n",
	)
	w(
		"The register holds every type of Spacelift's GraphQL schema, one record per type whose `id` is the type name, so other reports can cite a type, field or enum value without opening the snapshot. Descriptions are left out: they are Spacelift's prose and carry no stated licence. Report [28](28-data-model.md) builds the entity model on this inventory.\n\n",
	)
	w("## Reading the register\n\n")
	w(
		"A record's `kind` is the GraphQL type kind. `OBJECT` and `INTERFACE` records list their `fields` in upstream order, each with its `args`, its `type` in SDL notation such as `[String!]!`, whether it is `nullable`, and `deprecated` with the `deprecationReason` upstream gives, if any; `implements` and `implementedBy` link interfaces and the types that implement them. `INPUT_OBJECT` records list `fields` with their `default` and no deprecation, because the snapshot's introspection query does not ask for input field deprecation. `ENUM` records list `values`, `UNION` records list `members`, and `SCALAR` records hold only the name. Root queries and mutations are the fields of the `Query` and `Mutation` records. Records run in the order of the summary below, by name within a kind.\n\n",
	)
	w("## Summary\n\n| Kind | Types | Fields, values or members |\n|---|---|---|\n")
	for _, k := range inventoryKinds {
		members := 0
		for _, t := range byKind[k.kind] {
			members += len(t.Fields) + len(t.InputFields) + len(t.EnumValues)
			if t.Kind == KindUnion {
				members += len(t.PossibleTypes)
			}
		}
		w("| %s | %d | %d |\n", k.title, len(byKind[k.kind]), members)
	}
	w("\n## Sources\n\n")
	w(
		"- [`introspection.json`](../../../api/reference/spacelift/introspection.json): introspection of %s, sha256 `%s`, fetched %s.\n",
		pins.endpoint,
		pins.sha,
		pins.fetched,
	)
	w(
		"- [`sources.lock.yaml`](../../../api/reference/spacelift/sources.lock.yaml): the pins of every upstream input.\n",
	)
	return []byte(b.String())
}

type pinText struct{ short, sha, fetched, date, endpoint string }

func inventoryPins(lock *Lock) pinText {
	const unknown = "not recorded"
	p := pinText{short: unknown, sha: unknown, fetched: unknown, date: unknown, endpoint: unknown}
	if lock == nil {
		return p
	}
	in := lock.Introspection
	if in.SHA256 != "" {
		p.sha = in.SHA256
		p.short = strings.TrimPrefix(lock.version(), "schema-")
	}
	if in.FetchedAt != "" {
		p.fetched = in.FetchedAt
		p.date, _, _ = strings.Cut(in.FetchedAt, "T")
	}
	if in.Endpoint != "" {
		p.endpoint = "<" + in.Endpoint + ">"
	}
	return p
}

func refNames(refs []TypeRef) []string {
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.Named().Name
	}
	return out
}

// InventoryOutputs names the two files of the inventory.
type InventoryOutputs struct {
	Report   string
	Register string
}

func inventoryFiles(dir string, out InventoryOutputs) (map[string][]byte, error) {
	src, err := ReadSources(dir)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		out.Report:   Inventory(src.Schema, src.Lock),
		out.Register: InventoryRegister(src.Schema),
	}, nil
}

// InventoryFile writes the inventory of the reference directory dir.
func InventoryFile(dir string, out InventoryOutputs) error {
	files, err := inventoryFiles(dir, out)
	if err != nil {
		return err
	}
	for _, p := range []string{out.Report, out.Register} {
		if err := os.WriteFile(p, files[p], filePerm); err != nil {
			return fmt.Errorf("specref: %w", err)
		}
	}
	return nil
}

// CheckInventory fails with ErrDrift, naming each stale file, when the
// committed inventory is not what the reference directory dir generates.
func CheckInventory(dir string, out InventoryOutputs) error {
	files, err := inventoryFiles(dir, out)
	if err != nil {
		return err
	}
	var drift error
	for _, p := range []string{out.Report, out.Register} {
		got, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(got, files[p]) {
			drift = errors.Join(
				drift,
				fmt.Errorf("%w: %s (run make reference-inventory)", ErrDrift, p),
			)
		}
	}
	return drift
}
