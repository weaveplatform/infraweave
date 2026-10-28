# Glossary

Terms used across this research set, in the sense Spacelift uses them. Where
infraweave uses a term differently, the infraweave sense follows. A term's
presence here does not mean infraweave implements it.

| Term | Meaning |
|---|---|
| Field route | A REST route for a field of an object type rather than a root field, such as a stack's runs. The reference has 103 ([02](02-graphql-to-rest-mapping.md)) |
| Override | A hand-curated route or exclusion in `api/reference/spacelift/rest-mapping.yaml`, for a root field no rule can map |
| Parity oracle | A pinned upstream artifact that infraweave's surface is measured against: the specification reference for the API, and `terraform-schema.json` for the provider |
| Root field | A field of the GraphQL `Query` or `Mutation` type. Spacelift has 665 ([01](01-spacelift-landscape.md)) |
| RPC-shaped | A mutation served as `POST /<operation>` because no rule or override gives it a resource shape. The reference allows none |
| Served API | infraweave's own API, `api/deploy.openapi.yaml`, built from `api/src/deploy/` fragments. It starts from the reference and records each divergence |
| Specification reference | `api/reference/spacelift/spacelift.openapi.yaml`, the REST projection of Spacelift's GraphQL API. A reference for the served API, not that API |
