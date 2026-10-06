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
	"fmt"
	"log"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

// ExampleHostCallLimit limits the number of host function calls a single evaluation may
// perform. Iterating the three-element list requires three calls to 'pos', but the budget
// of two rejects the third call before the host function is entered.
func ExampleHostCallLimit() {
	env, err := cel.NewEnv(
		cel.Function("pos",
			cel.Overload("pos_int", []*cel.Type{cel.IntType}, cel.BoolType,
				cel.UnaryBinding(func(v ref.Val) ref.Val {
					return types.Bool(v.(types.Int) > 0)
				}))))
	if err != nil {
		log.Fatalf("cel.NewEnv() failed: %v", err)
	}
	ast, iss := env.Compile(`[1, 2, 3].all(x, pos(x))`)
	if iss.Err() != nil {
		log.Fatalf("env.Compile() failed: %v", iss.Err())
	}
	prg, err := env.Program(ast, cel.HostCallLimit(2))
	if err != nil {
		log.Fatalf("env.Program() failed: %v", err)
	}
	_, _, err = prg.Eval(cel.NoVars())
	fmt.Println(err)
	// Output: host call limit exceeded
}
