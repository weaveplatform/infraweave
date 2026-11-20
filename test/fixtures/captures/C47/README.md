# C47: ACCESS creation availability, 2026-11-19

Observed: creating an ACCESS policy in root fails with `cannot create stack
access policies outside of legacy space`. The account has no legacy space.
No ACCESS policy was created, attached or evaluated. This does not establish
post-disable-date evaluation on an account that retains legacy policies.

The intentional infraweave supersession remains in force. The legacy upstream
evaluation question stays open; no deprecated API is reintroduced from this read.

The final exchange verifies restoration and resource cleanup. Eight provider-free
runs were created; none applied infrastructure. REST replay awaits #659.
