# C273: actual policy-page requests and scheme/session effects

Resolved page-operation question: the real browser opened the PLAN body, Used by,
Samples, sample-details dialog and simulation panel. Used by showed the one lab
stack; Samples listed two ALLOW records newest first. Clicking Simulate issued
SimulatePolicyV2. The browser opened NOTIFICATION's body and observed Samples 0,
no Used by tab, and an explicit evaluation-entitlement warning. These are actual
UI-triggered requests, not manually constructed substitutes.

Operations captured: SearchPolicies, SearchPoliciesSuggestions, GetPolicyNames,
GetPolicyForDetails (body, engine, metadata, counts and attachments),
GetPolicyEvaluationRecords, GetPolicyEvaluationSample, SearchEvaluationRecords,
SearchEvaluationRecordSuggestions and SimulatePolicyV2. The simulation's returned
string is preserved as a JSON string; stateless output is not a run execution.

Separately, a LOGIN body update under MANAGED_USERS left an existing non-admin
session valid. Switching to LOGIN_POLICIES invalidated it while retaining the
owner caller session. Updating the LOGIN body while LOGIN_POLICIES was active
again invalidated the test session (viewer null). Switching back restored the
original scheme, preserved the active owner and left no queued invitations or
other sessions. The deleted key subsequently failed both admission and session
read. C50 retains the separate unresolved policy-evaluation/flag question.

The three ui-*.txt files contain the dated accessibility snapshots for Used by,
sample details and NOTIFICATION's missing attachment tab, with account/identity
redaction. Browser network headers, analytics and unrelated account responses
are omitted. C54 reuses #637's complete 62-body catalogue/library comparison.

The final exchange verifies restoration and resource cleanup. Eight provider-free
runs were created; none applied infrastructure. REST replay awaits #665.
