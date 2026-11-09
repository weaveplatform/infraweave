package expr

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common"
	"cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/cost"
	"cel.dev/cel-go/common/operators"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
	"cel.dev/cel-go/ext"
)

// The functions written for infraweave. random.* is bound per evaluation,
// from its seed.
//
// ref: decision 0024 "Multi-step logic"
const (
	fnB64Encode  = "base64.encode"
	fnB64Decode  = "base64.decode"
	fnMerge      = "merge"
	fnPick       = "pick"
	fnOmit       = "omit"
	fnURLEncode  = "url.encode"
	fnURLDecode  = "url.decode"
	fnURLQuery   = "url.query"
	fnJSONEncode = "json.encode"
	fnJSONDecode = "json.decode"
	fnUUID       = "random.uuid"
	fnInt        = "random.int"
	fnString     = "random.string"
	fnFail       = "fail"

	// fnSorted orders a comprehension's map range by key. The engine wraps
	// every comprehension's range in it; no expression can name it.
	fnSorted = "@infraweave.sorted"
)

// library is every function and macro an expression may use: CEL's
// standard library, optional values, cel-go's extensions as the module
// ships them, and infraweave's functions. cel-go's encoders extension is
// left out: it prices its JSON and YAML functions as unbounded, so under
// a cost limit every call fails, and its json.encode keeps a map's random
// order. base64.* and json.* are infraweave's instead.
func library(maxResult int, a types.Adapter) []cel.EnvOption {
	anyMap := cel.MapType(cel.DynType, cel.DynType)
	keys := cel.ListType(cel.StringType)
	mapOp := func(f func(traits.Mapper, ref.Val) ref.Val) func(ref.Val, ref.Val) ref.Val {
		return func(m, arg ref.Val) ref.Val {
			mapper, ok := m.(traits.Mapper)
			if !ok {
				return types.NewErr("expected a map, got %s", m.Type().TypeName())
			}
			return f(mapper, arg)
		}
	}
	return []cel.EnvOption{
		cel.OptionalTypes(),
		cel.DefaultUTCTimeZone(true),
		cel.CrossTypeNumericComparisons(true),
		ext.Strings(),
		ext.Lists(),
		ext.Regex(),
		ext.Math(),
		ext.Bindings(),
		ext.TwoVarComprehensions(),
		cel.CostModel(
			scanCost("infraweave_base64_encode_bytes"),
			scanCost("infraweave_base64_decode_string"),
			scanCost("infraweave_url_encode_string"),
			scanCost("infraweave_url_decode_string"),
			scanCost("infraweave_json_decode_string"),
			entriesCost("infraweave_json_encode_dyn", 0),
			entriesCost("infraweave_url_query_map", 0),
			entriesCost("infraweave_merge_map_map", 0, 1),
			entriesCost("infraweave_pick_map_list", 0, 1),
			entriesCost("infraweave_omit_map_list", 0, 1),
			cost.Overload("infraweave_random_string_int",
				cost.EvalCost(cost.Sum(cost.Const(cost.CallCost), cost.IntArg(0, 0)))),
		),
		cel.Function(
			fnB64Encode,
			cel.FunctionDocs("Encodes bytes as standard, padded base64."),
			cel.Overload(
				"infraweave_base64_encode_bytes",
				[]*cel.Type{cel.BytesType},
				cel.StringType,
				cel.OverloadExamples(`base64.encode(b"hello") // "aGVsbG8="`),
				cel.UnaryBinding(func(b ref.Val) ref.Val {
					return types.String(base64.StdEncoding.EncodeToString(b.(types.Bytes)))
				}),
			),
		),
		cel.Function(
			fnB64Decode,
			cel.FunctionDocs("Decodes standard base64, padded or not; anything else is an error."),
			cel.Overload(
				"infraweave_base64_decode_string",
				[]*cel.Type{cel.StringType},
				cel.BytesType,
				cel.OverloadExamples(`base64.decode("aGVsbG8") // b"hello"`),
				cel.UnaryBinding(func(s ref.Val) ref.Val {
					str := string(s.(types.String))
					b, err := base64.StdEncoding.DecodeString(str)
					if err != nil {
						if b, err = base64.RawStdEncoding.DecodeString(str); err != nil {
							return types.NewErr("base64.decode: %v", err)
						}
					}
					return types.Bytes(b)
				}),
			),
		),
		cel.Function(fnMerge,
			cel.FunctionDocs("Returns a map holding the entries of both maps; where both "+
				"hold a key, the second map's value is kept."),
			cel.Overload("infraweave_merge_map_map", []*cel.Type{cel.DynType, cel.DynType}, anyMap,
				cel.OverloadExamples(`merge({"a": 1, "b": 2}, {"b": 3}) // {"a": 1, "b": 3}`),
				cel.BinaryBinding(mapOp(func(m traits.Mapper, other ref.Val) ref.Val {
					o, ok := other.(traits.Mapper)
					if !ok {
						return types.NewErr("expected a map, got %s", other.Type().TypeName())
					}
					return entries(a, m, o, func(ref.Val) bool { return true })
				})))),
		cel.Function(fnPick,
			cel.FunctionDocs("Returns the entries of a map whose keys are listed."),
			cel.Overload("infraweave_pick_map_list", []*cel.Type{cel.DynType, keys}, anyMap,
				cel.OverloadExamples(`pick({"a": 1, "b": 2}, ["a"]) // {"a": 1}`),
				cel.BinaryBinding(mapOp(func(m traits.Mapper, list ref.Val) ref.Val {
					listed := list.(traits.Lister)
					return entries(
						a,
						m,
						nil,
						func(k ref.Val) bool { return listed.Contains(k) == types.True },
					)
				})))),
		cel.Function(fnOmit,
			cel.FunctionDocs("Returns the entries of a map whose keys are not listed."),
			cel.Overload("infraweave_omit_map_list", []*cel.Type{cel.DynType, keys}, anyMap,
				cel.OverloadExamples(`omit({"a": 1, "b": 2}, ["a"]) // {"b": 2}`),
				cel.BinaryBinding(mapOp(func(m traits.Mapper, list ref.Val) ref.Val {
					listed := list.(traits.Lister)
					return entries(
						a,
						m,
						nil,
						func(k ref.Val) bool { return listed.Contains(k) != types.True },
					)
				})))),
		cel.Function(
			fnURLEncode,
			cel.FunctionDocs("Escapes a string to sit in a URL query."),
			cel.Overload(
				"infraweave_url_encode_string",
				[]*cel.Type{cel.StringType},
				cel.StringType,
				cel.OverloadExamples(`url.encode("a b&c") // "a+b%26c"`),
				cel.UnaryBinding(func(s ref.Val) ref.Val {
					return types.String(url.QueryEscape(string(s.(types.String))))
				}),
			),
		),
		cel.Function(
			fnURLDecode,
			cel.FunctionDocs("Reverses url.encode; malformed escapes are an error."),
			cel.Overload(
				"infraweave_url_decode_string",
				[]*cel.Type{cel.StringType},
				cel.StringType,
				cel.OverloadExamples(`url.decode("a+b%26c") // "a b&c"`),
				cel.UnaryBinding(func(s ref.Val) ref.Val {
					out, err := url.QueryUnescape(string(s.(types.String)))
					if err != nil {
						return types.NewErr("url.decode: %v", err)
					}
					return types.String(out)
				}),
			),
		),
		cel.Function(fnURLQuery,
			cel.FunctionDocs("Encodes a map as a URL query, keys sorted; a list value "+
				"repeats its key, and other values are their text."),
			cel.Overload(
				"infraweave_url_query_map",
				[]*cel.Type{anyMap},
				cel.StringType,
				cel.OverloadExamples(
					`url.query({"q": "a b", "tag": ["x", "y"]}) // "q=a+b&tag=x&tag=y"`,
				),
				cel.UnaryBinding(func(m ref.Val) ref.Val { return query(m.(traits.Mapper)) }),
			)),
		cel.Function(fnJSONEncode,
			cel.FunctionDocs("Encodes a value as compact JSON, map keys sorted."),
			cel.Overload("infraweave_json_encode_dyn", []*cel.Type{cel.DynType}, cel.StringType,
				cel.OverloadExamples(`json.encode({"b": 1, "a": [true]}) // '{"a":[true],"b":1}'`),
				cel.UnaryBinding(func(v ref.Val) ref.Val {
					var b strings.Builder
					if err := encodeJSON(&b, v); err != nil {
						return types.NewErr("json.encode: %v", err)
					}
					return types.String(b.String())
				}))),
		cel.Function(fnJSONDecode,
			cel.FunctionDocs("Decodes JSON text into a value; malformed JSON is an error. "+
				"Numbers decode as doubles."),
			cel.Overload("infraweave_json_decode_string", []*cel.Type{cel.StringType}, cel.DynType,
				cel.OverloadExamples(`json.decode('{"a": [1]}').a[0] // 1.0`),
				cel.UnaryBinding(func(s ref.Val) ref.Val {
					var v any
					if err := json.Unmarshal([]byte(s.(types.String)), &v); err != nil {
						return types.NewErr("json.decode: %v", err)
					}
					return a.NativeToValue(v)
				}))),
		cel.Function(fnUUID,
			cel.FunctionDocs("Returns a version 4 UUID drawn from the evaluation's seed."),
			cel.Overload("infraweave_random_uuid", nil, cel.StringType,
				cel.OverloadExamples(`random.uuid() // "1b4e28ba-2fa1-4d3b-a3f5-ef19b5a7633b"`),
				cel.LateFunctionBinding())),
		cel.Function(
			fnInt,
			cel.FunctionDocs("Returns an integer from min to max, both included, drawn from "+
				"the evaluation's seed."),
			cel.Overload(
				"infraweave_random_int_int_int",
				[]*cel.Type{cel.IntType, cel.IntType},
				cel.IntType,
				cel.OverloadExamples(`random.int(1, 6) // 4`),
				cel.LateFunctionBinding(),
			),
		),
		cel.Function(fnString,
			cel.FunctionDocs("Returns a string of ASCII letters and digits of the given "+
				"length, drawn from the evaluation's seed."),
			cel.Overload("infraweave_random_string_int", []*cel.Type{cel.IntType}, cel.StringType,
				cel.OverloadExamples(`random.string(8) // "q3ZfK0aB"`),
				cel.LateFunctionBinding())),
		cel.Function(fnFail,
			cel.FunctionDocs("Stops the evaluation with the message."),
			cel.Overload(
				"infraweave_fail_string",
				[]*cel.Type{cel.StringType},
				cel.DynType,
				cel.OverloadExamples(
					`size(inputs.name) > 0 ? inputs.name : fail("a name is required")`,
				),
				cel.UnaryBinding(func(msg ref.Val) ref.Val {
					return types.WrapErr(&Failed{Message: string(msg.(types.String))})
				}),
			)),
		cel.Function(
			fnSorted,
			cel.Overload(
				"infraweave_sorted",
				[]*cel.Type{cel.TypeParamType("T")},
				cel.TypeParamType("T"),
				cel.UnaryBinding(func(v ref.Val) ref.Val {
					if m, ok := v.(traits.Mapper); ok {
						return sorted(m)
					}
					return v
				}),
			),
		),
	}
}

