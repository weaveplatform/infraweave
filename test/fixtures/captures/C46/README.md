# C46: real-webhook attempt blocked by an account entitlement

Unresolved: the same dedicated GitHub branch delivered two real push events.
With ignore=true GIT_PUSH attached, and again with no custom push policy attached,
a tracked run was created. Both ignored-trigger queries returned the real error
`Your billing tier does not support listing ignored runs.` Their commit SHAs
identify the two webhook deliveries; the latter query records the detached policy.

No CUSTOM_POLICY, CUSTOM_POLICY_NO_TRIGGER or DISABLED_BY_LABEL reason was observed.
An account entitled to ignored-trigger history and workflow push-policy evaluation
is needed to finish these cases, including discovery/verification of the label.
No label was guessed, no paid plan was purchased and no support message was sent.
The temporary source branch was deleted after the test; the CLI verified404.

The final exchange verifies cleanup. REST replay remains deferred to #652.
