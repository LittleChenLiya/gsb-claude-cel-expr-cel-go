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

func TestPartialEval(t *testing.T) {
	hostCalls := 0
	env, err := NewEnv(
		Variable("known", BoolType),
		Variable("request", MapType(StringType, DynType)),
		Function("once",
			Overload("once_bool",
				[]*Type{BoolType}, BoolType,
				UnaryBinding(func(arg ref.Val) ref.Val {
					hostCalls++
					return arg
				}))),
	)
	if err != nil {
		t.Fatalf("NewEnv() failed: %v", err)
	}
	vars := map[string]any{"known": true}
	tests := []struct {
		name         string
		expr         string
		wantValue    ref.Val
		wantUnknowns []string
		wantCalls    int
	}{
		{
			name:      "true or unknown short-circuits with no dependencies",
			expr:      `true || request.x`,
			wantValue: types.True,
		},
		{
			name:         "known and unknown retains the attribute path",
			expr:         `known && request.x`,
			wantUnknowns: []string{"request.x"},
		},
		{
			name:         "host function called at most once",
			expr:         `once(true) && request.x`,
			wantUnknowns: []string{"request.x"},
			wantCalls:    1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ast, iss := env.Compile(tc.expr)
			if iss.Err() != nil {
				t.Fatalf("Compile(%q) failed: %v", tc.expr, iss.Err())
			}
			before := hostCalls
			res, err := env.PartialEval(ast, vars)
			if err != nil {
				t.Fatalf("PartialEval(%q) failed: %v", tc.expr, err)
			}
			if tc.wantValue != nil {
				if !res.Resolved() {
					t.Fatalf("PartialEval(%q) got unknowns %v, wanted value %v", tc.expr, res.Unknowns, tc.wantValue)
				}
				if res.Value.Equal(tc.wantValue) != types.True {
					t.Errorf("PartialEval(%q) got %v, wanted %v", tc.expr, res.Value, tc.wantValue)
				}
				if len(res.Unknowns) != 0 {
					t.Errorf("PartialEval(%q) got unknowns %v, wanted none", tc.expr, res.Unknowns)
				}
			} else {
				if res.Resolved() {
					t.Fatalf("PartialEval(%q) got value %v, wanted unknowns %v", tc.expr, res.Value, tc.wantUnknowns)
				}
				if !reflect.DeepEqual(res.Unknowns, tc.wantUnknowns) {
					t.Errorf("PartialEval(%q) got unknowns %v, wanted %v", tc.expr, res.Unknowns, tc.wantUnknowns)
				}
			}
			if got := hostCalls - before; got > tc.wantCalls {
				t.Errorf("PartialEval(%q) invoked host function %d times, wanted at most %d", tc.expr, got, tc.wantCalls)
			}
		})
	}
}

func TestPartialEvalUnsupported(t *testing.T) {
	env, err := NewEnv(
		Variable("known", BoolType),
		Variable("request", MapType(StringType, DynType)),
	)
	if err != nil {
		t.Fatalf("NewEnv() failed: %v", err)
	}
	unsupported := []string{
		`known == true`,
		`request.x == request.y`,
		`1 + 2 > 0`,
		`has(request.x)`,
		`known ? true : false`,
		`[1, 2, 3].exists(i, i > 1)`,
	}
	for _, expr := range unsupported {
		ast, iss := env.Compile(expr)
		if iss.Err() != nil {
			t.Fatalf("Compile(%q) failed: %v", expr, iss.Err())
		}
		if _, err := env.PartialEval(ast, map[string]any{"known": true}); err == nil {
			t.Errorf("PartialEval(%q) succeeded, wanted unsupported expression error", expr)
		}
	}
}

func TestPartialEvalUnknownNotFalse(t *testing.T) {
	env, err := NewEnv(
		Variable("request", MapType(StringType, DynType)),
	)
	if err != nil {
		t.Fatalf("NewEnv() failed: %v", err)
	}
	// An unknown attribute in a boolean context must surface as an unknown
	// dependency rather than being treated as false.
	ast, iss := env.Compile(`!request.enabled || request.x`)
	if iss.Err() != nil {
		t.Fatalf("Compile() failed: %v", iss.Err())
	}
	res, err := env.PartialEval(ast, map[string]any{})
	if err != nil {
		t.Fatalf("PartialEval() failed: %v", err)
	}
	if res.Resolved() {
		t.Fatalf("PartialEval() got value %v, wanted unknown dependencies", res.Value)
	}
	want := []string{"request.enabled", "request.x"}
	if !reflect.DeepEqual(res.Unknowns, want) {
		t.Errorf("PartialEval() got unknowns %v, wanted %v", res.Unknowns, want)
	}
}
