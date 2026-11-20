# C56: policy timestamp units

Resolved: `createdAt` and `updatedAt` are Unix seconds. The real create and update
are bounded by UTC request timestamps; creation remains unchanged on update.
The unit test checks these bounds, rather than inferring units from digit counts.

The final exchange verifies cleanup. REST replay remains deferred to #645.
