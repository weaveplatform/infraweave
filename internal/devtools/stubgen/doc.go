// Package stubgen writes UnimplementedStrict, a type that implements a
// generated StrictServerInterface by answering 501 for every operation.
// oapi-codegen emits an unimplemented type for the plain ServerInterface but
// not for the strict one, and the strict one is what the server implements;
// without the stub every scaffolded operation would need a hand-written
// stub before the tree compiles.
//
// Parse reads the interface's methods from the generated source,
// OperationIDs maps each method to its operationId and route in the bundle,
// Generate renders the stubs, and Run does all three for one generated
// package (make generate).
//
// # Input and output
//
// Run reads the generated server.gen.go and the bundled spec, and writes
// unimplemented.gen.go beside server.gen.go
// (internal/platform/gen/<spec>/). Each stub returns a
// problem.NotImplementedError naming its operationId and route.
//
// # Dependencies
//
// It imports go/ast and go/parser to read the interface, go/format for the
// output, and yaml.v3 for the spec. The code it writes imports
// platform/httpkit/problem (ProblemImport). tools/stubgen runs it.
package stubgen
