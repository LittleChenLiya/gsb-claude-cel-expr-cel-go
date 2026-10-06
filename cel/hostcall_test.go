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
	"strings"
	"testing"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

// hostCallCountingEnv builds an environment with a single 'pos' host function which records
// how many times the host implementation has actually been entered.
func hostCallCountingEnv(t testing.TB, calls *int) *Env {
	t.Helper()
	return testEnv(t,
		Function("pos",
			Overload("pos_int", []*Type{IntType}, BoolType,
				UnaryBinding(func(v ref.Val) ref.Val {
					*calls++
					return types.Bool(v.(types.Int) > 0)
				}))))
}

// TestHostCallLimitExceeded evaluates an 'all' macro over a three-element list with a host
// call budget of two. The macro iterations share the per-evaluation budget, so the third
// iteration must be rejected before the host function is entered. Each independent Eval
// restarts the budget.
func TestHostCallLimitExceeded(t *testing.T) {
	calls := 0
	env := hostCallCountingEnv(t, &calls)
	ast, iss := env.Compile(`[1, 2, 3].all(x, pos(x))`)
	if iss.Err() != nil {
		t.Fatalf("env.Compile() failed: %v", iss.Err())
	}
	prg, err := env.Program(ast, HostCallLimit(2))
	if err != nil {
		t.Fatalf("env.Program() failed: %v", err)
	}
	for i := 0; i < 2; i++ {
		out, _, err := prg.Eval(NoVars())
		if err == nil || !strings.Contains(err.Error(), "host call limit exceeded") {
			t.Fatalf("prg.Eval() got (%v, %v), wanted 'host call limit exceeded' error", out, err)
		}
		if calls != 2 {
			t.Fatalf("prg.Eval() entered the host function %d times, wanted 2", calls)
		}
		calls = 0
	}
}

// TestHostCallLimitShortCircuit verifies that calls which are short-circuited and never
// executed are not charged against the host call budget.
func TestHostCallLimitShortCircuit(t *testing.T) {
	calls := 0
	env := hostCallCountingEnv(t, &calls)
	ast, iss := env.Compile(`false && pos(1)`)
	if iss.Err() != nil {
		t.Fatalf("env.Compile() failed: %v", iss.Err())
	}
	prg, err := env.Program(ast, HostCallLimit(1))
	if err != nil {
		t.Fatalf("env.Program() failed: %v", err)
	}
	out, _, err := prg.Eval(NoVars())
	if err != nil {
		t.Fatalf("prg.Eval() failed: %v", err)
	}
	if out.Equal(types.False) != types.True {
		t.Fatalf("prg.Eval() got %v, wanted false", out)
	}
	if calls != 0 {
		t.Fatalf("short-circuited host call charged %d times, wanted 0", calls)
	}
}

// TestHostCallLimitWithinBudget verifies that evaluations at or under the budget proceed
// normally.
func TestHostCallLimitWithinBudget(t *testing.T) {
	calls := 0
	env := hostCallCountingEnv(t, &calls)
	ast, iss := env.Compile(`[1, 2].all(x, pos(x))`)
	if iss.Err() != nil {
		t.Fatalf("env.Compile() failed: %v", iss.Err())
	}
	prg, err := env.Program(ast, HostCallLimit(2))
	if err != nil {
		t.Fatalf("env.Program() failed: %v", err)
	}
	out, _, err := prg.Eval(NoVars())
	if err != nil {
		t.Fatalf("prg.Eval() failed: %v", err)
	}
	if out.Equal(types.True) != types.True {
		t.Fatalf("prg.Eval() got %v, wanted true", out)
	}
	if calls != 2 {
		t.Fatalf("prg.Eval() entered the host function %d times, wanted 2", calls)
	}
}
