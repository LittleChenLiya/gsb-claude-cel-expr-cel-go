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
	"sort"
	"strings"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

// PartialOutcome represents the result of a restricted partial evaluation.
//
// The outcome is either a concrete Value, or a set of unknown attribute
// dependency paths which prevented full evaluation. Unknown values are never
// coerced to false: when an unknown attribute remains live in a boolean
// short-circuit expression, the outcome reports the unknown dependencies
// instead of a boolean result.
type PartialOutcome struct {
	// Value is the concrete evaluation result. It is nil when unknown
	// attribute dependencies remain.
	Value ref.Val

	// Unknowns lists the attribute paths (e.g. "request.user.id") that the
	// result depends on. It is empty when Value is concrete.
	Unknowns []string
}

// Resolved indicates whether the evaluation produced a concrete value.
func (o *PartialOutcome) Resolved() bool {
	return o.Value != nil
}

// PartialEval compiles expr and performs a restricted partial evaluation with
// the known vars and the unknown attribute paths listed in unknownAttrs.
//
// See PartialEvalAst for the evaluation semantics.
func (e *Env) PartialEval(expr string, vars map[string]any, unknownAttrs ...string) (*PartialOutcome, error) {
	ast, iss := e.Compile(expr)
	if iss.Err() != nil {
		return nil, iss.Err()
	}
	return e.PartialEvalAst(ast, vars, unknownAttrs...)
}

// PartialEvalAst performs a restricted partial evaluation of a parsed or
// checked AST against the known vars, treating the attribute paths in
// unknownAttrs (e.g. "request.user.id") as unknown.
//
// The expression is evaluated exactly once, so host functions are invoked at
// most once per call site. Only boolean short-circuiting and attribute reads
// are relied upon: if the result still depends on unknown attributes, the
// outcome reports the unknown attribute dependency paths rather than a
// residual AST, and unknowns are never treated as false. Normal Program.Eval
// behavior is unaffected by this entry point.
func (e *Env) PartialEvalAst(ast *Ast, vars map[string]any, unknownAttrs ...string) (*PartialOutcome, error) {
	patterns := make([]*AttributePatternType, len(unknownAttrs))
	for i, path := range unknownAttrs {
		patterns[i] = unknownAttributePattern(path)
	}
	if vars == nil {
		vars = map[string]any{}
	}
	act, err := PartialVars(vars, patterns...)
	if err != nil {
		return nil, err
	}
	prg, err := e.Program(ast, EvalOptions(OptPartialEval))
	if err != nil {
		return nil, err
	}
	// Evaluate exactly once; no residual AST is generated and host functions
	// are not re-invoked.
	out, _, err := prg.Eval(act)
	if err != nil {
		return nil, err
	}
	if unk, ok := out.(*types.Unknown); ok {
		return &PartialOutcome{Unknowns: unknownPaths(unk)}, nil
	}
	return &PartialOutcome{Value: out}, nil
}

// unknownAttributePattern converts a dotted attribute path such as
// "request.user.id" into an AttributePatternType.
func unknownAttributePattern(path string) *AttributePatternType {
	parts := strings.Split(path, ".")
	pat := AttributePattern(parts[0])
	for _, field := range parts[1:] {
		pat = pat.QualString(field)
	}
	return pat
}

// unknownPaths flattens the attribute trails of an Unknown into a sorted,
// deduplicated list of attribute path strings.
func unknownPaths(unk *types.Unknown) []string {
	seen := map[string]bool{}
	paths := []string{}
	for _, id := range unk.IDs() {
		trails, found := unk.GetAttributeTrails(id)
		if !found {
			continue
		}
		for _, t := range trails {
			p := t.String()
			if !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	sort.Strings(paths)
	return paths
}
