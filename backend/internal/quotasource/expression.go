// Package quotasource reads provider quota responses without accessing credentials or storage.
// Expression parsing is adapted from yetone/magpie internal/provider/balance.go
// at 47b51c8225a66e753dd249cb782472b0dcbee6c4, under the MIT license.
// See ../server/SUBSCRIPTION_THIRD_PARTY_NOTICES.md.
package quotasource

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// readBalancePath picks the amount out of a reply by a dotted path, array
// items by their index: "data.total_available", "balance_infos.0.total_balance".
// It can be sums of paths and numbers, with + - * / and brackets, for a
// relay counting in its own units ("data.total_available / 500000") or
// telling what was used of a plan ("(1 - credits.monthlyCredits / 70) %").
// A "$" or "¥" before it is put in front of the amount; a "%" after it
// shows it as a percent of 1 (0.25 is 25%). A path alone that is not a
// number is shown as it is. A path through a list's items ("a.*.used",
// "a[status=active].used") is several values, which sum(), max(), min()
// or count() make one (balanceExpr). Several amounts, each with a label if
// wanted, go apart by ";" and are shown together: "5h: windowLimits.
// fiveHour.used / windowLimits.fiveHour.cap %; week: …; $credits.
// monthlyCredits" is "5h 0% · week 3.4% · $70.00"; one of them the reply
// hasn't is "—" while the others show.

// BalancePart is one amount of a balance field, for a card to show each
// of several on a line of its own, its label apart from its figure, rather
// than all of them run together: Percent is its share, 0 to 100, when it
// was asked as one ("%" after it), for a meter.
type BalancePart struct {
	Label   string   `json:"label,omitempty"`
	Text    string   `json:"text"`
	Value   *float64 `json:"value,omitempty"`
	Unit    string   `json:"unit,omitempty"`
	Percent *float64 `json:"percent,omitempty"`
	// Error is why the amount couldn't be read, its Text then "—".
	Error string `json:"error,omitempty"`
	// Expires is when the part counts no more, for a balance typed with
	// credit that expires (key_balance_own.go); the GUI says in how many
	// days, its Label then empty
	Expires *time.Time `json:"expires,omitempty"`
}

// joinBalanceParts is the amounts on one line, as Balance tells them:
// "5h 0% · week 3.4% · $70.00".

// readBalanceParts is readBalancePath's amounts, each apart.
func readBalanceParts(b []byte, path string) ([]BalancePart, error) {
	if !strings.Contains(path, ";") && !strings.Contains(path, ":") {
		v, pct, err := readBalanceOne(b, path)
		if err != nil {
			return nil, err
		}
		part := numericPart(b, path, BalancePart{Text: v, Percent: pct})
		return []BalancePart{part}, nil
	}
	var out []BalancePart
	var first error
	read := false
	for part := range strings.SplitSeq(path, ";") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		label, expr, ok := strings.Cut(part, ":")
		if !ok {
			label, expr = "", part
		}
		label = strings.TrimSpace(label)
		v, pct, err := readBalanceOne(b, expr)
		if err != nil {
			// one amount the reply hasn't (an index past the list's end, a
			// window whose cap is 0) is "—", and the others still show
			// (#1993); the whole fails only when none can be read
			if label != "" {
				err = fmt.Errorf("%s: %w", label, err)
			}
			if first == nil {
				first = err
			}
			out = append(out, BalancePart{Label: label, Text: "—", Error: err.Error()})
			continue
		}
		read = true
		out = append(out, numericPart(b, expr, BalancePart{Label: label, Text: v, Percent: pct}))
	}
	if len(out) == 0 {
		return nil, errors.New("no balance path: where in the reply the amount is, e.g. data.balance")
	}
	if !read {
		return nil, first
	}
	return out, nil
}

// readBalanceOne is one amount of a balance path, and its share in percent
// when it was asked as one.
func readBalanceOne(b []byte, path string) (string, *float64, error) {
	path = strings.TrimSpace(path)
	sign := ""
	for _, s := range []string{"$", "¥", "€", "£"} {
		if rest, ok := strings.CutPrefix(path, s); ok {
			sign, path = s, strings.TrimSpace(rest)
			break
		}
	}
	percent := false
	if rest, ok := strings.CutSuffix(path, "%"); ok {
		percent, path = true, strings.TrimSpace(rest)
	}
	if path == "" {
		return "", nil, errors.New("no balance path: where in the reply the amount is, e.g. data.balance")
	}
	var reply any
	if err := json.Unmarshal(b, &reply); err != nil {
		return "", nil, errors.New("the reply is not JSON")
	}
	e := &balanceExpr{src: path, reply: reply}
	if e.lone() {
		// a path alone: its value, a number or not
		v, err := e.at(path)
		if err != nil {
			return "", nil, err
		}
		if n, ok := number(v); ok {
			return balanceAmount(sign, n, percent), balanceShare(n, percent), nil
		}
		if s, ok := v.(string); ok && s != "" && !percent {
			return sign + s, nil, nil
		}
		return "", nil, fmt.Errorf("%q in the reply is not an amount", path)
	}
	n, err := e.number()
	if err != nil {
		return "", nil, err
	}
	if math.IsInf(n, 0) || math.IsNaN(n) {
		return "", nil, fmt.Errorf("the balance path divides by nothing")
	}
	return balanceAmount(sign, n, percent), balanceShare(n, percent), nil
}

