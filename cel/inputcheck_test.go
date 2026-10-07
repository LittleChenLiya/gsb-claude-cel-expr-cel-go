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
	"strings"
	"sync/atomic"
	"testing"

	"cel.dev/cel-go/common/types/ref"
)

func newTwoVarEnv(t *testing.T) *Env {
	t.Helper()
	e, err := NewEnv(Variable("a", IntType), Variable("b", IntType))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func mustAST(t *testing.T, e *Env, expr string) *Ast {
	t.Helper()
	ast, iss := e.Compile(expr)
	if iss != nil && iss.Err() != nil {
		t.Fatal(iss.Err())
	}
	return ast
}

func mustProgram(t *testing.T, e *Env, expr string, opts ...ProgramOption) Program {
	t.Helper()
	prg, err := e.Program(mustAST(t, e, expr), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return prg
}

func asInputBindingError(t *testing.T, err error) *InputBindingError {
	t.Helper()
	if err == nil {
		t.Fatal("expected InputBindingError, got nil")
	}
	var ibe *InputBindingError
	if !errors.As(err, &ibe) {
		t.Fatalf("expected *InputBindingError, got %T: %v", err, err)
	}
	return ibe
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestEnvCheckInputBindings(t *testing.T) {
	e := newTwoVarEnv(t)

	tests := []struct {
		name        string
		input       any
		opts        []InputBindingOption
		wantMissing []string
		wantExtra   []string
	}{
		{
			name:        "both missing",
			input:       map[string]any{},
			wantMissing: []string{"a", "b"},
		},
		{
			name:        "one missing",
			input:       map[string]any{"a": int64(1)},
			wantMissing: []string{"b"},
		},
		{
			name:  "explicit nil counts as provided",
			input: map[string]any{"a": nil, "b": nil},
		},
		{
			name:      "extra key reported",
			input:     map[string]any{"a": int64(1), "b": int64(2), "c": int64(3)},
			wantExtra: []string{"c"},
		},
		{
			name:        "missing and extra together",
			input:       map[string]any{"a": int64(1), "c": int64(3)},
			wantMissing: []string{"b"},
			wantExtra:   []string{"c"},
		},
		{
			name:        "extra ignored with option",
			input:       map[string]any{"a": int64(1), "c": int64(3)},
			opts:        []InputBindingOption{AllowExtraVariables()},
			wantMissing: []string{"b"},
		},
		{
			name:  "exact match valid",
			input: map[string]any{"a": int64(1), "b": int64(2)},
		},
		{
			name:        "sub-key does not satisfy declaration",
			input:       map[string]any{"a.b": int64(1), "b": int64(2)},
			wantMissing: []string{"a"},
			wantExtra:   []string{"a.b"},
		},
	}
	for _, tst := range tests {
		tc := tst
		t.Run(tc.name, func(t *testing.T) {
			err := e.CheckInputBindings(tc.input, tc.opts...)
			if len(tc.wantMissing) == 0 && len(tc.wantExtra) == 0 {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			ibe := asInputBindingError(t, err)
			if !equalStrings(ibe.Missing, tc.wantMissing) {
				t.Errorf("missing = %v, want %v", ibe.Missing, tc.wantMissing)
			}
			if !equalStrings(ibe.Extra, tc.wantExtra) {
				t.Errorf("extra = %v, want %v", ibe.Extra, tc.wantExtra)
			}
		})
	}
}

func TestEnvCheckInputBindingsActivation(t *testing.T) {
	e := newTwoVarEnv(t)
	act, err := NewActivation(map[string]any{"a": int64(1)})
	if err != nil {
		t.Fatal(err)
	}
	ibe := asInputBindingError(t, e.CheckInputBindings(act))
	if !equalStrings(ibe.Missing, []string{"b"}) || len(ibe.Extra) != 0 {
		t.Errorf("unexpected error sets: missing=%v extra=%v", ibe.Missing, ibe.Extra)
	}

	// A partial activation enumerates only the variables it supplies; unknown patterns are not
	// treated as supplied keys.
	part, err := PartialVars(map[string]any{"a": int64(1)}, AttributePattern("b"))
	if err != nil {
		t.Fatal(err)
	}
	ibe = asInputBindingError(t, e.CheckInputBindings(part))
	if !equalStrings(ibe.Missing, []string{"b"}) {
		t.Errorf("partial activation missing = %v, want [b]", ibe.Missing)
	}

	// Custom activations which cannot enumerate names produce an error rather than silently
	// skipping validation.
	if err := e.CheckInputBindings(opaqueTestActivation{}); err == nil ||
		!strings.Contains(err.Error(), "cannot enumerate") {
		t.Fatalf("expected enumeration error, got %v", err)
	}
}

func TestProgramValidateInputBindingsDoesNotEvaluate(t *testing.T) {
	e := newTwoVarEnv(t)
	legacy := mustProgram(t, e, "a + b")
	checked := mustProgram(t, e, "a + b", ValidateInputBindings())

	var callsA atomic.Int32
	var callsB atomic.Int32
	input := map[string]any{
		"a": func() any {
			callsA.Add(1)
			return int64(42)
		},
		"b": func() ref.Val {
			callsB.Add(1)
			return nil
		},
		"c": int64(3),
	}
	// Extra key means the check fails; neither the lazy getter nor the expression runs.
	_, _, err := checked.Eval(input)
	ibe := asInputBindingError(t, err)
	if !equalStrings(ibe.Extra, []string{"c"}) || len(ibe.Missing) != 0 {
		t.Fatalf("unexpected error sets: missing=%v extra=%v", ibe.Missing, ibe.Extra)
	}
	if callsA.Load() != 0 || callsB.Load() != 0 {
		t.Errorf("getters invoked during failed check: a=%d b=%d", callsA.Load(), callsB.Load())
	}

	// Missing key with a lazy binding for the present key: same guarantee, no evaluation.
	_, _, err = checked.Eval(map[string]any{
		"a": func() any {
			callsA.Add(1)
			return int64(42)
		},
	})
	ibe = asInputBindingError(t, err)
	if !equalStrings(ibe.Missing, []string{"b"}) || len(ibe.Extra) != 0 {
		t.Fatalf("unexpected error sets: missing=%v extra=%v", ibe.Missing, ibe.Extra)
	}
	if callsA.Load() != 0 {
		t.Errorf("lazy getter for present key invoked: %d", callsA.Load())
	}
	if callsB.Load() != 0 {
		t.Errorf("getter for missing key should be unreachable: %d", callsB.Load())
	}

	// A program built without the option keeps the legacy behavior: a missing var yields the
	// standard runtime no-matching error rather than an InputBindingError.
	_, _, err = legacy.Eval(map[string]any{"a": int64(1)})
	if err == nil || !strings.Contains(err.Error(), "b") {
		t.Fatalf("expected runtime resolution error for missing b, got %v", err)
	}
	var legacyErr *InputBindingError
	if errors.As(err, &legacyErr) {
		t.Fatalf("legacy program must not return InputBindingError, got %v", legacyErr)
	}
}

func TestProgramValidateInputBindingsValidRunsProgram(t *testing.T) {
	e := newTwoVarEnv(t)
	checked := mustProgram(t, e, "a + b", ValidateInputBindings())
	out, _, err := checked.Eval(map[string]any{"a": int64(40), "b": int64(2)})
	if err != nil {
		t.Fatal(err)
	}
	if out.Value() != int64(42) {
		t.Fatalf("eval result = %v, want 42", out.Value())
	}

	// Explicit null is provided, and evaluation proceeds with a null operand, producing the
	// standard CEL type error rather than a binding error.
	_, _, err = checked.Eval(map[string]any{"a": nil, "b": int64(2)})
	if err == nil {
		t.Fatal("expected a CEL evaluation error with a null operand")
	}
	var ibe *InputBindingError
	if errors.As(err, &ibe) {
		t.Fatalf("explicit nil should not be a binding error, got %v", ibe)
	}
}

func TestProgramValidateInputBindingsMissingOnly(t *testing.T) {
	e := newTwoVarEnv(t)
	checked := mustProgram(t, e, "a + b",
		ValidateInputBindings(AllowExtraVariables()))
	// Extra key tolerated: evaluation runs and succeeds since a, b are present.
	out, _, err := checked.Eval(map[string]any{"a": int64(1), "b": int64(2), "c": int64(3)})
	if err != nil {
		t.Fatalf("extra key should be allowed, got %v", err)
	}
	if out.Value() != int64(3) {
		t.Fatalf("eval result = %v, want 3", out.Value())
	}
	// Missing still rejected.
	_, _, err = checked.Eval(map[string]any{"a": int64(1), "c": int64(3)})
	if !equalStrings(asInputBindingError(t, err).Missing, []string{"b"}) {
		t.Fatalf("expected missing b, got %v", err)
	}
}

func TestProgramValidateInputBindingsGlobalVars(t *testing.T) {
	e := newTwoVarEnv(t)
	checked := mustProgram(t, e, "a + b",
		Globals(map[string]any{"b": int64(7)}),
		ValidateInputBindings())
	// b supplied through Globals counts as provided even when omitted from the per-eval input.
	out, _, err := checked.Eval(map[string]any{"a": int64(35)})
	if err != nil {
		t.Fatalf("global var should satisfy declaration, got %v", err)
	}
	if out.Value() != int64(42) {
		t.Fatalf("eval result = %v, want 42", out.Value())
	}
}

func TestProgramValidateInputBindingsContextEval(t *testing.T) {
	e := newTwoVarEnv(t)
	checked := mustProgram(t, e, "a + b", ValidateInputBindings())
	_, _, err := checked.ContextEval(context.Background(), map[string]any{"a": int64(1)})
	if !equalStrings(asInputBindingError(t, err).Missing, []string{"b"}) {
		t.Fatalf("expected missing b from ContextEval, got %v", err)
	}
	out, _, err := checked.ContextEval(context.Background(),
		map[string]any{"a": int64(21), "b": int64(21)})
	if err != nil {
		t.Fatal(err)
	}
	if out.Value() != int64(42) {
		t.Fatalf("context eval result = %v, want 42", out.Value())
	}
}

func TestProgramValidateInputBindingsConcurrentEval(t *testing.T) {
	e := newTwoVarEnv(t)
	checked := mustProgram(t, e, "a + b", ValidateInputBindings())
	res := <-checked.ConcurrentEval(context.Background(), map[string]any{"a": int64(1)})
	if res.Err == nil || !equalStrings(asInputBindingError(t, res.Err).Missing, []string{"b"}) {
		t.Fatalf("expected missing b from ConcurrentEval, got %v", res.Err)
	}
	res = <-checked.ConcurrentEval(context.Background(),
		map[string]any{"a": int64(40), "b": int64(2)})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res.Val.Value() != int64(42) {
		t.Fatalf("concurrent eval result = %v, want 42", res.Val.Value())
	}
}

// opaqueTestActivation cannot enumerate its binding names.
type opaqueTestActivation struct{}

func (opaqueTestActivation) ResolveName(string) (any, bool) { return nil, false }
func (opaqueTestActivation) Parent() Activation             { return nil }
