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
	"sort"
	"strings"

	"cel.dev/cel-go/common/decls"
	"cel.dev/cel-go/interpreter"
)

// InputBindingError describes a mismatch between the root variables declared in an environment
// and the root variable keys supplied with an evaluation input.
//
// Missing lists the declared root variables which were not supplied. Extra lists the supplied
// root keys which do not correspond to any declared root variable. Both slices are sorted and
// contain no duplicates.
//
// A key explicitly bound to a Go nil value is supplied and is therefore never reported as
// missing: nil is a valid CEL null value and is distinct from leaving the variable unprovided.
type InputBindingError struct {
	Missing []string
	Extra   []string
}

// Error implements the error interface.
func (e *InputBindingError) Error() string {
	var msgs []string
	if len(e.Missing) > 0 {
		msgs = append(msgs, fmt.Sprintf("missing declared input variables: %s", strings.Join(e.Missing, ", ")))
	}
	if len(e.Extra) > 0 {
		msgs = append(msgs, fmt.Sprintf("unexpected input variables: %s", strings.Join(e.Extra, ", ")))
	}
	return strings.Join(msgs, "; ")
}

// InputBindingOption configures how input bindings are checked against the environment's
// declared variable set.
type InputBindingOption func(*inputBindingConfig)

type inputBindingConfig struct {
	// allowExtraVariables permits input keys which do not match any declared root variable.
	allowExtraVariables bool
}

// AllowExtraVariables configures an input binding check to reject only missing declared
// variables and ignore any additional keys supplied with the input.
//
// By default, both missing variables and unexpected keys are reported.
func AllowExtraVariables() InputBindingOption {
	return func(cfg *inputBindingConfig) {
		cfg.allowExtraVariables = true
	}
}

// CheckInputBindings compares the root variable keys supplied in the input with the root
// variables declared in the environment and returns an *InputBindingError when the sets do not
// match.
//
// The input may be any value accepted by Program.Eval: a map[string]any or an Activation. The
// check is based solely on the environment's compiled declaration set and the binding keys: it
// never accesses binding values, resolves object properties, or executes the expression. In
// particular lazy bindings (func() any / func() ref.Val) are not invoked and a key bound to an
// explicit nil is treated as provided rather than missing.
//
// The AllowExtraVariables option restricts the check to missing variables only.
func (e *Env) CheckInputBindings(input any, opts ...InputBindingOption) error {
	names, err := inputBindingNames(input)
	if err != nil {
		return err
	}
	return checkInputBindings(e.Variables(), names, opts)
}

// inputBindingNames returns the set of root variable keys supplied with an evaluation input
// without resolving a single binding value.
func inputBindingNames(input any) (map[string]bool, error) {
	switch v := input.(type) {
	case map[string]any:
		names := make(map[string]bool, len(v))
		for name := range v {
			names[name] = true
		}
		return names, nil
	case interpreter.Activation:
		names, complete := interpreter.RootBindingNames(v)
		if !complete {
			return nil, fmt.Errorf(
				"input bindings of type %T cannot enumerate their variable names: "+
					"implement interpreter.BindingNamesActivation to enable input binding checks",
				input)
		}
		return names, nil
	case nil:
		return nil, fmt.Errorf("bindings must be non-nil")
	default:
		return nil, fmt.Errorf(
			"activation input must be an activation or map[string]interface: got %T", input)
	}
}

// checkInputBindings compares the supplied root binding names against the declared variable
// names. Binding keys use the same fully qualified names as the declarations, so comparison is
// by exact name: a declared variable "a" is satisfied exactly by the supplied key "a", and a
// supplied key "a.b" does not satisfy it (property access is never inspected by the check).
func checkInputBindings(declared []*decls.VariableDecl, supplied map[string]bool, opts []InputBindingOption) error {
	cfg := &inputBindingConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	var missing []string
	declaredNames := make(map[string]bool, len(declared))
	for _, v := range declared {
		name := v.Name()
		if name == "" {
			continue
		}
		declaredNames[name] = true
		if !supplied[name] {
			missing = append(missing, name)
		}
	}

	var extra []string
	if !cfg.allowExtraVariables {
		for name := range supplied {
			if !declaredNames[name] {
				extra = append(extra, name)
			}
		}
	}

	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) == 0 && len(extra) == 0 {
		return nil
	}
	return &InputBindingError{Missing: missing, Extra: extra}
}
