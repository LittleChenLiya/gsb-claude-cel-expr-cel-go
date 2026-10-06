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
	"fmt"
	"strings"
	"testing"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

// hostCallCounterEnv creates an environment with a single host function, `twice(int) -> int`,
// which increments the supplied counter every time the host function is actually entered.
func hostCallCounterEnv(t *testing.T, calls *int) *Env {
	t.Helper()
	env, err := NewEnv(
		Function("twice",
			Overload("twice_int",
				[]*Type{IntType}, IntType,
				UnaryBinding(func(arg ref.Val) ref.Val {
					*calls++
					return arg.(types.Int) * 2
				}),
			),
		),
	)
	if err != nil {
		t.Fatalf("NewEnv() failed: %v", err)
	}
	return env
}

func hostCallProgram(t *testing.T, env *Env, expr string, limit uint64) Program {
	t.Helper()
	ast, iss := env.Compile(expr)
	if iss.Err() != nil {
		t.Fatalf("env.Compile(%q) failed: %v", expr, iss.Err())
	}
	prg, err := env.Program(ast, HostCallLimit(limit))
	if err != nil {
		t.Fatalf("env.Program() failed: %v", err)
	}
	return prg
}

// TestHostCallLimitThreeElementList verifies that with a three-item list and a host call
// budget of two, the third call is rejected before the host function is entered.
func TestHostCallLimitThreeElementList(t *testing.T) {
	calls := 0
	env := hostCallCounterEnv(t, &calls)
	prg := hostCallProgram(t, env, `[twice(1), twice(2), twice(3)]`, 2)

	out, _, err := prg.Eval(NoVars())
	if err == nil || !strings.Contains(err.Error(), "host call limit exceeded") {
		t.Errorf("Eval() got (%v, %v), wanted 'host call limit exceeded' error", out, err)
	}
	if calls != 2 {
		t.Errorf("host function entered %d times, wanted exactly 2 (third call rejected before entry)", calls)
	}
}

// TestHostCallLimitMacroIterationsShareBudget verifies that macro (comprehension)
// iterations draw from the same per-Eval budget rather than a per-iteration budget.
//
// The map expansion charges one list-concat and one twice() call per iteration, so a
// budget of four admits exactly two full iterations; the third iteration is rejected
// before its next host call.
func TestHostCallLimitMacroIterationsShareBudget(t *testing.T) {
	calls := 0
	env := hostCallCounterEnv(t, &calls)
	prg := hostCallProgram(t, env, `[1, 2, 3].map(x, twice(x))`, 4)

	out, _, err := prg.Eval(NoVars())
	if err == nil || !strings.Contains(err.Error(), "host call limit exceeded") {
		t.Errorf("Eval() got (%v, %v), wanted 'host call limit exceeded' error", out, err)
	}
	if calls != 2 {
		t.Errorf("host function entered %d times, wanted exactly 2 (iterations share the Eval budget)", calls)
	}
}

// TestHostCallLimitShortCircuitNotCharged verifies that calls skipped by short-circuit
// evaluation are never charged against the budget.
func TestHostCallLimitShortCircuitNotCharged(t *testing.T) {
	tests := []struct {
		expr string
		want ref.Val
	}{
		{expr: `false && twice(1) == 2`, want: types.False},
		{expr: `true || twice(1) == 2`, want: types.True},
	}
	for _, tc := range tests {
		calls := 0
		env := hostCallCounterEnv(t, &calls)
		prg := hostCallProgram(t, env, tc.expr, 1)
		out, _, err := prg.Eval(NoVars())
		if err != nil {
			t.Errorf("Eval(%q) failed: %v", tc.expr, err)
			continue
		}
		if out != tc.want {
			t.Errorf("Eval(%q) got %v, wanted %v", tc.expr, out, tc.want)
		}
		if calls != 0 {
			t.Errorf("Eval(%q) entered the host function %d times, wanted 0 for a short-circuited call", tc.expr, calls)
		}
	}
}

// TestHostCallLimitResetsPerEval verifies that the budget is per-Eval: each independent
// evaluation starts with a fresh budget.
func TestHostCallLimitResetsPerEval(t *testing.T) {
	calls := 0
	env := hostCallCounterEnv(t, &calls)
	prg := hostCallProgram(t, env, `[twice(1), twice(2)]`, 2)

	for i := 0; i < 2; i++ {
		out, _, err := prg.Eval(NoVars())
		if err != nil {
			t.Fatalf("Eval() pass %d failed: %v", i, err)
		}
		want := types.DefaultTypeAdapter.NativeToValue([]int64{2, 4})
		if out.Equal(want) != types.True {
			t.Errorf("Eval() pass %d got %v, wanted %v", i, out, want)
		}
		if calls != (i+1)*2 {
			t.Errorf("host function entered %d times after pass %d, wanted %d (budget resets per Eval)", calls, i, (i+1)*2)
		}
	}
}

// ExampleHostCallLimit demonstrates bounding the number of host function calls a single
// evaluation may make. The three-item list would require three calls, but the budget of
// two rejects the third call before the host function is entered.
func ExampleHostCallLimit() {
	env, err := NewEnv(
		Function("twice",
			Overload("twice_int",
				[]*Type{IntType}, IntType,
				UnaryBinding(func(arg ref.Val) ref.Val {
					return arg.(types.Int) * 2
				}),
			),
		),
	)
	if err != nil {
		panic(err)
	}
	ast, iss := env.Compile(`[twice(1), twice(2), twice(3)]`)
	if iss.Err() != nil {
		panic(iss.Err())
	}
	prg, err := env.Program(ast, HostCallLimit(2))
	if err != nil {
		panic(err)
	}
	_, _, err = prg.Eval(NoVars())
	fmt.Println(err)

	// Every independent evaluation starts with a fresh budget, so a two-item
	// list still succeeds under the same limit of two.
	ast2, iss := env.Compile(`[twice(1), twice(2)]`)
	if iss.Err() != nil {
		panic(iss.Err())
	}
	prg2, err := env.Program(ast2, HostCallLimit(2))
	if err != nil {
		panic(err)
	}
	out, _, err := prg2.Eval(NoVars())
	fmt.Println(out, err)

	// Output:
	// host call limit exceeded
	// [2, 4] <nil>
}
