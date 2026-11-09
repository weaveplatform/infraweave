// Package expr compiles, type-checks and evaluates the expressions users
// write: CEL, in its two forms. An expression is a whole value that keeps
// its CEL type; a text template embeds expressions as ${{ … }} and renders
// them into a string, $${{ being a literal ${{, and a template that is
// exactly one ${{ … }} is an expression.
//
// Each domain that has a place for expressions declares it as a Surface:
// its variables, each typed by a JSON Schema, and Env, which authorizes a
// read of one context and describes it. A Registry holds every surface. It
// turns a schema into CEL types, so an object with properties is an object
// type whose misspelt field is an error at check time; it builds one
// cel-go environment per set of declared types, and caches each compiled
// program by a hash of its surface, declared types, form and source.
//
// The library is the same on every surface: CEL's standard library and
// optional values, cel-go's strings, lists, regex, maths, bindings and
// two-variable comprehension extensions as the module ships them, and
// infraweave's base64.*, json.*, url.*, merge, pick, omit, random.* and
// fail. Functions lists it from the environment itself.
//
// An evaluation is deterministic: now and a seed come with its Input,
// random.* draws from the seed, every map is iterated in key order, and no
// function reads the clock, the network or a file. It runs under
// expressions.cost_limit and expressions.timeout, its source under
// expressions.max_source and its result under expressions.max_result;
// going over one fails as fail does, naming the setting. A secret variable
// is read only with a literal key, so an evaluation loads only the secrets
// its source names, and a result that read one is masked by platform/mask.
//
// ref: decision 0024
//
// # Input and output
//
// Check takes a Request (surface, the context's variables, form and
// source) and returns diagnostics with ranges in the source, the result's
// type and a static cost estimate; Diagnostic.FieldError gives each as the
// API's error item. Evaluate takes a Request and an Input (now, the seed,
// the variables' values, and a loader of secret keys) and returns the
// Result as Go values, or *CheckError for a source that does not compile
// and *Failed for an evaluation that stops. Nothing is logged or stored;
// the caches live in memory and are rebuilt on a miss.
//
// # Dependencies
//
// cel-go (cel.dev/cel-go), internal/kernel for principals and error items,
// and internal/platform/mask. It imports no domain, so the agent may
// import it, as the runner does for workflow command templates.
package expr
