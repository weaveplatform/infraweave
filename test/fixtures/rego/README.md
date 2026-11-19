# Real Rego simulation evidence

`preamble-simulations.json` holds seven real, stateless Spacelift
`policySimulatev2` exchanges captured on 2026-11-19 during the #644 preflight.
Their fixed test bodies and `{}` inputs contain no account identity or tokens.
They establish raw decisions for PLAN, APPROVAL, INITIALIZATION, TASK,
NOTIFICATION, ACCESS and LOGIN; they do not establish login/key/scheme side
effects or tier eligibility.

`login-role-forms.json` holds four further real, stateless LOGIN simulations
from the same date. Partial array, set and boolean-map role forms succeed;
complete `roles := {...}` reassignment fails compilation. Raw LOGIN decisions
retain `roles.nothing: {}` even when no real role is granted.

The empty TRIGGER decision has its nonsecret request and response in
[the policy contract provenance](../policy-inputs/provenance/trigger-defaults.json).

`TestUnitRegoCapturedDecisions` runs the recorded bodies through the linked OPA
library after the package namespace rewrite and compares the complete raw
result. The version of Spacelift's OPA is a dated observation; this runtime
links the current pinned library instead.
