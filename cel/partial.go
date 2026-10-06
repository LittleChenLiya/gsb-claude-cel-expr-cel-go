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

	"cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/operators"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

// PartialResult holds the outcome of a restricted partial evaluation.
//
// When the expression is fully determined by the known inputs, Value holds the
// final result and Unknowns is empty. Otherwise Value is nil and Unknowns
// lists the attribute paths (e.g. "request.x") which the result still depends
// on. Unknown values are never coerced to false; boolean short-circuit
// operators only collapse to a final value when the CEL semantics allow it
// (e.g. `true || unknown` is `true`).
type PartialResult struct {
	// Value is the final evaluation result, or nil when unknown attribute
	// dependencies remain.
	Value ref.Val

	// Unknowns is the sorted, de-duplicated set of attribute paths the result
	// depends on. It is empty when Value is non-nil.
	Unknowns []string
}

// Resolved reports whether the expression evaluated to a final value with no
// remaining unknown attribute dependencies.
func (r *PartialResult) Resolved() bool {
	return r != nil && r.Value != nil
}

// PartialEval performs a restricted partial evaluation of the expression
// against the known inputs in vars.
//
// Variables declared in the Env but absent from vars are treated as unknown
// attributes. The evaluation is deliberately limited to a small expression
// subset:
//
//   - boolean short-circuit operators: &&, ||, !
//   - attribute reads: a.b.c
//   - literals and identifiers
//   - host function calls, each invoked at most once during the single
//     evaluation pass
//
// Any other construct (comparisons, arithmetic, indexing, comprehensions,
// presence tests, etc.) is rejected with an error. Unlike ResidualAst, no
// residual AST is generated; the outcome is either a final value or the set
// of unknown attribute paths blocking full evaluation. The regular Program
// and Eval behavior is unaffected by this entry point.
func (e *Env) PartialEval(a *Ast, vars any) (*PartialResult, error) {
	if a == nil {
		return nil, fmt.Errorf("partial eval: ast is nil")
	}
	expr := a.NativeRep().Expr()
	if err := checkPartialExpr(expr); err != nil {
		return nil, err
	}
	prg, err := e.Program(a, EvalOptions(OptPartialEval))
	if err != nil {
		return nil, fmt.Errorf("partial eval: %w", err)
	}
	act, err := e.PartialVars(vars)
	if err != nil {
		return nil, fmt.Errorf("partial eval: %w", err)
	}
	// A single evaluation pass guarantees every host function call site is
	// invoked at most once.
	out, _, err := prg.Eval(act)
	if err != nil {
		return nil, fmt.Errorf("partial eval: %w", err)
	}
	if unk, ok := out.(*types.Unknown); ok {
		return &PartialResult{Unknowns: partialUnknownPaths(expr, unk)}, nil
	}
	return &PartialResult{Value: out}, nil
}

// checkPartialExpr verifies the expression only uses the restricted subset
// supported by PartialEval.
func checkPartialExpr(e ast.Expr) error {
	switch e.Kind() {
	case ast.LiteralKind, ast.IdentKind:
		return nil
	case ast.SelectKind:
		sel := e.AsSelect()
		if sel.IsTestOnly() {
			return fmt.Errorf("partial eval: presence tests are not supported")
		}
		return checkPartialExpr(sel.Operand())
	case ast.CallKind:
		call := e.AsCall()
		fn := call.FunctionName()
		switch fn {
		case operators.LogicalAnd, operators.LogicalOr, operators.LogicalNot:
			// Boolean short-circuit operators are supported.
		default:
			if _, isOperator := operators.FindReverse(fn); isOperator {
				return fmt.Errorf("partial eval: operator %q is not supported", fn)
			}
			// Any other call targets a host function which is invoked at
			// most once during the single evaluation pass.
		}
		if call.IsMemberFunction() {
			if err := checkPartialExpr(call.Target()); err != nil {
				return err
			}
		}
		for _, arg := range call.Args() {
			if err := checkPartialExpr(arg); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("partial eval: expression kind %v is not supported", e.Kind())
	}
}

// partialUnknownPaths computes the sorted, de-duplicated set of attribute
// paths an unknown result depends on.
//
// Unknown attribute patterns declared at the variable level (e.g. `request`)
// only record the variable in the unknown attribute trail, so the full
// attribute paths (e.g. `request.x`) are recovered by matching the unknown
// root variables against the attribute reads present in the expression. In
// the restricted expression subset, an unknown result implies every attribute
// read rooted at an unknown variable was evaluated, since short-circuit
// operators only skip branches when the outcome is already fully determined.
func partialUnknownPaths(expr ast.Expr, unk *types.Unknown) []string {
	roots := make(map[string]bool)
	fallback := make(map[string]struct{})
	for _, id := range unk.IDs() {
		trails, found := unk.GetAttributeTrails(id)
		if !found {
			continue
		}
		for _, trail := range trails {
			fallback[trail.String()] = struct{}{}
			if v := trail.Variable(); v != "" {
				roots[v] = true
			}
		}
	}
	paths := make(map[string]struct{})
	collectAttributePaths(expr, roots, paths)
	if len(paths) == 0 {
		// The unknown did not originate from an attribute read present in the
		// expression (e.g. an unresolved host function call); fall back to the
		// raw attribute trails.
		paths = fallback
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	return sorted
}

// collectAttributePaths records the attribute paths rooted at the given
// unknown variables. Select chains are recorded as a single dotted path at
// the outermost select; bare identifiers are recorded by name.
func collectAttributePaths(e ast.Expr, roots map[string]bool, paths map[string]struct{}) {
	switch e.Kind() {
	case ast.IdentKind:
		if name := e.AsIdent(); roots[name] {
			paths[name] = struct{}{}
		}
	case ast.SelectKind:
		if root, path, ok := attributePath(e); ok && roots[root] {
			paths[path] = struct{}{}
		}
	case ast.CallKind:
		call := e.AsCall()
		if call.IsMemberFunction() {
			collectAttributePaths(call.Target(), roots, paths)
		}
		for _, arg := range call.Args() {
			collectAttributePaths(arg, roots, paths)
		}
	}
}

// attributePath flattens a select chain such as `a.b.c` into its root
// identifier `a` and its dotted path `a.b.c`.
func attributePath(e ast.Expr) (string, string, bool) {
	parts := []string{}
	for e.Kind() == ast.SelectKind {
		sel := e.AsSelect()
		parts = append([]string{sel.FieldName()}, parts...)
		e = sel.Operand()
	}
	if e.Kind() != ast.IdentKind {
		return "", "", false
	}
	root := e.AsIdent()
	return root, root + "." + strings.Join(parts, "."), true
}
