Public, importable packages. Anything another repository may import lives
here; everything else goes in `internal/`, which nothing here imports.

| Package | What |
|---|---|
| `client/api` | The client oapi-codegen generates from `api/api.openapi.yaml`; `make verify-generate` holds it to the bundle |
| `sdk` | The supported entry point: the generated client with the API's conventions (token, `Idempotency-Key`, retries, cursor iterators, typed problem errors) |
