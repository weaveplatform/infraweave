// Package features is decision 0021's feature catalogue, compiled in:
// catalogue.gen.go, which make generate writes from docs/data/features.json
// and the tier-feature records of docs/data/capabilities.json, holds every
// Feature, and make verify-generate fails when it is stale. Catalogue and
// Lookup read it; Seed is a seeded tier's content, every gated feature from
// its lowest seeded tier up and each limit's seeded value, with Enterprise
// holding every gated feature and no limit; Gated is every capability and
// option; Closed drops the features whose requirements an entitlement
// lacks. Billing and Operator are the names the installation holds, which
// features.disabled rejects.
//
// # Input and output
//
// None; the catalogue is compiled in.
//
// # Dependencies
//
// It imports the standard library only. The features domain's packages and
// internal/app, which checks the served documents against it, import it.
package features
