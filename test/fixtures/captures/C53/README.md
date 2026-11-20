# C53: capture/sampling separation and sensitive PLAN values

Observed on 2026-11-19/20: sample=false still produced an account-level PLAN input
capture while policy sampleCount was zero and receipts said sampled=false.
After sample=true, actual policy samples appeared (one PLAN, two APPROVAL) and
receipts said sampled=true; the latest PLAN input capture remained the earlier
sample=false document. A later resource-containing plan also left that latest
input-capture ID/timestamp unchanged. This bounded sequence does not establish a
replacement interval or deduplication algorithm. Lists return multiple historical
records, not just a single replaceable document.

A non-admin key with actual canRead/canWrite stack access received `unauthorized`
for root's policyInputCaptures. Root could read both sampled policy input and
account-level input captures. Direct non-admin evaluationSample access and
cross-space isolation remain unobserved.

The resource-containing real PLAN sample masks terraform_data.sensitive's input
as `1b761c86`, the final eight SHA-256 hex characters of the fixture's dummy secret.
No plaintext copy of that dummy value occurs anywhere in that input. The run's
commit hash is 6699d52b96b83cb8785243d406f085e0515ca423, which contains the resource
fixture. The account-level latest capture is older and is not presented as the
sensitive-resource sample. Binary Terraform plans were never read or published.

An old PLAN capture remains readable about 2.8 days after capture; the schema has
no expiration field. This establishes only the measured lower bound, not a TTL.
The first exchange preserves the earlier C53 observation used by #641 at immutable
commit 8b964824a8430199dd0a6e33ebeb7be5fb54f50f. Cleanup removes the lab stack,
policies, key and source branch; existing service-owned input capture history has
no exposed deletion operation and remains service-managed.

The final exchange verifies restoration and resource cleanup. Eight provider-free
runs were created; none applied infrastructure. REST replay awaits #662.
