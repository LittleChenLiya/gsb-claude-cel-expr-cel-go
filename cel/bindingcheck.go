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
	"strings"

	"cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/interpreter"
)

// BindingCheck describes the result of comparing the root variable names supplied in an
// evaluation input against the variables referenced by a compiled expression and declared in the
// environment.
//
// The check is purely structural: it operates on the compiled declaration set and the keys of
// the input bindings. It never accesses object properties, resolves lazy bindings, or executes
// any part of an expression. A variable explicitly bound to nil is treated as supplied and is
// reported under NullVars, never under MissingVars.
type BindingCheck struct {
	// MissingVars lists root variables referenced by the expression but not present in the
	// input bindings (neither a concrete value nor an explicit nil).
	MissingVars []string

	// NullVars lists root variables referenced by the expression whose input binding is
	// explicitly present with a nil value. These are supplied, not missing.
	NullVars []string

	// ExtraVars lists root binding keys which are neither referenced by the expression nor
	// declared in the environment, and which therefore cannot affect the evaluation.
	ExtraVars []string
}

// OK reports whether the check found no missing and no extra variables. Explicitly nil bindings
// do not cause OK to return false.
func (c *BindingCheck) OK() bool {
	return c == nil || (len(c.MissingVars) == 0 && len(c.ExtraVars) == 0)
}

// BindingCheckError is returned when a binding precheck is configured to reject missing or extra
// root variables and the input bindings fail the check.
type BindingCheckError struct {
	// MissingVars are the root variables the expression references that were not supplied.
	MissingVars []string
	// ExtraVars are the supplied root binding keys that are neither used nor declared.
	ExtraVars []string
}

// Error implements the error interface.
func (e *BindingCheckError) Error() string {
	var msgs []string
	if len(e.MissingVars) > 0 {
		msgs = append(msgs, fmt.Sprintf("missing input bindings for variables: %s",
			strings.Join(e.MissingVars, ", ")))
	}
	if len(e.ExtraVars) > 0 {
		msgs = append(msgs, fmt.Sprintf("unexpected extra input bindings: %s",
			strings.Join(e.ExtraVars, ", ")))
	}
	return strings.Join(msgs, "; ")
}

// Is allows errors.Is to match binding-check errors with identical content.
func (e *BindingCheckError) Is(target error) bool {
	other, ok := target.(*BindingCheckError)
	if !ok {
		return false
	}
	return sameStringSet(e.MissingVars, other.MissingVars) &&
		sameStringSet(e.ExtraVars, other.ExtraVars)
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]struct{}{}
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		if _, found := set[s]; !found {
			return false
		}
	}
	return true
}

// BindingCheckOption configures the strictness of an input binding precheck.
type BindingCheckOption func(*bindingCheckConfig) error

type bindingCheckConfig struct {
	rejectMissing bool
	rejectExtra   bool
}

func newBindingCheckConfig(opts ...BindingCheckOption) (*bindingCheckConfig, error) {
	conf := &bindingCheckConfig{}
	for _, opt := range opts {
		if err := opt(conf); err != nil {
			return nil, err
		}
	}
	return conf, nil
}

// RejectMissingBindings configures a precheck to return an error when a root variable referenced
// by the expression is not present in the input bindings. Explicit nil bindings are still
// considered present.
func RejectMissingBindings() BindingCheckOption {
	return func(conf *bindingCheckConfig) error {
		conf.rejectMissing = true
		return nil
	}
}

// RejectExtraBindings configures a precheck to return an error when the input bindings contain a
// root key which is neither referenced by the expression nor declared in the environment.
func RejectExtraBindings() BindingCheckOption {
	return func(conf *bindingCheckConfig) error {
		conf.rejectExtra = true
		return nil
	}
}

// CheckInputBindings compares the root variable keys in the given bindings against the compiled
// declaration set represented by the Ast and its environment.
//
// The Ast must have been type-checked, since the referenced root variables are derived from the
// checked expression. The bindings argument may be a map[string]any or an Activation; in both
// cases only the root binding keys are inspected, so no lazy binding is evaluated and no
// expression code runs.
//
// Without options the method performs a report-only check: the returned BindingCheck lists the
// missing, explicitly-nil, and extra root variables and the error is nil. With
// RejectMissingBindings and/or RejectExtraBindings the method returns a *BindingCheckError for
// the rejected categories while still populating the BindingCheck.
func (e *Env) CheckInputBindings(a *Ast, bindings any, opts ...BindingCheckOption) (*BindingCheck, error) {
	if a == nil {
		return nil, errors.New("check input bindings: ast must be non-nil")
	}
	if !a.IsChecked() {
		return nil, errors.New("check input bindings: ast must be type-checked; compile the expression before checking bindings")
	}
	conf, err := newBindingCheckConfig(opts...)
	if err != nil {
		return nil, err
	}
	supplied, nilBound, err := suppliedBindingKeys(bindings)
	if err != nil {
		return nil, err
	}
	return e.checkBindingKeys(a.NativeRep(), supplied, nilBound, conf)
}

