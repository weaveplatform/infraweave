# C52: server initialization denial and TASK controls, 2026-11-19

Resolved initialization question: a sampled REGO_V1 server-side INITIALIZATION
deny leaves a real tracked run FAILED. Its history is QUEUED, READY, FAILED and
the failure note is exactly `job assignment failed: denied by 1 initialization
policy`. The receipt identifies INITIALIZATION, deny and sampled=true. No worker
INITIALIZING phase occurred; this is not a worker-side initialization policy.

TASK denial's no-run outcome remains unobserved. With an attached unconditional
TASK deny, four task runs reached FINISHED: owner with initialization skipped,
owner with initialization enabled, non-admin writer with initialization skipped,
and the same writer after changing only the TASK body/engine to classic REGO_V0.
No TASK receipt or evaluation sample was observed. Actual TASK input captures do
exist, including the last key-created task; input-capture existence therefore
does not prove policy evaluation. This account cannot establish the intended
deny-before-run-creation behavior for #659. The four histories and both policy
versions are retained, rather than rewriting a successful task into a denial.

The final exchange verifies restoration and resource cleanup. Eight provider-free
runs were created; none applied infrastructure. REST replay awaits #660.
