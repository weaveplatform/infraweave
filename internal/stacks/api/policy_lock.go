package api

// PolicyLockPrefix separates a policy's caller key from every human/API actor.
// A policy key can only release or reuse a lock in this namespace.
const PolicyLockPrefix = "policy:"
