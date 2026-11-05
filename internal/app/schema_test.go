package app

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

// apiImport is an allow entry of a graph rule naming another domain's api/.
var apiImport = regexp.MustCompile(`^` + regexp.QuoteMeta(module) + `/internal/([a-z]+)/api[$/]$`)

// declaredGraph reads decision 0004's declared graph from the graph-<domain>
// depguard rules: each domain and the domains whose api/ it may import.
func declaredGraph(rules map[string]depguardRule) map[string][]string {
	graph := map[string][]string{}
	for name, r := range rules {
		domain, ok := strings.CutPrefix(name, "graph-")
		if !ok {
			continue
		}
		graph[domain] = []string{}
		for _, a := range r.Allow {
			if m := apiImport.FindStringSubmatch(a); m != nil && m[1] != domain {
				graph[domain] = append(graph[domain], m[1])
			}
		}
	}
	return graph
}

// mayReference reports whether a table of from may reference one of to:
// within a stream, from a domain to the platform, and from a domain to a
// domain it imports.
func mayReference(graph map[string][]string, from, to string) bool {
	switch {
	case from == to:
		return true
	case from == schema.Name:
		return false
	case to == schema.Name:
		return true
	}
	return slices.Contains(graph[from], to)
}

// graphProblems checks the declared graph and the streams' order against
// it, without a database: a cycle, a stream that names no domain, and a
// stream registered before the platform's or before a domain it imports.
func graphProblems(streams []migrate.Stream, graph map[string][]string) []string {
	var out []string
	const (
		unseen = iota
		open
		done
	)
	state := map[string]int{}
	var visit func(d string, path []string)
	visit = func(d string, path []string) {
		switch state[d] {
		case open:
			i := slices.Index(path, d)
			out = append(
				out,
				"the declared graph has a cycle: "+strings.Join(append(path[i:], d), " → "),
			)
			return
		case done:
			return
		}
		state[d] = open
		for _, next := range graph[d] {
			visit(next, append(path, d))
		}
		state[d] = done
	}
	for _, d := range slices.Sorted(maps.Keys(graph)) {
		visit(d, nil)
	}
	at := map[string]int{}
	for i, s := range streams {
		at[s.Name] = i
	}
	for i, s := range streams {
		if s.Name == schema.Name {
			continue
		}
		if _, ok := graph[s.Name]; !ok {
			out = append(
				out,
				fmt.Sprintf("stream %s names no domain of the declared graph", s.Name),
			)
		}
		for _, dep := range append([]string{schema.Name}, graph[s.Name]...) {
			if j, ok := at[dep]; ok && j > i {
				out = append(
					out,
					fmt.Sprintf(
						"stream %s is registered before %s, which it may reference",
						s.Name,
						dep,
					),
				)
			}
		}
	}
	return out
}

