package harness

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/cucumber/godog"
)

// ErrTagExpression is returned for a focus tag expression that does not
// parse.
var ErrTagExpression = errors.New("tag expression")

// ErrNoScenario is returned when a run's tag selection matches no scenario.
var ErrNoScenario = errors.New("the tag selection matches no scenario")

// Selection is the godog tag expression that selects the scenarios of the
// partition called partition, under the features directory, that also match
// focus. It returns ErrNoScenario when focus is set and selects none of
// them, so a narrowed run that tests nothing fails rather than passes.
//
// ref: decision 0016
func Selection(features, partition, focus string) (string, error) {
	expr, err := TagExpression(partition)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(focus) == "" {
		return expr, nil
	}
	narrowed, err := GodogTags(focus)
	if err != nil {
		return "", err
	}
	expr = And(expr, narrowed)
	selected, err := godog.TestSuite{Options: &godog.Options{Paths: []string{features}, Tags: expr}}.RetrieveFeatures()
	if err != nil {
		return "", fmt.Errorf("the features of %s: %w", features, err)
	}
	for _, f := range selected {
		if len(f.Pickles) > 0 {
			return expr, nil
		}
	}
	return "", fmt.Errorf("%w: %s=%q in partition %q (as godog's %q)",
		ErrNoScenario, EnvTags, focus, partition, narrowed)
}

// GodogTags translates the tag expression expr into the syntax godog's
// filter reads: alternatives joined by "," of conjunctions joined by
// " && " of tags, each negated by a leading "~". expr is a Cucumber tag
// expression, with "or", "and", "not" and parentheses, in which godog's
// ",", "&&" and "~" stand for the operators too. Godog's filter treats
// any other text as part of a tag name, so an untranslated "@a or @b"
// would select nothing.
func GodogTags(expr string) (string, error) {
	p := tagParser{expr: expr, tokens: tagTokens(expr)}
	node, err := p.or()
	if err == nil && p.pos < len(p.tokens) {
		err = p.fail("unexpected %q", p.tokens[p.pos])
	}
	if err != nil {
		return "", err
	}
	clauses := node.dnf(false)
	out := make([]string, len(clauses))
	for i, clause := range clauses {
		terms := make([]string, len(clause))
		for j, lit := range clause {
			terms[j] = lit.tag
			if lit.not {
				terms[j] = "~" + lit.tag
			}
		}
		out[i] = strings.Join(terms, " && ")
	}
	return strings.Join(out, ","), nil
}

// tagTokens splits expr into parentheses, operators and words. "&&" and
// "~" end a word, as in godog's syntax, where "~@a&&@b" is two terms.
func tagTokens(expr string) []string {
	var tokens []string
	word := func(i int) int {
		j := i
		for j < len(expr) && !unicode.IsSpace(rune(expr[j])) && !strings.ContainsRune("(),~", rune(expr[j])) &&
			!strings.HasPrefix(expr[j:], "&&") {
			j++
		}
		return j
	}
	for i := 0; i < len(expr); {
		switch {
		case unicode.IsSpace(rune(expr[i])):
			i++
		case strings.ContainsRune("(),~", rune(expr[i])):
			tokens = append(tokens, expr[i:i+1])
			i++
		case strings.HasPrefix(expr[i:], "&&"):
			tokens = append(tokens, "&&")
			i += 2
		default:
			j := word(i)
			tokens = append(tokens, expr[i:j])
			i = j
		}
	}
	return tokens
}

// tagParser reads a tag expression with "not" binding tighter than "and",
// and "and" tighter than "or", as Cucumber's do.
type tagParser struct {
	expr   string
	tokens []string
	pos    int
}

func (p *tagParser) fail(format string, args ...any) error {
	return fmt.Errorf("%w %q: %s; write tags as @name joined by or, and, not and parentheses",
		ErrTagExpression, p.expr, fmt.Sprintf(format, args...))
}

func (p *tagParser) next(ops ...string) bool {
	if p.pos < len(p.tokens) {
		for _, op := range ops {
			if p.tokens[p.pos] == op {
				p.pos++
				return true
			}
		}
	}
	return false
}

func (p *tagParser) or() (tagNode, error) {
	return p.chain(p.and, "or", ",")
}

func (p *tagParser) and() (tagNode, error) {
	return p.chain(p.unary, "and", "&&")
}

func (p *tagParser) chain(operand func() (tagNode, error), ops ...string) (tagNode, error) {
	node, err := operand()
	if err != nil {
		return tagNode{}, err
	}
	for p.next(ops...) {
		right, err := operand()
		if err != nil {
			return tagNode{}, err
		}
		node = tagNode{op: ops[0], args: []tagNode{node, right}}
	}
	return node, nil
}

func (p *tagParser) unary() (tagNode, error) {
	switch {
	case p.next("not", "~"):
		arg, err := p.unary()
		return tagNode{op: "not", args: []tagNode{arg}}, err
	case p.next("("):
		node, err := p.or()
		if err == nil && !p.next(")") {
			err = p.fail("a ( is not closed")
		}
		return node, err
	case p.pos == len(p.tokens):
		return tagNode{}, p.fail("a tag is missing at the end")
	}
	tok := p.tokens[p.pos]
	if !strings.HasPrefix(tok, "@") || len(tok) == 1 {
		return tagNode{}, p.fail("%q is not a tag", tok)
	}
	p.pos++
	return tagNode{tag: tok}, nil
}

// tagNode is a tag when op is empty, and otherwise "or", "and" or "not"
// of args.
type tagNode struct {
	op   string
	tag  string
	args []tagNode
}

type tagLiteral struct {
	tag string
	not bool
}

// dnf is the node, negated when neg, as alternatives of conjunctions of
// tags or their negations. Godog's filter has no parentheses, so negation
// moves onto the tags and "and" distributes over "or".
func (n tagNode) dnf(neg bool) [][]tagLiteral {
	switch n.op {
	case "not":
		return n.args[0].dnf(!neg)
	case "or", "and":
		left, right := n.args[0].dnf(neg), n.args[1].dnf(neg)
		if (n.op == "or") != neg {
			return append(left, right...)
		}
		var out [][]tagLiteral
		for _, l := range left {
			for _, r := range right {
				out = append(out, append(append([]tagLiteral{}, l...), r...))
			}
		}
		return out
	}
	return [][]tagLiteral{{{tag: n.tag, not: neg}}}
}