// balanceShare is an amount asked as a percent, 0.25 as 25; nil for one
// that wasn't.
func balanceShare(n float64, percent bool) *float64 {
	if !percent {
		return nil
	}
	p := n * 100
	return &p
}

func balanceAmount(sign string, n float64, percent bool) string {
	if percent {
		return sign + strings.TrimSuffix(strconv.FormatFloat(n*100, 'f', 1, 64), ".0") + "%"
	}
	return money(sign, n)
}

// balanceExpr reads a balance path's sum: paths into the reply, numbers,
// + - * / and brackets. A path can go through every item of a list ("*",
// "data.subscriptions.*.used") or those of them that match ("[status=
// active]", "[plan.kind!=free]"), and is then several values, which
// sum(), max(), min() or count() make one (#1993): "sum(a.*.used) /
// sum(a.*.cap)", "max(a.*.used / a.*.cap)". Several values left as they
// are, outside those, are an error rather than a guess at which of them
// was meant. Within them, an item without the path, or one divided by 0,
// is left out.
type balanceExpr struct {
	src   string
	i     int
	reply any
}

// balanceVal is a number, or several (many) when a path went through a
// list; NaN stands for an item that has none.
type balanceVal struct {
	n    float64
	ns   []float64
	many bool
	// items are a path's own values, for count() to count those that are
	// there, numbers or not
	items []any
}

// balanceFuncs make a path's several values one.
var balanceFuncs = map[string]func([]float64) (float64, error){
	"sum": func(ns []float64) (float64, error) {
		s := 0.0
		for _, n := range ns {
			if !math.IsNaN(n) {
				s += n
			}
		}
		return s, nil
	},
	"count": func(ns []float64) (float64, error) {
		c := 0.0
		for _, n := range ns {
			if !math.IsNaN(n) {
				c++
			}
		}
		return c, nil
	},
	"max": func(ns []float64) (float64, error) { return balanceExtreme(ns, "max", 1) },
	"min": func(ns []float64) (float64, error) { return balanceExtreme(ns, "min", -1) },
}

func balanceExtreme(ns []float64, name string, sign float64) (float64, error) {
	best, found := 0.0, false
	for _, n := range ns {
		if !math.IsNaN(n) && (!found || (n-best)*sign > 0) {
			best, found = n, true
		}
	}
	if !found {
		return 0, fmt.Errorf("%s() of the balance path has nothing to take it of", name)
	}
	return best, nil
}

func (e *balanceExpr) space() {
	for e.i < len(e.src) && e.src[e.i] == ' ' {
		e.i++
	}
}

// lone is whether the whole is one path, to one value.
func (e *balanceExpr) lone() bool {
	return !strings.ContainsAny(e.src, "+-*/() [") && e.src != "" && !isDigit(e.src[0])
}

// number is the whole's one number.
func (e *balanceExpr) number() (float64, error) {
	v, err := e.sum()
	if err == nil && e.i < len(e.src) {
		err = fmt.Errorf("the balance path has %q it can't read", e.src[e.i:])
	}
	if err == nil && v.many {
		err = errors.New("the balance path has several values where one is wanted: put them in sum(), max(), min() or count()")
	}
	return v.n, err
}

func (e *balanceExpr) sum() (balanceVal, error) {
	v, err := e.product()
	for err == nil {
		e.space()
		if e.i >= len(e.src) || (e.src[e.i] != '+' && e.src[e.i] != '-') {
			break
		}
		op := e.src[e.i]
		e.i++
		var w balanceVal
		if w, err = e.product(); err == nil {
			v, err = balanceOp(v, w, op)
		}
	}
	return v, err
}

func (e *balanceExpr) product() (balanceVal, error) {
	v, err := e.unary()
	for err == nil {
		e.space()
		if e.i >= len(e.src) || (e.src[e.i] != '*' && e.src[e.i] != '/') {
			break
		}
		op := e.src[e.i]
		e.i++
		var w balanceVal
		if w, err = e.unary(); err == nil {
			v, err = balanceOp(v, w, op)
		}
	}
	return v, err
}

