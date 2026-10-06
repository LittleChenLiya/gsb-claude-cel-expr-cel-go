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
	"reflect"
	"testing"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

func partialEvalTestEnv(t *testing.T, opts ...EnvOption) *Env {
	t.Helper()
	base := []EnvOption{
		Variable("known", BoolType),
		Variable("request", MapType(StringType, DynType)),
	}
	env, err := NewEnv(append(base, opts...)...)
	if err != nil {
		t.Fatalf("NewEnv() failed: %v", err)
	}
	return env
}

// true || unknown evaluates to true with no residual dependencies.
func TestPartialEvalTrueOrUnknown(t *testing.T) {
	env := partialEvalTestEnv(t)
	out, err := env.PartialEval("true || request.x", map[string]any{}, "request.x")
	if err != nil {
		t.Fatalf("PartialEval() failed: %v", err)
	}
	if !out.Resolved() {
		t.Fatalf("PartialEval() got unknowns %v, wanted concrete true", out.Unknowns)
	}
	if out.Value != types.True {
		t.Errorf("PartialEval() got %v, wanted true", out.Value)
	}
	if len(out.Unknowns) != 0 {
		t.Errorf("PartialEval() got dependencies %v, wanted none", out.Unknowns)
	}
}

// known && unknown keeps the unknown attribute path instead of treating the
// unknown as false.
func TestPartialEvalKnownAndUnknown(t *testing.T) {
	env := partialEvalTestEnv(t)
	out, err := env.PartialEval("known && request.x", map[string]any{"known": true}, "request.x")
	if err != nil {
		t.Fatalf("PartialEval() failed: %v", err)
	}
	if out.Resolved() {
		t.Fatalf("PartialEval() got concrete value %v, wanted unknown dependency on request.x", out.Value)
	}
	if !reflect.DeepEqual(out.Unknowns, []string{"request.x"}) {
		t.Errorf("PartialEval() got dependencies %v, wanted [request.x]", out.Unknowns)
	}
}

// A host function is invoked at most once during partial evaluation, even
// when the overall result remains unknown.
func TestPartialEvalHostFunctionCalledOnce(t *testing.T) {
	calls := 0
	env := partialEvalTestEnv(t,
		Function("host",
			Overload("host_int",
				[]*Type{IntType},
				BoolType,
				UnaryBinding(func(v ref.Val) ref.Val {
					calls++
					return types.Bool(v.(types.Int) > 0)
				}))))
	out, err := env.PartialEval("host(1) && request.x", map[string]any{}, "request.x")
	if err != nil {
		t.Fatalf("PartialEval() failed: %v", err)
	}
	if calls > 1 {
		t.Errorf("host function called %d times, wanted at most 1", calls)
	}
	if !reflect.DeepEqual(out.Unknowns, []string{"request.x"}) {
		t.Errorf("PartialEval() got dependencies %v, wanted [request.x]", out.Unknowns)
	}
}
