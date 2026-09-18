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
	// ObligationsReady is how many of them could air right now without a rule
	// being bent — see obligations.ready in parseConditionTerm.
	ObligationsReady int
	// EnteredToday is how many times each block has already been entered in
	// this listening day, for the `maxPerDay` caps.
	EnteredToday map[string]int
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
	// obligations.pending is everything the station owes; obligations.ready is
	// the part of it that could go out right now without bending a rule.
	//
	// They differ by exactly the things a block gated on "while episodes are
	// owed" should not be waiting on: a second surfacing with hours of
	// separation still to run, an episode too long for the room before the
	// next booked show, one held for the listening day. Gated on `pending`, a
	// new-episodes block never hands over while any of those exist — which on
	// a two-surfacing plan is the whole afternoon — and every position it plays
	// in the meantime is an obligation position with nothing owed to give it.
	for _, name := range []string{"obligations.pending", "obligations.ready"} {
		if !strings.HasPrefix(lower, name) {
			continue
		}
		kind := "obligations"
		if name == "obligations.ready" {
			kind = "obligationsReady"
		}
		rest := strings.TrimSpace(text[len(name):])
		for _, op := range []string{">=", "<=", "==", ">", "<"} {
			if strings.HasPrefix(rest, op) {
				value, err := strconv.Atoi(strings.TrimSpace(rest[len(op):]))
				if err != nil {
					return term, fmt.Errorf("in %q: %v is not a count", raw, rest)
				}
				term.kind, term.op, term.count = kind, op, value
				return term, nil
			}
		}
		return term, fmt.Errorf("in %q: %s needs a comparison, e.g. %s > 0", raw, name, name)
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
		"obligations.pending > 0, obligations.ready > 0)", raw)
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
	case "obligations", "obligationsReady":
		count := ctx.ObligationsPending
		if t.kind == "obligationsReady" {
			count = ctx.ObligationsReady
		}
		switch t.op {
		case ">=":
			return count >= t.count
		case ">":
			return count > t.count
		case "<=":
			return count <= t.count
		case "<":
			return count < t.count
		case "==":
			return count == t.count
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