// balanceOp is v op w, item by item when either is several values: two
// lists by the same path go together, a number with each of a list's.
func balanceOp(v, w balanceVal, op byte) (balanceVal, error) {
	one := func(a, b float64) float64 {
		switch op {
		case '+':
			return a + b
		case '-':
			return a - b
		case '*':
			return a * b
		}
		if b == 0 {
			return math.NaN()
		}
		return a / b
	}
	if !v.many && !w.many {
		if op == '/' && w.n == 0 {
			return v, errors.New("the balance path divides by 0")
		}
		return balanceVal{n: one(v.n, w.n)}, nil
	}
	if v.many && w.many && len(v.ns) != len(w.ns) {
		return v, fmt.Errorf("the balance path puts %d values with %d", len(v.ns), len(w.ns))
	}
	size := max(len(v.ns), len(w.ns))
	out := make([]float64, size)
	for i := range out {
		a, b := v.n, w.n
		if v.many {
			a = v.ns[i]
		}
		if w.many {
			b = w.ns[i]
		}
		out[i] = one(a, b)
	}
	return balanceVal{ns: out, many: true}, nil
}

func (e *balanceExpr) unary() (balanceVal, error) {
	e.space()
	if e.i >= len(e.src) {
		return balanceVal{}, errors.New("the balance path ends where a number or a path should be")
	}
	switch c := e.src[e.i]; {
	case c == '-':
		e.i++
		v, err := e.unary()
		if err != nil {
			return v, err
		}
		return balanceOp(balanceVal{n: 0}, v, '-')
	case c == '(':
		e.i++
		return e.closed(e.sum())
	case isDigit(c) || c == '.':
		j := e.i
		for j < len(e.src) && (isDigit(e.src[j]) || e.src[j] == '.') {
			j++
		}
		n, err := strconv.ParseFloat(e.src[e.i:j], 64)
		if err != nil {
			return balanceVal{}, fmt.Errorf("the balance path has %q, not a number", e.src[e.i:j])
		}
		e.i = j
		return balanceVal{n: n}, nil
	}
	j := e.i
	for j < len(e.src) {
		c := e.src[j]
		if c == '[' {
			k := strings.IndexByte(e.src[j:], ']')
			if k < 0 {
				return balanceVal{}, errors.New("the balance path has a [ without its ]")
			}
			j += k + 1
			continue
		}
		// "*" right after a dot, or at the start, is every item of a list
		if c == '*' && (j == e.i || e.src[j-1] == '.') {
			j++
			continue
		}
		if strings.IndexByte("+-*/() ", c) >= 0 {
			break
		}
		j++
	}
	if j == e.i {
		return balanceVal{}, fmt.Errorf("the balance path has %q where a number or a path should be", e.src[e.i:])
	}
	path := e.src[e.i:j]
	e.i = j
	if f, ok := balanceFuncs[path]; ok && e.i < len(e.src) && e.src[e.i] == '(' {
		e.i++
		v, err := e.closed(e.sum())
		if err != nil {
			return v, err
		}
		ns := v.ns
		if !v.many {
			ns = []float64{v.n}
		}
		if path == "count" && v.items != nil {
			ns = make([]float64, 0, len(v.items))
			for _, x := range v.items {
				if x != nil {
					ns = append(ns, 1)
				}
			}
		}
		n, err := f(ns)
		return balanceVal{n: n}, err
	}
	vs, many, err := e.all(path)
	if err != nil {
		return balanceVal{}, err
	}
	if !many {
		n, ok := number(vs[0])
		if !ok {
			return balanceVal{}, fmt.Errorf("%q in the reply is not a number", path)
		}
		return balanceVal{n: n}, nil
	}
	ns := make([]float64, len(vs))
	for i, x := range vs {
		ns[i] = math.NaN()
		if n, ok := number(x); ok {
			ns[i] = n
		}
	}
	return balanceVal{ns: ns, many: true, items: vs}, nil
}

// closed is a bracket's inside, with the ) after it.
func (e *balanceExpr) closed(v balanceVal, err error) (balanceVal, error) {
	if err != nil {
		return v, err
	}
	e.space()
	if e.i >= len(e.src) || e.src[e.i] != ')' {
		return v, errors.New("the balance path has a ( without its )")
	}
	e.i++
	return v, nil
}

// at is what is at a dotted path in the reply, the path to one value.
func (e *balanceExpr) at(path string) (any, error) {
	vs, many, err := e.all(path)
	if err != nil {
		return nil, err
	}
	if many {
		return nil, fmt.Errorf("%q is several values: put it in sum(), max(), min() or count()", path)
	}
	return vs[0], nil
}