// scanCost prices a function that reads its string or bytes argument once.
func scanCost(id string) cost.OverloadModel {
	return cost.Overload(id, cost.EvalCost(
		cost.Sum(cost.Const(cost.CallCost), cost.StringScan(cost.Length(cost.Arg(0))))))
}

// entriesCost prices a function that visits each entry of the arguments
// at args once.
func entriesCost(id string, args ...int) cost.OverloadModel {
	terms := make([]cost.QuantityExpr, 0, 1+len(args))
	terms = append(terms, cost.Const(cost.CallCost))
	for _, i := range args {
		terms = append(terms, cost.Length(cost.Arg(i)))
	}
	return cost.Overload(id, cost.EvalCost(cost.Sum(terms...)))
}

// entries copies the entries of m that keep accepts, then every entry of
// other, into a new map ordered by key.
func entries(a types.Adapter, m, other traits.Mapper, keep func(ref.Val) bool) ref.Val {
	out := map[ref.Val]ref.Val{}
	for it := m.Iterator(); it.HasNext() == types.True; {
		if k := it.Next(); keep(k) {
			out[k] = m.Get(k)
		}
	}
	if other != nil {
		for it := other.Iterator(); it.HasNext() == types.True; {
			k := it.Next()
			out[k] = other.Get(k)
		}
	}
	return sorted(types.NewRefValMap(a, out))
}

