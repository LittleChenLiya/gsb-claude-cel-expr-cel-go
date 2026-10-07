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
	"errors"
	"fmt"
	"sort"
	"testing"
)

// TestInputBindingCheckDemo is an in-memory fixture demonstrating the input binding source check
// against the two-variable expression "a + b". It prints, for each scenario: the pre-check
// outcome, whether the lazy getter (evaluation proxy) ran, and the program result when bindings
// were accepted.
//
// Entry point:
//
//	go test ./cel/ -run TestInputBindingCheckDemo -v
func TestInputBindingCheckDemo(t *testing.T) {
	env := newTwoVarEnv(t)
	prg := mustProgram(t, env, "a + b", ValidateInputBindings())

	scenarios := []struct {
		name  string
		input map[string]any
	}{
		{"missing-b", map[string]any{"a": int64(40)}},
		{"explicit-null-a", map[string]any{"a": nil, "b": int64(2)}},
		{"extra-c", map[string]any{"a": int64(40), "b": int64(2), "c": int64(9)}},
		{"valid", map[string]any{"a": int64(40), "b": int64(2)}},
	}

	for _, sc := range scenarios {
		var getterRan bool
		if sc.name == "valid" {
			// Replace "a" with a lazy binding so the getter count proves evaluation did run.
			sc.input["a"] = func() any {
				getterRan = true
				return int64(40)
			}
		}
		out, _, err := prg.Eval(sc.input)
		result := "rejected"
		switch {
		case err == nil:
			result = fmt.Sprintf("evaluated => %v (lazy getter ran=%v)", out.Value(), getterRan)
		case isBindingErr(err):
			result = fmt.Sprintf("pre-check rejected: %v", err)
		default:
			result = fmt.Sprintf("evaluation error: %v", err)
		}
		t.Logf("[%-14s] input=%v -> %s", sc.name, keysOf(sc.input), result)
	}

	// Missing-only mode: extra keys allowed.
	prgMissingOnly := mustProgram(t, env, "a + b",
		ValidateInputBindings(AllowExtraVariables()))
	_, _, err := prgMissingOnly.Eval(map[string]any{"a": int64(7), "b": int64(8), "extra": true})
	t.Logf("[extra-allowed ] missing-only mode with extra key -> err=%v", err)
}

func isBindingErr(err error) bool {
	var ibe *InputBindingError
	return errors.As(err, &ibe)
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
