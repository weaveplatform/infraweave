# C48: accessible free-tier observations

Partial capture: V5_FREE advertises no WORKFLOW_POLICIES, ACCESS_POLICIES,
NOTIFICATION_POLICIES or RUN_IGNORED_TRIGGERS. It still allows creating PLAN,
APPROVAL, GIT_PUSH, TRIGGER, INITIALIZATION, TASK, NOTIFICATION and INTENT policies.
All except NOTIFICATION could be attached to the test stack; NOTIFICATION returns
a type-specific attachment error. Real receipts establish PLAN, APPROVAL and
INITIALIZATION evaluation on this account. Creation or attachment does not prove
that a policy type is evaluated or gated by a particular feature.

A real GitHub push with an attached ignore=true GIT_PUSH policy nevertheless
created a tracked run. No GIT_PUSH receipt/sample was observed. An entitled-account
comparison is still required before assigning WORKFLOW_POLICIES to policy types;
this fixture does not turn that inference into an observed contract. LOGIN,
ACCESS, STACK_ACCESS and legacy aliases were not mutated in this bounded lab.

The final exchange verifies cleanup. REST replay remains deferred to #645.

The read-only policies page also displayed the warning in console.json that Access, Push, Trigger and Notification policies will not be evaluated on this account. This narrows the affected types but does not isolate the mapping between each absent billing feature and each type.