// query encodes m as a URL query.
func query(m traits.Mapper) ref.Val {
	q := url.Values{}
	add := func(k string, v ref.Val) error {
		s, err := text(v)
		q.Add(k, s)
		return err
	}
	for it := m.Iterator(); it.HasNext() == types.True; {
		k := it.Next()
		key, err := text(k)
		if err != nil {
			return types.NewErr("url.query: %v", err)
		}
		v := m.Get(k)
		if list, ok := v.(traits.Lister); ok {
			for e := list.Iterator(); e.HasNext() == types.True; {
				if err := add(key, e.Next()); err != nil {
					return types.NewErr("url.query: %v", err)
				}
			}
			continue
		}
		if err := add(key, v); err != nil {
			return types.NewErr("url.query: %v", err)
		}
	}
	return types.String(q.Encode())
}

// alphabet is what random.string draws from.
const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// randomFunctions binds random.* to a generator seeded with seed, so an
// evaluation with the same seed draws the same values in the same order.
// maxResult bounds random.string, which would otherwise allocate before
// the result's size is checked.
func randomFunctions(seed uint64, maxResult int) map[string]cel.LateBoundFunction {
	// PCG is not for secrets; it is what makes a replay draw the same
	// values, which a cryptographic source cannot.
	r := rand.New(rand.NewPCG(seed, seed)) //nolint:gosec // reproducible by design
	return map[string]cel.LateBoundFunction{
		fnUUID: func(string, ...ref.Val) ref.Val {
			var b [16]byte
			binary.LittleEndian.PutUint64(b[:8], r.Uint64())
			binary.LittleEndian.PutUint64(b[8:], r.Uint64())
			b[6] = b[6]&0x0f | 0x40
			b[8] = b[8]&0x3f | 0x80
			return types.String(
				fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]),
			)
		},
		fnInt: func(_ string, args ...ref.Val) ref.Val {
			lo, hi := int64(args[0].(types.Int)), int64(args[1].(types.Int))
			if lo > hi {
				return types.NewErr("random.int: min %d is greater than max %d", lo, hi)
			}
			// hi - lo wraps negative when the range is wider than int64.
			if span := hi - lo; span >= 0 && span < math.MaxInt64 {
				return types.Int(lo + r.Int64N(span+1))
			}
			// Such a range holds more than half of all int64s, so a uniform
			// draw over all of them lands in it within a few tries.
			for {
				n := r.Int64()
				if r.IntN(2) == 0 {
					n = -n - 1
				}
				if n >= lo && n <= hi {
					return types.Int(n)
				}
			}
		},
		fnString: func(_ string, args ...ref.Val) ref.Val {
			n := int64(args[0].(types.Int))
			if n < 0 {
				return types.NewErr("random.string: length %d is negative", n)
			}
			if n > int64(maxResult) {
				return types.WrapErr(&Failed{
					Message: fmt.Sprintf(
						"random.string(%d) is longer than expressions.max_result (%d).",
						n,
						maxResult,
					),
					Limit: "expressions.max_result",
				})
			}
			b := make([]byte, n)
			for i := range b {
				b[i] = alphabet[r.IntN(len(alphabet))]
			}
			return types.String(b)
		},
	}
}

