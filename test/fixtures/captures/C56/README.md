# C56: policy timestamp units

Resolved: `createdAt` and `updatedAt` are Unix seconds. The real create and update
are bounded by UTC request timestamps; creation remains unchanged on update.
The unit test checks these bounds, rather than inferring units from digit counts.

The final exchange verifies upstream cleanup. `TestAccCaptures/C56` now creates,
updates, reads and deletes a real infraweave policy. Its stateful REST replayer
checks Unix-second request bounds, preserved creation time, an advancing update
timestamp, persistence and cleanup. The local request adapts the module namespace
and owning root; the checked-in reference exchange remains unchanged.
