// Package workerchart keeps the infraweave-workers chart's values schema
// in step with its WorkerPool CRD. Each entry of the chart's pools value
// carries a WorkerPool spec, which values.schema.json types exactly as the
// CRD's structural schema does, closed with additionalProperties: false,
// so an unknown or misspelt key fails the install rather than reaching
// the API server, which would prune it.
//
// PoolSpec converts the CRD's spec schema to JSON Schema: it drops the
// Kubernetes extensions and formats, which the API server checks itself
// once the release applies the resource, and closes every object that
// names its properties. Render writes that conversion into a values
// schema as $defs.workerPoolSpec, and Check reports whether a chart's
// committed schema is what Render writes.
//
// # Input and output
//
// PoolSpec reads a CRD manifest, Render a values schema and a CRD, and
// Write and Check a chart directory's crds/workerpools.yaml and
// values.schema.json. Write rewrites values.schema.json with its keys
// sorted and two-space indentation. Nothing touches the network.
//
// # Dependencies
//
// It imports sigs.k8s.io/yaml to read the CRD and encoding/json to write
// the schema. tools/workerchart runs it: make generate writes the schema
// and make verify-generate fails on a stale one.
package workerchart
