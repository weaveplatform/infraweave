# C50: bounded LOGIN flag experiment, 2026-11-19

Unresolved effect: a compiled LOGIN policy emits a flag in stateless simulation.
The live experiment used one ordinary, non-admin, non-personal root writer key,
with the active scheme explicitly switched to LOGIN_POLICIES. Its flag=true and
flag=false policy bodies both allowed members and the exact test key, and assigned
that key only root's space-writer role. Both admissions issued non-admin tokens,
but stack reads were denied and no LOGIN evaluation records or input captures
appeared. A further boolean deny for that same key still issued a token. These
controls do not establish that LOGIN rules were evaluated on this V5_FREE account.
They cannot prove that flags have no effect or that role grants were honored.

Actual session invalidation and scheme restoration are captured in C273. No run
was created by these login experiments. JWT claim *names* alone were recorded;
tokens and the key secret were redacted before export. The key was deleted,
fresh admission then returned null, and old-session viewer returned null.

An entitled LOGIN evaluation/receipt is still needed for C50. ACCESS remains
superseded; NOTIFICATION and INTENT no-run effects belong to notification and Intent execution.

The final exchange verifies restoration and resource cleanup. Eight provider-free
runs were created; none applied infrastructure. The executable native replay is described below.

## Native verification for #666

Native REST replay now executes the captured flag=true, flag=false and deny programs with namespace, native secret-key login and root-space identities mapped to the local fixture. Real admission/grants, scheme/session invalidation, denied-entry session/grant stability, no run effects and key/policy cleanup pass. Historical V5_FREE evaluation/flag observations remain unresolved as described above. The exact observed/REST/upstream classification is recorded in [expect.yaml](expect.yaml); original responses and browser snapshots are unchanged.
