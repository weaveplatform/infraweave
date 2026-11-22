package api

// TaskDeprecatedSince is infraweave's TASK evaluation introduction date,
// 2026-11-21 UTC. The type is deprecated from introduction. The upstream
// documentation supplies no start date, so this is not an upstream timestamp.
const TaskDeprecatedSince int64 = 1795219200

// TaskDeprecationInfo describes the notice's scope and approval migration.
const TaskDeprecationInfo = "https://github.com/weaveplatform/infraweave/blob/main/docs/implementation/task-policy-verification.md#deprecation-notice"

// InitializationDeprecatedSince is this server gate's infraweave adoption date,
// 2026-11-21 UTC, rather than an asserted upstream deprecation date.
const InitializationDeprecatedSince = TaskDeprecatedSince

// InitializationDeprecationInfo scopes the server notice separately from P14.
const InitializationDeprecationInfo = "https://github.com/weaveplatform/infraweave/blob/main/docs/implementation/initialization-policy-verification.md#deprecation-notice"