// internal reports whether name is an implementation detail rather than a
// function to call: cel-go marks those with @ or wraps them in __.
func internal(name string) bool {
	return strings.Contains(name, "@") || strings.HasPrefix(name, "__")
}

// Function is one function or macro an expression may call, as the
// completion endpoint lists it.
type Function struct {
	Name        string
	Description string
	// Signatures are each overload's, as cel-go formats them, for example
	// "string.split(string) -> list(string)"; a macro's is its call shape.
	Signatures []string
	Macro      bool
}

// functions lists env's callable functions and macros by name: operators
// and the engine's own internal functions are left out.
func functions(env *cel.Env) []Function {
	var out []Function
	for name, fn := range env.Functions() {
		if _, op := operators.FindReverse(name); op || internal(name) {
			continue
		}
		doc := fn.Documentation()
		f := Function{Name: name, Description: doc.Description}
		for _, o := range doc.Children {
			f.Signatures = append(f.Signatures, o.Signature)
		}
		out = append(out, f)
	}
	spaces := namespaces(env)
	for _, m := range env.Macros() {
		f := Function{Name: m.Function(), Macro: true}
		if d, ok := m.(documented); ok && d.Documentation() != nil {
			f.Description = d.Documentation().Description
		}
		args := "…"
		if !variadic(m) {
			args = strings.TrimSuffix(strings.Repeat("_, ", m.ArgCount()), ", ")
		}
		switch ns := namespaceOf(env, m, spaces); {
		case ns != "":
			f.Name = ns + "." + f.Name
			f.Signatures = []string{f.Name + "(" + args + ")"}
		case m.IsReceiverStyle():
			f.Signatures = []string{"_." + f.Name + "(" + args + ")"}
		default:
			f.Signatures = []string{f.Name + "(" + args + ")"}
		}
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b Function) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Signatures[0], b.Signatures[0])
	})
	return out
}

