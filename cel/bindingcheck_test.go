// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cel

import (
	"context"
	"errors"
	"testing"

	"cel.dev/cel-go/common/types/ref"
)

// counterActivation counts how often its lazy getters are invoked. The precheck must never
// evaluate an expression or invoke a lazy binding, so the counters must remain zero even when the
// check rejects the input.
type counterActivation struct {
	values map[string]any
	calls  map[string]int
}

func (c *counterActivation) ResolveName(name string) (any, bool) {
	if v, ok := c.values[name]; ok {
		if fn, isLazy := v.(func() ref.Val); isLazy {
			c.calls[name]++
			return fn(), true
		}
		return v, true
	}
	return nil, false
}

func (c *counterActivation) Parent() Activation { return nil }

func (c *counterActivation) BindingKeySets() (map[string]bool, map[string]bool) {
	present := map[string]bool{}
	nilBound := map[string]bool{}
	for name, val := range c.values {
		present[name] = true
		if val == nil {
			nilBound[name] = true
		}
	}
	return present, nilBound
}

func bindingCheckTestEnv(t *testing.T) (*Env, *Ast) {
	t.Helper()
	e, err := NewEnv(
		Variable("a", StringType),
		Variable("b", StringType),
	)
	if err != nil {
		t.Fatalf("NewEnv() failed: %v", err)
	}
	ast, iss := e.Compile(`a + b`)
	if iss.Err() != nil {
		t.Fatalf("Compile() failed: %v", iss.Err())
	}
	return e, ast
}

func TestCheckInputBindingsReportOnly(t *testing.T) {
	e, ast := bindingCheckTestEnv(t)

	tests := []struct {
		name    string
		bind    map[string]any
		missing []string
		null    []string
		extra   []string
		ok      bool
	}{
		{
			name:    "missing one",
			bind:    map[string]any{"a": "x"},
			missing: []string{"b"},
			ok:      false,
		},
		{
			name:    "missing both",
			bind:    map[string]any{},
			missing: []string{"a", "b"},
			ok:      false,
		},
		{
			name: "explicit null is supplied",
			bind: map[string]any{"a": "x", "b": nil},
			null: []string{"b"},
			ok:   true,
		},
		{
			name:  "extra key",
			bind:  map[string]any{"a": "x", "b": "y", "c": "z"},
			extra: []string{"c"},
			ok:    false,
		},
		{
			name: "complete and exact",
			bind: map[string]any{"a": "x", "b": "y"},
			ok:   true,
		},
	}
	for _, tst := range tests {
		tc := tst
		t.Run(tc.name, func(t *testing.T) {
			check, err := e.CheckInputBindings(ast, tc.bind)
			if err != nil {
				t.Fatalf("CheckInputBindings() returned error in report-only mode: %v", err)
			}
			if check.OK() != tc.ok {
				t.Errorf("OK() = %v, wanted %v (check=%+v)", check.OK(), tc.ok, check)
			}
			if !equalStringSlice(check.MissingVars, tc.missing) {
				t.Errorf("MissingVars = %v, wanted %v", check.MissingVars, tc.missing)
			}
			if !equalStringSlice(check.NullVars, tc.null) {
				t.Errorf("NullVars = %v, wanted %v", check.NullVars, tc.null)
			}
			if !equalStringSlice(check.ExtraVars, tc.extra) {
				t.Errorf("ExtraVars = %v, wanted %v", check.ExtraVars, tc.extra)
			}
		})
	}
}

func TestCheckInputBindingsReject(t *testing.T) {
	e, ast := bindingCheckTestEnv(t)

	// Reject-missing-only: missing is an error, extra is reported but not rejected.
	check, err := e.CheckInputBindings(ast,
		map[string]any{"c": "z"},
		RejectMissingBindings())
	var bce *BindingCheckError
	if !errors.As(err, &bce) {
		t.Fatalf("expected BindingCheckError, got %v", err)
	}
	if !equalStringSlice(bce.MissingVars, []string{"a", "b"}) {
		t.Errorf("MissingVars = %v, wanted [a b]", bce.MissingVars)
	}
	if len(bce.ExtraVars) != 0 {
		t.Errorf("missing-only policy must not reject extras, got %v", bce.ExtraVars)
	}
	if !equalStringSlice(check.ExtraVars, []string{"c"}) {
		t.Errorf("report should still list extra vars, got %v", check.ExtraVars)
	}

	// Explicit null is not missing and therefore must not be rejected.
	if _, err := e.CheckInputBindings(ast,
		map[string]any{"a": "x", "b": nil},
		RejectMissingBindings()); err != nil {
		t.Errorf("explicit nil binding rejected as missing: %v", err)
	}

	// Both policies reject both categories.
	_, err = e.CheckInputBindings(ast,
		map[string]any{"b": "y", "c": "z"},
		RejectMissingBindings(), RejectExtraBindings())
	if !errors.As(err, &bce) {
		t.Fatalf("expected BindingCheckError, got %v", err)
	}
	if !equalStringSlice(bce.MissingVars, []string{"a"}) ||
		!equalStringSlice(bce.ExtraVars, []string{"c"}) {
		t.Errorf("unexpected rejected sets: missing=%v extra=%v", bce.MissingVars, bce.ExtraVars)
	}

	// Exact bindings pass.
	if _, err := e.CheckInputBindings(ast,
		map[string]any{"a": "x", "b": "y"},
		RejectMissingBindings(), RejectExtraBindings()); err != nil {
		t.Errorf("valid bindings rejected: %v", err)
	}
}