// checkBindingKeys computes the BindingCheck from raw binding-key sets and applies the rejection
// policy. The key sets are produced without evaluating any binding.
func (e *Env) checkBindingKeys(a *ast.AST, supplied, nilBound map[string]bool,
	conf *bindingCheckConfig) (*BindingCheck, error) {
	required := referencedRootVars(a)

	// candidateNames honors CEL namespace resolution: a variable reference recorded under one
	// qualified form may be supplied under any container-qualified form, in resolution order.
	candidateNames := func(name string) []string {
		if e.Container != nil {
			return e.Container.ResolveCandidateNames(name)
		}
		return []string{name}
	}

	// Expand every referenced name into all of its legal supply-key forms.
	requiredForms := map[string]bool{}
	candidatesOf := map[string][]string{}
	for name := range required {
		candidates := candidateNames(name)
		candidatesOf[name] = candidates
		for _, candidate := range candidates {
			requiredForms[candidate] = true
		}
	}
	boundUnder := func(name string) bool {
		for _, candidate := range candidatesOf[name] {
			if supplied[candidate] {
				return true
			}
		}
		return false
	}

	// declaredForms is the set of qualified supply-key forms which correspond to a variable
	// declared in the environment, whether or not the expression references it.
	declaredForms := map[string]bool{}
	for _, v := range e.Variables() {
		for _, candidate := range candidateNames(v.Name()) {
			declaredForms[candidate] = true
		}
	}

	check := &BindingCheck{}
	for name := range required {
		if !boundUnder(name) {
			check.MissingVars = append(check.MissingVars, name)
			continue
		}
		for _, candidate := range candidatesOf[name] {
			if nilBound[candidate] {
				check.NullVars = append(check.NullVars, name)
				break
			}
		}
	}
	for name := range supplied {
		if requiredForms[name] || declaredForms[name] {
			continue
		}
		check.ExtraVars = append(check.ExtraVars, name)
	}
	sort.Strings(check.MissingVars)
	sort.Strings(check.NullVars)
	sort.Strings(check.ExtraVars)

	if (conf.rejectMissing && len(check.MissingVars) > 0) ||
		(conf.rejectExtra && len(check.ExtraVars) > 0) {
		rejectErr := &BindingCheckError{}
		if conf.rejectMissing {
			rejectErr.MissingVars = append(rejectErr.MissingVars, check.MissingVars...)
		}
		if conf.rejectExtra {
			rejectErr.ExtraVars = append(rejectErr.ExtraVars, check.ExtraVars...)
		}
		return check, rejectErr
	}
	return check, nil
}

// suppliedBindingKeys extracts the root keys present in the bindings input along with the subset
// whose value is explicitly nil. No lazy binding is invoked and no expression is evaluated.
func suppliedBindingKeys(bindings any) (map[string]bool, map[string]bool, error) {
	switch v := bindings.(type) {
	case nil:
		return nil, nil, errors.New("check input bindings: bindings must be non-nil")
	case map[string]any:
		supplied := make(map[string]bool, len(v))
		nilBound := make(map[string]bool)
		for name, val := range v {
			supplied[name] = true
			if val == nil {
				nilBound[name] = true
			}
		}
		return supplied, nilBound, nil
	case interpreter.Activation:
		// Enumerate keys structurally so lazy bindings are never invoked and no expression code
		// runs; explicit nil bindings are reported separately from non-nil supplies.
		present, nilSet := interpreter.BindingKeySets(v)
		for name := range nilSet {
			present[name] = true
		}
		return present, nilSet, nil
	default:
		return nil, nil, fmt.Errorf(
			"check input bindings: bindings must be an activation or map[string]interface{}: got %T",
			bindings)
	}
}

// referencedRootVars walks the checked AST and returns the set of root variable names the
// expression references, keyed by the checker-resolved name (which may be a container-qualified
// name and matches the runtime's own "no such attribute" diagnostics). Iteration and
// accumulator variables introduced by comprehensions are locally bound and excluded, and
// identifiers compiled to inline constants (such as enum values) are ignored.
func referencedRootVars(a *ast.AST) map[string]bool {
	roots := map[string]bool{}
	refMap := a.ReferenceMap()
	var walk func(e ast.Expr, local map[string]bool)
	walk = func(e ast.Expr, local map[string]bool) {
		switch e.Kind() {
		case ast.IdentKind:
			name := strings.TrimPrefix(e.AsIdent(), ".")
			if local[name] {
				return
			}
			if info, found := refMap[e.ID()]; found {
				// Constants such as enum values are compiled to inline values, not inputs.
				if info.Value != nil {
					return
				}
				if info.Name != "" {
					name = strings.TrimPrefix(info.Name, ".")
				}
			}
			roots[name] = true
		case ast.SelectKind:
			walk(e.AsSelect().Operand(), local)
		case ast.CallKind:
			call := e.AsCall()
			if call.IsMemberFunction() {
				walk(call.Target(), local)
			}
			for _, arg := range call.Args() {
				walk(arg, local)
			}
		case ast.ListKind:
			for _, el := range e.AsList().Elements() {
				walk(el, local)
			}
		case ast.MapKind:
			for _, entry := range e.AsMap().Entries() {
				me := entry.AsMapEntry()
				walk(me.Key(), local)
				walk(me.Value(), local)
			}
		case ast.StructKind:
			for _, field := range e.AsStruct().Fields() {
				walk(field.AsStructField().Value(), local)
			}
		case ast.ComprehensionKind:
			comp := e.AsComprehension()
			// The iteration range is evaluated outside the comprehension's local scope.
			walk(comp.IterRange(), local)
			child := make(map[string]bool, len(local)+3)
			for k := range local {
				child[k] = true
			}
			child[comp.IterVar()] = true
			if comp.HasIterVar2() {
				child[comp.IterVar2()] = true
			}
			child[comp.AccuVar()] = true
			walk(comp.AccuInit(), child)
			walk(comp.LoopCondition(), child)
			walk(comp.LoopStep(), child)
			walk(comp.Result(), child)
		case ast.LiteralKind:
			// No variable references.
		}
	}
	walk(a.Expr(), map[string]bool{})
	return roots
}