// all is what is at a dotted path in the reply: one value, or, when the
// path goes through a list by "*" or "[…]", every item's (many), nil for
// an item without the rest of the path.
func (e *balanceExpr) all(path string) ([]any, bool, error) {
	segs, err := balanceSegments(path)
	if err != nil {
		return nil, false, err
	}
	vs, many := []any{e.reply}, false
	for _, s := range segs {
		var next []any
		for _, v := range vs {
			if v == nil {
				next = append(next, nil)
				continue
			}
			next = append(next, s.step(v)...)
		}
		many = many || s.spreads()
		vs = next
		if !many && (len(vs) != 1 || vs[0] == nil) {
			return nil, false, fmt.Errorf("nothing at %q in the reply", path)
		}
	}
	return vs, many, nil
}

// balanceSegment is one step of a path: a key, an index or "*", and the
// tests an item must pass, "[status=active]": a list's items that pass,
// or an object when it does.
type balanceSegment struct {
	key   string
	tests []balanceTest
}

type balanceTest struct {
	path []string
	not  bool
	want string
}

func (s balanceSegment) spreads() bool { return s.key == "*" || len(s.tests) > 0 }

// step is what one value leads to: one value, or a list's items.
func (s balanceSegment) step(v any) []any {
	if s.key == "*" {
		v = balanceItems(v)
	} else if s.key != "" {
		v = balanceChild(v, s.key)
	}
	if len(s.tests) == 0 {
		if s.key == "*" {
			return v.([]any)
		}
		return []any{v}
	}
	// a list's items that pass, or an object itself when it does
	items, ok := v.([]any)
	if !ok {
		items = []any{v}
	}
	var out []any
	for _, x := range items {
		if x != nil && s.passes(x) {
			out = append(out, x)
		}
	}
	return out
}

func (s balanceSegment) passes(x any) bool {
	for _, t := range s.tests {
		v := x
		for _, k := range t.path {
			if v = balanceChild(v, k); v == nil {
				break
			}
		}
		got := ""
		switch y := v.(type) {
		case string:
			got = y
		case float64:
			got = strconv.FormatFloat(y, 'f', -1, 64)
		case bool:
			got = strconv.FormatBool(y)
		}
		if (v != nil && got == t.want) == t.not {
			return false
		}
	}
	return true
}

// balanceChild is a key of an object, or an index of a list.
func balanceChild(v any, k string) any {
	switch x := v.(type) {
	case map[string]any:
		return x[k]
	case []any:
		if i, err := strconv.Atoi(k); err == nil && i >= 0 && i < len(x) {
			return x[i]
		}
	}
	return nil
}

// balanceItems is a list's items, or an object's values by key.
func balanceItems(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case map[string]any:
		keys := slices.Sorted(maps.Keys(x))
		out := make([]any, len(keys))
		for i, k := range keys {
			out[i] = x[k]
		}
		return out
	}
	return []any{}
}

// balanceSegments splits a path at its dots, a "[…]" test kept whole.
func balanceSegments(path string) ([]balanceSegment, error) {
	var out []balanceSegment
	for rest := path; ; {
		end := 0
		for end < len(rest) && rest[end] != '.' {
			if rest[end] == '[' {
				k := strings.IndexByte(rest[end:], ']')
				if k < 0 {
					return nil, errors.New("the balance path has a [ without its ]")
				}
				end += k
			}
			end++
		}
		seg, err := balanceSegmentOf(rest[:end], path)
		if err != nil {
			return nil, err
		}
		out = append(out, seg)
		if end == len(rest) {
			return out, nil
		}
		rest = rest[end+1:]
	}
}

func balanceSegmentOf(raw, path string) (balanceSegment, error) {
	key, tests, _ := strings.Cut(raw, "[")
	s := balanceSegment{key: key}
	if key == "" && tests == "" {
		return s, fmt.Errorf("nothing at %q in the reply", path)
	}
	for tests != "" {
		body, after, ok := strings.Cut(tests, "]")
		if !ok {
			return s, errors.New("the balance path has a [ without its ]")
		}
		t := balanceTest{}
		left, want, ok := strings.Cut(body, "=")
		if !ok || strings.TrimSpace(left) == "" {
			return s, fmt.Errorf("the balance path has [%s], not a test like [status=active]", body)
		}
		if l, isNot := strings.CutSuffix(left, "!"); isNot {
			t.not, left = true, l
		}
		t.path = strings.Split(strings.TrimSpace(left), ".")
		t.want = strings.Trim(strings.TrimSpace(want), `'"`)
		s.tests = append(s.tests, t)
		tests = strings.TrimPrefix(after, "[")
	}
	return s, nil
}

func money(sign string, v float64) string {
	return sign + strconv.FormatFloat(v, 'f', 2, 64)
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}
