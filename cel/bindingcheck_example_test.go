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

package cel_test

import (
	"errors"
	"fmt"

	cel "cel.dev/cel-go/cel"
)

// ExampleCheckInputBindings shows the in-memory precheck fixture for a two-variable expression:
// missing keys, explicit null vs. not supplied, extra keys, and valid bindings still flowing
// through the normal Program evaluation.
func ExampleCheckInputBindings() {
	env, err := cel.NewEnv(
		cel.Variable("a", cel.StringType),
		cel.Variable("b", cel.StringType),
	)
	if err != nil {
		panic(err)
	}
	ast, iss := env.Compile("a + b")
	if iss.Err() != nil {
		panic(iss.Err())
	}

	report := func(label string, vars map[string]any) {
		check, err := env.CheckInputBindings(ast, vars)
		if err != nil {
			panic(err)
		}
		fmt.Printf("%-22s missing=%v null=%v extra=%v ok=%v\n",
			label, check.MissingVars, check.NullVars, check.ExtraVars, check.OK())
	}

	report("missing-b", map[string]any{"a": "x"})
	report("explicit-null-b", map[string]any{"a": "x", "b": nil})
	report("extra-c", map[string]any{"a": "x", "b": "y", "c": "z"})
	report("complete", map[string]any{"a": "foo", "b": "bar"})

	// Strict program: rejects before evaluation.
	strict, err := env.Program(ast,
		cel.CheckInputBindings(cel.RejectMissingBindings(), cel.RejectExtraBindings()))
	if err != nil {
		panic(err)
	}
	_, _, err = strict.Eval(map[string]any{"a": "x"})
	var bce *cel.BindingCheckError
	if errors.As(err, &bce) {
		fmt.Printf("strict rejected:       missing=%v extra=%v\n", bce.MissingVars, bce.ExtraVars)
	}

	// Valid bindings evaluate through the standard Program path.
	out, _, err := strict.Eval(map[string]any{"a": "foo", "b": "bar"})
	if err != nil {
		panic(err)
	}
	fmt.Printf("strict valid result:   %v\n", out.Value())

	// Default interface (no precheck option) keeps its original permissive behavior.
	plain, err := env.Program(ast)
	if err != nil {
		panic(err)
	}
	out, _, err = plain.Eval(map[string]any{"a": "foo", "b": "bar", "unused": 1})
	if err != nil {
		panic(err)
	}
	fmt.Printf("default regression:    %v\n", out.Value())

	// Output:
	// missing-b              missing=[b] null=[] extra=[] ok=false
	// explicit-null-b        missing=[] null=[b] extra=[] ok=true
	// extra-c                missing=[] null=[] extra=[c] ok=false
	// complete               missing=[] null=[] extra=[] ok=true
	// strict rejected:       missing=[b] extra=[]
	// strict valid result:   foobar
	// default regression:    foobar
}