func TestCheckInputBindingsDoesNotEvaluate(t *testing.T) {
	e, ast := bindingCheckTestEnv(t)
	calls := map[string]int{}
	act := &counterActivation{
		values: map[string]any{
			"a": func() ref.Val {
				calls["a"]++
				return nil
			},
			"b": nil, // explicit nil, supplied
		},
	}
	// b is explicitly nil and a is lazy; neither value may be resolved by the check.
	check, err := e.CheckInputBindings(ast, act)
	if err != nil {
		t.Fatalf("CheckInputBindings() failed: %v", err)
	}
	if calls["a"] != 0 {
		t.Errorf("lazy binding for a was evaluated %d times during precheck", calls["a"])
	}
	if len(check.MissingVars) != 0 {
		t.Errorf("explicit nil + lazy keys must both count as supplied, missing=%v", check.MissingVars)
	}
	if !equalStringSlice(check.NullVars, []string{"b"}) {
		t.Errorf("NullVars = %v, wanted [b]", check.NullVars)
	}
}

func TestProgramBindingPrecheck(t *testing.T) {
	e, ast := bindingCheckTestEnv(t)

	// A counting getter ensures the expression never runs when the precheck rejects.
	aCalls := 0
	base := map[string]any{
		"a": func() any {
			aCalls++
			return "x"
		},
	}

	prg, err := e.Program(ast, CheckInputBindings(
		RejectMissingBindings(), RejectExtraBindings()))
	if err != nil {
		t.Fatalf("Program() failed: %v", err)
	}

	// Missing b, extra c: reject before evaluation.
	_, _, err = prg.Eval(map[string]any{"a": "x", "c": "z"})
	var bce *BindingCheckError
	if !errors.As(err, &bce) {
		t.Fatalf("expected BindingCheckError on Eval, got %v", err)
	}
	if !equalStringSlice(bce.MissingVars, []string{"b"}) ||
		!equalStringSlice(bce.ExtraVars, []string{"c"}) {
		t.Errorf("unexpected rejected sets: missing=%v extra=%v", bce.MissingVars, bce.ExtraVars)
	}

	// Lazy getter must not have fired.
	if _, _, err = prg.Eval(base); err == nil {
		t.Fatal("expected rejection for missing b via lazy-only input")
	} else if !errors.As(err, &bce) {
		t.Fatalf("expected BindingCheckError, got %v", err)
	}
	if aCalls != 0 {
		t.Errorf("lazy binding evaluated %d times during rejected Eval", aCalls)
	}

	// Valid bindings proceed through the normal Program evaluation path.
	out, _, err := prg.Eval(map[string]any{"a": "foo", "b": "bar"})
	if err != nil {
		t.Fatalf("Eval() of valid bindings failed: %v", err)
	}
	if out.Value() != "foobar" {
		t.Errorf("Eval() = %v, wanted foobar", out.Value())
	}
}

func TestProgramBindingPrecheckGlobals(t *testing.T) {
	// A variable supplied via Globals counts as supplied by the precheck too.
	e, ast := bindingCheckTestEnv(t)
	prg, err := e.Program(ast,
		Globals(map[string]any{"b": "bar"}),
		CheckInputBindings(RejectMissingBindings(), RejectExtraBindings()))
	if err != nil {
		t.Fatalf("Program() failed: %v", err)
	}
	out, _, err := prg.Eval(map[string]any{"a": "foo"})
	if err != nil {
		t.Fatalf("Eval() with global binding failed: %v", err)
	}
	if out.Value() != "foobar" {
		t.Errorf("Eval() = %v, wanted foobar", out.Value())
	}
	if _, _, err := prg.Eval(map[string]any{"a": "foo", "c": "z"}); err == nil {
		t.Fatal("expected extra-key rejection despite globals, got nil")
	}
}

