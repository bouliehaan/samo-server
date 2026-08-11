package channels

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Conditions are the small closed language a block uses to say "only when".
//
// Deliberately not a scripting language. A closed vocabulary can be validated
// when the plan is saved rather than failing silently at three in the morning,
// and — just as important — it can be rendered back into English in the
// decision record, so "why is this block on" has an answer a person can read.
// Anything the vocabulary cannot express is a sign the model is missing a
// concept, not a sign it needs eval().

// Condition is a parsed, evaluatable block condition.
type Condition struct {
	raw   string
	terms []conditionTerm
}

type conditionTerm struct {
	negated bool
	kind    string // "always" | "window" | "poolAvailable" | "obligations"
	op      string
	value   time.Duration
	count   int
	pool    string
	text    string
}

// ConditionContext is everything a condition may ask about.
type ConditionContext struct {
	// Window is the time until the next hard anchor. Zero means unbounded —
	// nothing is booked within the horizon — which satisfies every lower bound.
	Window time.Duration
	// PoolAvailable reports whether a pool can currently produce anything.
	PoolAvailable func(poolID string) bool
	// ObligationsPending is how many things the station currently owes.
	ObligationsPending int
}

// ParseCondition validates and compiles a condition. An empty string is always
// true, which is what makes `when` optional everywhere it appears.
func ParseCondition(raw string) (Condition, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Condition{raw: ""}, nil
	}
	condition := Condition{raw: trimmed}
	for _, part := range strings.Split(trimmed, "&&") {
		term, err := parseConditionTerm(part)
		if err != nil {
			return Condition{}, err
		}
		condition.terms = append(condition.terms, term)
	}
	return condition, nil
}

func parseConditionTerm(raw string) (conditionTerm, error) {
	text := strings.TrimSpace(raw)
	term := conditionTerm{text: text}
	for strings.HasPrefix(text, "!") {
		term.negated = !term.negated
		text = strings.TrimSpace(text[1:])
	}
	lower := strings.ToLower(text)

	if lower == "always" || lower == "true" {
		term.kind = "always"
		return term, nil
	}
	if strings.HasPrefix(lower, "window") {
		rest := strings.TrimSpace(text[len("window"):])
		if strings.EqualFold(rest, "unbounded") {
			term.kind = "windowUnbounded"
			return term, nil
		}
		for _, op := range []string{">=", "<=", ">", "<"} {
			if strings.HasPrefix(rest, op) {
				value, err := parseDuration(strings.TrimSpace(rest[len(op):]))
				if err != nil {
					return term, fmt.Errorf("in %q: %v", raw, err)
				}
				term.kind, term.op, term.value = "window", op, value
				return term, nil
			}
		}
		return term, fmt.Errorf("in %q: window needs a comparison, e.g. window >= 45m", raw)
	}
	if strings.HasPrefix(lower, "obligations.pending") {
		rest := strings.TrimSpace(text[len("obligations.pending"):])
		for _, op := range []string{">=", "<=", "==", ">", "<"} {
			if strings.HasPrefix(rest, op) {
				value, err := strconv.Atoi(strings.TrimSpace(rest[len(op):]))
				if err != nil {
					return term, fmt.Errorf("in %q: %v is not a count", raw, rest)
				}
				term.kind, term.op, term.count = "obligations", op, value
				return term, nil
			}
		}
		return term, fmt.Errorf("in %q: obligations.pending needs a comparison, "+
			"e.g. obligations.pending > 0", raw)
	}
	if strings.HasPrefix(lower, "pool.") {
		rest := text[len("pool."):]
		name, suffix, ok := strings.Cut(rest, ".")
		if !ok || !strings.EqualFold(suffix, "available") {
			return term, fmt.Errorf("in %q: only pool.<id>.available is understood", raw)
		}
		if strings.TrimSpace(name) == "" {
			return term, fmt.Errorf("in %q: pool condition names no pool", raw)
		}
		term.kind, term.pool = "poolAvailable", strings.TrimSpace(name)
		return term, nil
	}
	return term, fmt.Errorf("%q is not something a block can ask about "+
		"(try: always, window >= 45m, window unbounded, pool.<id>.available, "+
		"obligations.pending > 0)", raw)
}

// Empty reports whether this condition constrains anything.
func (c Condition) Empty() bool { return len(c.terms) == 0 }

// String is the condition as written, for the decision record.
func (c Condition) String() string { return c.raw }

// Eval reports whether the condition holds. An unparsed zero Condition is
// vacuously true, which is what an absent `when` means.
func (c Condition) Eval(ctx ConditionContext) bool {
	for _, term := range c.terms {
		if term.eval(ctx) == term.negated {
			return false
		}
	}
	return true
}

func (t conditionTerm) eval(ctx ConditionContext) bool {
	switch t.kind {
	case "always":
		return true
	case "windowUnbounded":
		return ctx.Window <= 0
	case "window":
		if ctx.Window <= 0 {
			// Nothing booked ahead: every lower bound is satisfied and every
			// upper bound is not.
			return t.op == ">=" || t.op == ">"
		}
		switch t.op {
		case ">=":
			return ctx.Window >= t.value
		case ">":
			return ctx.Window > t.value
		case "<=":
			return ctx.Window <= t.value
		case "<":
			return ctx.Window < t.value
		}
		return false
	case "obligations":
		switch t.op {
		case ">=":
			return ctx.ObligationsPending >= t.count
		case ">":
			return ctx.ObligationsPending > t.count
		case "<=":
			return ctx.ObligationsPending <= t.count
		case "<":
			return ctx.ObligationsPending < t.count
		case "==":
			return ctx.ObligationsPending == t.count
		}
		return false
	case "poolAvailable":
		if ctx.PoolAvailable == nil {
			return false
		}
		return ctx.PoolAvailable(t.pool)
	}
	return false
}
