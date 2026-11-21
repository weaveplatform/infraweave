# C291: rejection in both post-plan review states

Resolved: a tracked run in UNCONFIRMED and a warned proposed run in PENDING_REVIEW
both transition to FAILED after a REJECT review. Both histories record exactly
`Run rejected by approval policies`. Each final evaluation has one rejecting
approval policy and a different approving policy. No infrastructure was applied.

The final exchange verifies cleanup. `TestAccCaptures/C291` uses the
`policy-approval-rejections` replayer with the recorded policy sources and
dialects. It drives actual Terraform workers to both review states, sends
the captured rejection notes with each local state's current version, and
checks the exact failure note and latest mixed approval outcomes. It then
deletes the stack and policies through REST, verifies 404s and restores the
initial inventories. Only the module namespace, local identities and root
space are translated; no worker report or run state is injected.

The reusable feature session retains its own Cucumber report and requires
both expected scenarios to complete, including cleanup. Inherited ordinary
partition filters cannot select zero scenarios and count as a replay.