func TestProgramDefaultRegressionNoPrecheck(t *testing.T) {
	// Without the option, evaluation behaves exactly as before, including tolerating extra keys
	// and accepting inputs via the legacy interface.
	e, ast := bindingCheckTestEnv(t)
	prg, err := e.Program(ast)
	if err != nil {
		t.Fatalf("Program() failed: %v", err)
	}
	out, _, err := prg.Eval(map[string]any{"a": "foo", "b": "bar", "unused": 1})
	if err != nil {
		t.Fatalf("default Eval() failed: %v", err)
	}
	if out.Value() != "foobar" {
		t.Errorf("default Eval() = %v, wanted foobar", out.Value())
	}

	// ContextEval keeps working without any precheck.
	out, _, err = prg.ContextEval(context.Background(),
		map[string]any{"a": "foo", "b": "bar"})
	if err != nil {
		t.Fatalf("default ContextEval() failed: %v", err)
	}
	if out.Value() != "foobar" {
		t.Errorf("default ContextEval() = %v, wanted foobar", out.Value())
	}
}

func TestCheckInputBindingsComprehensionLocals(t *testing.T) {
	// The iter variable x and accumulator __result__ are comprehension-local and must not be
	// treated as missing root inputs.
	e, err := NewEnv(
		Variable("items", ListType(StringType)),
		Variable("prefix", StringType),
	)
	if err != nil {
		t.Fatalf("NewEnv() failed: %v", err)
	}
	ast, iss := e.Compile(`items.map(x, prefix + x)`)
	if iss.Err() != nil {
		t.Fatalf("Compile() failed: %v", iss.Err())
	}
	check, err := e.CheckInputBindings(ast, map[string]any{
		"items":  []string{"a"},
		"prefix": "p",
	})
	if err != nil {
		t.Fatalf("CheckInputBindings() failed: %v", err)
	}
	if !check.OK() {
		t.Errorf("expected clean check, got %+v", check)
	}
	if len(check.MissingVars) != 0 {
		t.Errorf("comprehension locals reported as missing roots: %v", check.MissingVars)
	}

	missing, err := e.CheckInputBindings(ast, map[string]any{"items": []string{"a"}})
	if err != nil {
		t.Fatalf("CheckInputBindings() failed: %v", err)
	}
	if !equalStringSlice(missing.MissingVars, []string{"prefix"}) {
		t.Errorf("MissingVars = %v, wanted [prefix]", missing.MissingVars)
	}
}

func TestCheckInputBindingsQualifiedNames(t *testing.T) {
	// Inside a container the checker resolves simple identifiers to their qualified names; the
	// precheck must accept the qualified supply keys and flag simple keys as extra, matching the
	// runtime's own resolution behavior.
	e, err := NewEnv(
		Container("ns.app"),
		Variable("ns.app.a", StringType),
	)
	if err != nil {
		t.Fatalf("NewEnv() failed: %v", err)
	}
	ast, iss := e.Compile(`a`)
	if iss.Err() != nil {
		t.Fatalf("Compile() failed: %v", iss.Err())
	}
	check, err := e.CheckInputBindings(ast, map[string]any{"ns.app.a": "v"})
	if err != nil {
		t.Fatalf("CheckInputBindings() failed: %v", err)
	}
	if !check.OK() {
		t.Errorf("qualified supply rejected: %+v", check)
	}
	check, err = e.CheckInputBindings(ast, map[string]any{"a": "v"})
	if err != nil {
		t.Fatalf("CheckInputBindings() failed: %v", err)
	}
	if !equalStringSlice(check.MissingVars, []string{"ns.app.a"}) ||
		!equalStringSlice(check.ExtraVars, []string{"a"}) {
		t.Errorf("simple supply mismatch: missing=%v extra=%v", check.MissingVars, check.ExtraVars)
	}
}

func TestCheckInputBindingsRequiresCheckedAst(t *testing.T) {
	e, err := NewEnv(Variable("a", StringType))
	if err != nil {
		t.Fatalf("NewEnv() failed: %v", err)
	}
	parsed, iss := e.Parse("a")
	if iss.Err() != nil {
		t.Fatalf("Parse() failed: %v", iss.Err())
	}
	if _, err := e.CheckInputBindings(parsed, map[string]any{"a": "x"}); err == nil {
		t.Fatal("expected error for unchecked ast, got nil")
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
