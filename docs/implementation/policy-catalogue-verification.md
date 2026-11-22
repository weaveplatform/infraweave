# Pinned policy catalogue verification (#664)

The catalogue is imported from the MIT-licensed Spacelift example library at
`8e8894fbd4cefffa3c261746bcdd70ef9fbb53a8`. Its original bytes, full MIT license,
attribution and per-file SHA-256 inventory are under
`internal/policies/internal/catalogue/upstream`. This includes 68 YAML templates,
74 Rego programs and 62 upstream test files. No upstream file is overwritten by
namespace adaptation. Six LOGIN templates and six non-template legacy ACCESS
programs remain compatibility-only; the served catalogue contains 62 templates.

## Source and capture comparison

Every one of C54/library.json's 68 original body hashes matches the pinned
source. All 62 captured public bodies and metadata derive from that source.
C54's opaque IDs/slugs are preserved in CATALOGUE.json. Names, descriptions and
label values are preserved; label comparisons use set semantics because the
live capture sorts some source lists differently. MIT NOTICE includes the full
original license, whose SHA-256 is
`6197d457d51544c73079bdc058aa9314d4ecf00079ac3f531bd2052e069a7e9f`.

Namespace adaptation parses OPA's AST. It rewrites package, input and data
namespace references, and root namespace keys in `with input` documents,
including documents assigned to a local variable first. Imported data keeps its
local alias. Arbitrary string values and unrelated object keys stay unchanged.
The verbose corpus test reports the file and number of namespace rewrites.

Three assertions in the pinned Discord/Teams notification tests contain unsafe
negated wildcards (`not webhook[_]`). Running the original, unmodified source
and test modules proves OPA rejects these before namespace adaptation. A bounded
test-only AST adapter changes the three assertions to `count(webhook) == 0`,
preserving their empty-result meaning and `with input` scope. The original files
remain unchanged, and the adapter asserts the exact one/two-expression inventory.
This is a documented upstream-test compatibility correction, not vendor behavior.

## Runtime and API behavior

Every one of the 74 original programs compiles through the production OPA runtime
in its supported dialect, including all six LOGIN and six legacy ACCESS
programs. TestUnitPolicyLibrary runs every upstream test file: 62 files,
241 individual Rego tests, with no skipped file/case. The namespace string and
import-alias controls, complete captured metadata/body comparison and MIT checks
also pass. The measured catalogue package coverage is 92.4% before final CI.

The three API operations require policies plus SPACE_READ in any account space.
GET returns the original metadata with the adapted body. An additive `engineType`
field explicitly supplies V0 for older examples rather than changing creation's
intentional V1 default. Policy creation validates the selected supported origin
and matching policy type, and retains that immutable origin in template_origin.

Search and suggestions use the existing typed PostgreSQL search engine over a
bound JSONB recordset of public metadata. This introduces no persisted table or
writes. The account UUID is only a virtual scope adapter for the shared engine;
it grants no root-space rights. Queries include no body, source path or rewrite
report. Cursors include the pinned revision; names/slugs plus the unique ID form
a total order. Fields name/slug/description/label/type are infraweave search
choices: C54 proves membership and bodies, not a vendor suggestion-field schema.
LOGIN and other valid-but-unserved policy types return empty filtered results.

## Real service proof

- Real PostgreSQL tests page all 62 stable IDs, check the exact five served type
  counts, label/text filters, matching facets, invalid cursors/fields and empty
  LOGIN/TASK/INITIALIZATION results.
- Public HTTP: 9 scenarios and 42 passing steps through two actual servers and
  PostgreSQL cover all three operations' success/403/failure, full captured
  membership, typed filters and unknown origin/handle validation.
- Five public import → create → simulate journeys execute APPROVAL, PLAN,
  NOTIFICATION, GIT_PUSH and TRIGGER with real OPA. They verify persisted source,
  dialect/type, origin metadata and concrete decision results, including an old
  V0 notification example and the renamed PLAN input namespace.
- Focused runtime/storage proof uses race detection, shuffled order and
  `GOFIPS140=certified GODEBUG=fips140=only`.

Configured lint, generated/reference/parity checks, final coverage and the full
certified-FIPS repository acceptance run remain merge gates. #663 is the direct
simulation dependency; this branch does not claim that ticket's pending merge.

## Explicit source refreshes

A refresh pins a specific source commit and preserves its license/notice. Review
MANIFEST, all template/test inventories, served and excluded memberships, source
body/metadata differences, stable identity mapping and namespace reports. Run
all upstream tests and every production compiler/import/simulation proof again.
Changing the source or required count silently, dropping failing tests, or
marking legacy ACCESS as served is not a refresh.
