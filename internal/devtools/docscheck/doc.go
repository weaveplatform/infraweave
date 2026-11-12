// Package docscheck checks the structure of the documentation tree, so that a
// moved or renamed document cannot leave dangling references behind and a
// research report or decision record cannot merge without the parts readers
// rely on. make docs-check runs it over the repository, and the gate blocks
// on any finding.
//
// The checks:
//   - every relative markdown link under docs/ resolves, including its
//     #anchor when the target is markdown;
//   - every numbered report in docs/research/reference/ has its
//     "Checked against:" and "Research baseline:" lines and a "## Sources"
//     section (docs/research/README.md, Conventions);
//   - every decision record has a Status of Proposed, Accepted or Superseded,
//     and docs/research/decisions/README.md lists it with that status;
//   - every docs/….md path named in the repository's source, configuration
//     and documentation exists;
//   - every tag of each Spacelift product's reference spec (specref's
//     Products: Deploy and Flows) has a tag: record of that product in
//     docs/data/capabilities.json, and every tag: record is a tag of its
//     product's spec, so the scope accounts for every area of both APIs;
//   - every operations entry of a superseded record of
//     docs/data/supersessions.json, <product>:<operationId>, names an
//     operation of that product's reference spec;
//   - every enumValues entry of a superseded record, Type.VALUE, names a
//     value of that enum in docs/data/graphql-inventory.json, and every
//     fields entry, Type.field, a field or input field of that type;
//   - every report that marks a claim *(unverified)* is listed by
//     a record of docs/data/open-questions.json, the register of open
//     questions, which in turn makes the report cite that record's id;
//   - every numbered architecture option has the same outline as the first,
//     so the comparison in docs/research/architecture-options/README.md
//     compares like with like;
//   - every phase of docs/implementation/plan.md states its decisions,
//     dependencies, tasks and exit criteria, and every phase the features,
//     capabilities and supersessions registers name is a phase of the plan;
//   - GLOSSARY.md, infraweave's own language, defines each term once, with
//     a definition and the words to avoid;
//   - every register under docs/data matches its schema in docs/data/schema,
//     its record ids are unique, its links resolve, every id a document or
//     register cites exists, and the files and phrases its records name
//     agree with it (registers.go);
//   - every Go package under internal/, pkg/, test/acceptance/ and
//     test/deps/ has a doc.go whose package comment says what the package
//     is and does, then has "# Input and output" and "# Dependencies"
//     sections (packages.go);
//   - every citation of a decision, a report or a register record in a Go
//     comment, a migration, a YAML file, the Makefile or a scenario sits on
//     a ref: line of its own, so the comment around it stands on its own
//     (refs.go).
//
// # Input and output
//
// Check reads the repository through the fs.FS it is given: every markdown
// file under docs/, GLOSSARY.md, the registers and schemas under docs/data,
// each Spacelift product's reference spec, the source and configuration
// files it scans for docs/ paths and citations, and the doc.go of every Go
// package under internal/, pkg/, test/acceptance/ and test/deps/. It writes nothing and touches no
// network; it returns the findings, sorted by path and line.
//
// # Dependencies
//
// It imports jsonschema/v6 to validate the registers, yaml.v3 to read the
// reference specs' tags, and internal/devtools/specref for the list of
// products and where each one's spec lives. make docs-check runs its
// TestUnitDocs over the repository root; no command imports it.
package docscheck