// documented is a macro that carries its documentation, as cel-go's own
// macros do.
type documented interface {
	Documentation() *common.Doc
}

// variadic reports whether m takes any number of arguments: its key has
// * for its count, as function:*:receiver.
func variadic(m cel.Macro) bool {
	return strings.Contains(m.MacroKey(), ":*:")
}

// qualified matches a name an expression can write: identifiers joined by
// dots, which leaves out operators and internal functions.
var qualified = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)*$`)

// namespaces lists the namespaces env's functions are declared in, such as
// math and cel.
func namespaces(env *cel.Env) []string {
	seen := map[string]bool{}
	for name := range env.Functions() {
		if i := strings.LastIndexByte(name, '.'); i > 0 && qualified.MatchString(name[:i]) {
			seen[name[:i]] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// namespaceOf is the namespace a receiver macro is called in, such as math
// for math.greatest, or "" for a macro called on any value, such as all.
// cel-go writes both as receiver macros, a namespaced one expanding only
// on its namespace's name; parsing a call shows which.
func namespaceOf(env *cel.Env, m cel.Macro, spaces []string) string {
	if !m.IsReceiverStyle() {
		return ""
	}
	n := max(m.ArgCount(), 1)
	args := strings.TrimSuffix(strings.Repeat("x, ", n), ", ")
	expands := func(target string) bool {
		parsed, iss := env.Parse(target + "." + m.Function() + "(" + args + ")")
		if iss.Err() != nil {
			// The macro claimed the call and refused its arguments.
			return true
		}
		root := parsed.NativeRep().Expr()
		return root.Kind() != ast.CallKind || root.AsCall().FunctionName() != m.Function()
	}
	// No namespace is called this, so a macro that expands on it takes any
	// target.
	if expands("_infraweave_probe") {
		return ""
	}
	for _, ns := range spaces {
		if expands(ns) {
			return ns
		}
	}
	return ""
}