// relations are the tables and views of the public schema.
func relations(ctx context.Context, conn *pgx.Conn) (map[string]bool, error) {
	rows, err := conn.Query(ctx, `SELECT relname FROM pg_class
WHERE relnamespace = 'public'::regnamespace AND relkind IN ('r', 'p', 'v', 'm')`)
	if err != nil {
		return nil, err //nolint:wrapcheck // test helper
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	out := map[string]bool{}
	for _, n := range names {
		out[n] = true
	}
	return out, err //nolint:wrapcheck // test helper
}

// references are the foreign keys and view dependencies between relations,
// with each foreign key's ON DELETE action; a view's is empty.
const references = `
SELECT conrelid::regclass::text, confrelid::regclass::text, conname, confdeltype::text
FROM pg_constraint
WHERE contype = 'f' AND connamespace = 'public'::regnamespace
UNION
SELECT v.relname, t.relname, 'view ' || v.relname, ''
FROM pg_depend d
JOIN pg_rewrite r ON r.oid = d.objid
JOIN pg_class v ON v.oid = r.ev_class
JOIN pg_class t ON t.oid = d.refobjid
WHERE d.classid = 'pg_rewrite'::regclass AND d.refclassid = 'pg_class'::regclass
  AND v.oid <> t.oid AND t.relnamespace = 'public'::regnamespace
ORDER BY 1, 2, 3`

// schemaDependencyProblems applies streams one at a time to the empty
// database at url, attributes the relations each creates to its stream, and
// checks every reference between streams against graph: it must follow the
// graph, and a foreign key must neither cascade nor set a value on delete
// (decision 0004).
func schemaDependencyProblems(
	t *testing.T,
	url string,
	streams []migrate.Stream,
	graph map[string][]string,
) []string {
	t.Helper()
	out := graphProblems(streams, graph)
	ctx := t.Context()
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	owner := map[string]string{}
	for i, s := range streams {
		before, err := relations(ctx, conn)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := migrate.Up(ctx, cfg, "schema-check", streams[:i+1]); err != nil {
			return append(out, fmt.Sprintf("stream %s does not apply: %v", s.Name, err))
		}
		after, err := relations(ctx, conn)
		if err != nil {
			t.Fatal(err)
		}
		for rel := range after {
			if !before[rel] && rel != "schema_migrations" && rel != "schema_state" {
				owner[rel] = s.Name
			}
		}
	}
	rows, err := conn.Query(ctx, references)
	if err != nil {
		t.Fatal(err)
	}
	var from, to, name, onDelete string
	if _, err := pgx.ForEachRow(rows, []any{&from, &to, &name, &onDelete}, func() error {
		a, b := owner[from], owner[to]
		switch {
		case a == "" || b == "" || a == b:
		case !mayReference(graph, a, b):
			out = append(out, fmt.Sprintf("%s: %s (%s) references %s (%s), which %s may not import",
				name, from, a, to, b, a))
		case onDelete != "" && onDelete != "r" && onDelete != "a":
			out = append(
				out,
				fmt.Sprintf(
					"%s: %s (%s) references %s (%s) with an ON DELETE action that rewrites rows; use RESTRICT",
					name,
					from,
					a,
					to,
					b,
				),
			)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// conventions are decision 0005's schema conventions as queries; each row
// is a breach.
var conventions = []struct{ rule, query string }{
	{"foreign key without an index", `
SELECT c.conrelid::regclass::text || '.' || c.conname
FROM pg_constraint c
WHERE c.contype = 'f' AND c.connamespace = 'public'::regnamespace
  AND NOT EXISTS (
    SELECT 1 FROM pg_index i
    WHERE i.indrelid = c.conrelid
      AND (SELECT array_agg(k ORDER BY o) FROM unnest(i.indkey::int2[]) WITH ORDINALITY u(k, o)
           WHERE o <= cardinality(c.conkey)) @> c.conkey
      AND cardinality(i.indkey::int2[]) >= cardinality(c.conkey)
      AND (SELECT array_agg(k ORDER BY o) FROM unnest(i.indkey::int2[]) WITH ORDINALITY u(k, o)
           WHERE o <= cardinality(c.conkey)) <@ c.conkey)`},
	{"timestamp without time zone", `
SELECT table_name || '.' || column_name FROM information_schema.columns
WHERE table_schema = 'public' AND data_type = 'timestamp without time zone'`},
	{"enum type", `
SELECT typname::text FROM pg_type WHERE typtype = 'e' AND typnamespace = 'public'::regnamespace`},
	{"_sealed column that is not bytea", `
SELECT table_name || '.' || column_name FROM information_schema.columns
WHERE table_schema = 'public' AND column_name LIKE '%\_sealed' AND data_type <> 'bytea'`},
	{"table without account_id uuid not null and no global: comment", `
SELECT c.relname::text FROM pg_class c
WHERE c.relnamespace = 'public'::regnamespace AND c.relkind IN ('r', 'p') AND NOT c.relispartition
  AND coalesce(obj_description(c.oid, 'pg_class'), '') NOT LIKE 'global:%'
  AND NOT EXISTS (
    SELECT 1 FROM pg_attribute a
    WHERE a.attrelid = c.oid AND a.attname = 'account_id' AND a.atttypid = 'uuid'::regtype
      AND a.attnotnull AND NOT a.attisdropped)`},
}

func schemaConventionProblems(t *testing.T, url string) []string {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var out []string
	for _, c := range conventions {
		rows, err := conn.Query(t.Context(), c.query+" ORDER BY 1")
		if err != nil {
			t.Fatalf("%s: %v", c.rule, err)
		}
		names, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("%s: %v", c.rule, err)
		}
		for _, n := range names {
			out = append(out, c.rule+": "+n)
		}
	}
	return out
}

// TestAccSchemaDependencies holds every registered stream's tables and
// views to the declared graph in .golangci.yml (decision 0004).
func TestAccSchemaDependencies(t *testing.T) {
	graph := declaredGraph(readDepguardRules(t, "../../.golangci.yml"))
	if got := schemaDependencyProblems(t, sqltest.Empty(t), Streams(), graph); len(got) != 0 {
		t.Errorf("schema dependencies:\n%s", strings.Join(got, "\n"))
	}
}

// TestAccSchemaConventions holds the fully migrated schema to decision
// 0005's conventions.
func TestAccSchemaConventions(t *testing.T) {
	if got := schemaConventionProblems(t, sqltest.DSN(t)); len(got) != 0 {
		t.Errorf("schema conventions:\n%s", strings.Join(got, "\n"))
	}
}

func fixtureStream(name string, sql ...string) migrate.Stream {
	m := fstest.MapFS{}
	for i, s := range sql {
		m[fmt.Sprintf("%05d_f%d.sql", i+1, i+1)] = &fstest.MapFile{Data: []byte(s)}
	}
	return migrate.Stream{Name: name, Files: m}
}

// TestAccSchemaDependencyFixtures proves each failure of the dependency
// check on fixture streams and rules: platform, then domain a, then b,
// which imports a.
func TestAccSchemaDependencyFixtures(t *testing.T) {
	rules := map[string]depguardRule{
		"graph-a": {
			Allow: []string{"$gostd", module + "/internal/platform/", module + "/internal/a/"},
		},
		"graph-b":  {Allow: []string{module + "/internal/a/api$", module + "/internal/b/"}},
		"devtools": {Allow: []string{module + "/internal/c/api$"}},
	}
	graph := declaredGraph(rules)
	if !slices.Equal(graph["b"], []string{"a"}) || len(graph["a"]) != 0 || len(graph) != 2 {
		t.Fatalf("declared graph = %v", graph)
	}
	platform := fixtureStream(schema.Name, "CREATE TABLE p (id int PRIMARY KEY);")
	a := fixtureStream("a", "CREATE TABLE a (id int PRIMARY KEY, p int REFERENCES p);")
	for _, tc := range []struct {
		name    string
		streams []migrate.Stream
		graph   map[string][]string
		want    []string
	}{
		{
			name: "references along the graph",
			streams: []migrate.Stream{platform, a, fixtureStream("b",
				"CREATE TABLE b (id int PRIMARY KEY, a int REFERENCES a ON DELETE RESTRICT, p int REFERENCES p);"+
					"CREATE TABLE b2 (id int REFERENCES b ON DELETE CASCADE);"+
					"CREATE VIEW bv AS SELECT a.id FROM a JOIN p ON p.id = a.p;")},
		},
		{
			name: "a reference against the graph",
			streams: []migrate.Stream{
				platform, fixtureStream("b", "CREATE TABLE b (id int PRIMARY KEY);"),
				fixtureStream("a", "CREATE TABLE a (id int PRIMARY KEY, b int REFERENCES b);"),
			},
			graph: map[string][]string{"a": {}, "b": {}},
			want:  []string{"a_b_fkey: a (a) references b (b), which a may not import"},
		},
		{
			name:    "a view against the graph",
			streams: []migrate.Stream{platform, fixtureStream("b", "CREATE TABLE b (id int);"), fixtureStream("a", "CREATE VIEW av AS SELECT id FROM b;")},
			graph:   map[string][]string{"a": {}, "b": {}},
			want:    []string{"view av: av (a) references b (b), which a may not import"},
		},
		{
			name:    "a cross-domain key that cascades",
			streams: []migrate.Stream{platform, a, fixtureStream("b", "CREATE TABLE b (a int REFERENCES a ON DELETE CASCADE);")},
			want:    []string{"b_a_fkey: b (b) references a (a) with an ON DELETE action that rewrites rows; use RESTRICT"},
		},
		{
			name:    "a cross-domain key that sets null",
			streams: []migrate.Stream{platform, a, fixtureStream("b", "CREATE TABLE b (a int REFERENCES a ON DELETE SET NULL);")},
			want:    []string{"b_a_fkey: b (b) references a (a) with an ON DELETE action that rewrites rows; use RESTRICT"},
		},
		{
			name: "the platform referencing a domain",
			streams: []migrate.Stream{
				fixtureStream("a", "CREATE TABLE a (id int PRIMARY KEY);"),
				fixtureStream(schema.Name, "CREATE TABLE p (a int REFERENCES a);"),
			},
			want: []string{
				"stream a is registered before platform, which it may reference",
				"p_a_fkey: p (platform) references a (a), which platform may not import",
			},
		},
		{
			name:    "a stream before one it imports",
			streams: []migrate.Stream{platform, fixtureStream("b", "SELECT 1;"), a},
			want:    []string{"stream b is registered before a, which it may reference"},
		},
		{
			name:    "a cycle",
			streams: []migrate.Stream{platform},
			graph:   map[string][]string{"a": {"b"}, "b": {"a"}},
			want:    []string{"the declared graph has a cycle: a → b → a"},
		},
		{
			name:    "a stream outside the graph",
			streams: []migrate.Stream{platform, fixtureStream("z", "SELECT 1;")},
			want:    []string{"stream z names no domain of the declared graph"},
		},
		{
			name:    "a stream that fails",
			streams: []migrate.Stream{platform, fixtureStream("a", "CREATE TABLE a (b int REFERENCES nowhere);")},
			want:    []string{`stream a does not apply: migrate: a 00001_f1: ERROR: relation "nowhere" does not exist (SQLSTATE 42P01)`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := graph
			if tc.graph != nil {
				g = tc.graph
			}
			got := schemaDependencyProblems(t, sqltest.Empty(t), tc.streams, g)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf(
					"got:\n%s\nwant:\n%s",
					strings.Join(got, "\n"),
					strings.Join(tc.want, "\n"),
				)
			}
		})
	}
}

// TestAccSchemaConventionFixtures proves each convention fires once.
func TestAccSchemaConventionFixtures(t *testing.T) {
	url := sqltest.Empty(t)
	s := fixtureStream("a", `
CREATE TYPE mood AS ENUM ('ok');
CREATE TABLE accounts (id uuid PRIMARY KEY);
COMMENT ON TABLE accounts IS 'global: the tenants themselves';
CREATE TABLE good (
	account_id uuid NOT NULL REFERENCES accounts,
	key_sealed bytea,
	at timestamptz
);
CREATE INDEX good_account ON good (account_id);
CREATE TABLE bad (
	account_id uuid REFERENCES accounts,
	key_sealed text,
	at timestamp
);`)
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Up(t.Context(), cfg, "fixture", []migrate.Stream{s}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"foreign key without an index: bad.bad_account_id_fkey",
		"timestamp without time zone: bad.at",
		"enum type: mood",
		"_sealed column that is not bytea: bad.key_sealed",
		"table without account_id uuid not null and no global: comment: bad",
	}
	if got := schemaConventionProblems(
		t,
		url,
	); strings.Join(
		got,
		"\n",
	) != strings.Join(
		want,
		"\n",
	) {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// sealedColumns are the migrated schema's _sealed columns, each as
// table.column(primary key columns), in the form sealedEntry gives a
// crypt.Column.
func sealedColumns(t *testing.T, url string) []string {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	rows, err := conn.Query(t.Context(), `
SELECT c.relname || '.' || a.attname || '(' || coalesce((
	SELECT string_agg(k.attname, ',' ORDER BY x.n)
	FROM pg_index i
	CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS x(attnum, n)
	JOIN pg_attribute k ON k.attrelid = i.indrelid AND k.attnum = x.attnum
	WHERE i.indrelid = c.oid AND i.indisprimary), '') || ')'
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid
JOIN pg_namespace s ON s.oid = c.relnamespace
WHERE s.nspname = 'public' AND c.relkind = 'r'
  AND a.attname LIKE '%\_sealed' AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func sealedEntry(c crypt.Column) string {
	return c.Table + "." + c.Name + "(" + strings.Join(c.Key, ",") + ")"
}

// sealedRegistryProblems compares the schema's _sealed columns with
// registry: a column resealing would miss, and an entry that names no
// column or the wrong primary key.
func sealedRegistryProblems(schema []string, registry []crypt.Column) []string {
	listed := map[string]bool{}
	for _, c := range registry {
		listed[sealedEntry(c)] = true
	}
	var out []string
	for _, s := range schema {
		if !listed[s] {
			out = append(out, "not in crypt.Registry: "+s)
		}
		delete(listed, s)
	}
	for _, e := range slices.Sorted(maps.Keys(listed)) {
		out = append(out, "in crypt.Registry, not in the schema: "+e)
	}
	return out
}

// TestAccSealedColumnRegistry holds crypt.Registry to the fully migrated
// schema (decision 0013): every _sealed column is listed with its primary
// key, so resealing after a rotation reaches it, and nothing else is.
func TestAccSealedColumnRegistry(t *testing.T) {
	if got := sealedRegistryProblems(
		sealedColumns(t, sqltest.DSN(t)),
		crypt.Registry(),
	); len(
		got,
	) != 0 {
		t.Errorf("sealed columns:\n%s", strings.Join(got, "\n"))
	}
}

// TestAccSealedColumnRegistryFixtures proves the comparison fires both
// ways and on a wrong primary key.
func TestAccSealedColumnRegistryFixtures(t *testing.T) {
	url := sqltest.Empty(t)
	s := fixtureStream("a", `
CREATE TABLE hooks (account_id uuid, id uuid, secret_sealed bytea, PRIMARY KEY (account_id, id));
CREATE TABLE keys (id integer PRIMARY KEY, key_sealed bytea, note_sealed bytea);`)
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Up(t.Context(), cfg, "fixture", []migrate.Stream{s}); err != nil {
		t.Fatal(err)
	}
	registry := []crypt.Column{
		{Table: "hooks", Name: "secret_sealed", Key: []string{"account_id", "id"}},
		{Table: "keys", Name: "key_sealed", Key: []string{"account_id", "id"}},
		{Table: "gone", Name: "value_sealed", Key: []string{"id"}},
	}
	want := []string{
		"not in crypt.Registry: keys.key_sealed(id)",
		"not in crypt.Registry: keys.note_sealed(id)",
		"in crypt.Registry, not in the schema: gone.value_sealed(id)",
		"in crypt.Registry, not in the schema: keys.key_sealed(account_id,id)",
	}
	if got := sealedRegistryProblems(
		sealedColumns(t, url),
		registry,
	); strings.Join(
		got,
		"\n",
	) != strings.Join(
		want,
		"\n",
	) {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
