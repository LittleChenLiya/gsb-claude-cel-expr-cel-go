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

package interpreter

import (
	"errors"
	"testing"

	"cel.dev/cel-go/checker"
	"cel.dev/cel-go/common"
	"cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/containers"
	"cel.dev/cel-go/common/decls"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/parser"
)

// errBoom is the sentinel host error returned by the failing test function.
var errBoom = errors.New("boom")

// failCounter counts host function invocations across a single evaluation.
type failCounter struct {
	calls int
}

// decl returns a fail(int) -> dyn function declaration whose host binding
// increments the call count and always returns the errBoom sentinel error.
// The dyn return type lets the function appear within logical, conditional,
// and arithmetic expressions in the tests below.
func (c *failCounter) decl(t *testing.T) *decls.FunctionDecl {
	t.Helper()
	return funcDecl(t, "fail",
		decls.Overload("fail_int",
			[]*types.Type{types.IntType}, types.DynType,
			decls.UnaryBinding(func(arg ref.Val) ref.Val {
				c.calls++
				return types.WrapErr(errBoom)
			})))
}

// callExprIDs collects the AST node ids of call expressions by function name.
type callExprIDs map[string][]int64

// VisitExpr records call expression ids keyed by function name.
func (c callExprIDs) VisitExpr(e ast.Expr) {
	if e.Kind() == ast.CallKind {
		call := e.AsCall()
		c[call.FunctionName()] = append(c[call.FunctionName()], e.ID())
	}
}

// VisitEntryExpr implements the ast.Visitor interface.
func (c callExprIDs) VisitEntryExpr(e ast.EntryExpr) {}

// findCallID returns the id of the single call expression for the function.
func (c callExprIDs) findCallID(t *testing.T, function string) int64 {
	t.Helper()
	ids := c[function]
	if len(ids) != 1 {
		t.Fatalf("got %d call expressions for %q, wanted exactly one", len(ids), function)
	}
	return ids[0]
}

// newFuncErrProgram parses, checks, and plans the expression with the
// provided host functions registered through the standard declaration and
// dispatcher flow.
func newFuncErrProgram(t *testing.T, expr string, funcs ...*decls.FunctionDecl) (*ast.AST, InterpretableV2, *ExecutionFrame) {
	t.Helper()
	cont := containers.DefaultContainer
	reg := newTestRegistry(t)
	env := newTestEnv(t, cont, reg)
	if err := env.AddFunctions(funcs...); err != nil {
		t.Fatalf("env.AddFunctions() failed: %v", err)
	}
	disp := NewDispatcher()
	addFunctionBindings(t, disp)
	if err := disp.Add(funcBindings(t, funcs...)...); err != nil {
		t.Fatalf("dispatcher.Add() failed: %v", err)
	}
	attrs := NewAttributeFactory(cont, reg, reg)
	interp := NewInterpreter(disp, cont, reg, reg, attrs)

	src := common.NewTextSource(expr)
	p, err := parser.NewParser(parser.Macros(parser.AllMacros...))
	if err != nil {
		t.Fatalf("parser.NewParser() failed: %v", err)
	}
	parsed, errs := p.Parse(src)
	if len(errs.GetErrors()) != 0 {
		t.Fatalf("Parse(%q) failed: %v", expr, errs.ToDisplayString())
	}
	checked, errs := checker.Check(parsed, src, env)
	if len(errs.GetErrors()) != 0 {
		t.Fatalf("Check(%q) failed: %v", expr, errs.ToDisplayString())
	}
	prg, err := interp.NewInterpretable(checked)
	if err != nil {
		t.Fatalf("NewInterpretable() failed: %v", err)
	}
	frame, err := NewExecutionFrame(EmptyActivation())
	if err != nil {
		t.Fatalf("NewExecutionFrame() failed: %v", err)
	}
	return checked, prg, frame
}

// evalFuncErr evaluates the expression with a failing host function and
// returns the checked AST, the resulting value, and the call counter.
func evalFuncErr(t *testing.T, expr string) (*ast.AST, ref.Val, *failCounter) {
	t.Helper()
	counter := &failCounter{}
	checked, prg, frame := newFuncErrProgram(t, expr, counter.decl(t))
	return checked, prg.Exec(frame), counter
}

// mustFunctionCallErr asserts that the value is a CEL error carrying
// FunctionCallError metadata for the given function and call expression id,
// with the errBoom sentinel reachable via errors.Is, and that the expression
// id resolves to the expected source position.
func mustFunctionCallErr(t *testing.T, checked *ast.AST, out ref.Val, function string, callID int64, wantLine, wantCol int) {
	t.Helper()
	// The failure must surface as a CEL error value, not a normal value.
	if !types.IsError(out) {
		t.Fatalf("got %v (%T), wanted CEL error", out, out)
	}
	err, ok := out.(error)
	if !ok {
		t.Fatalf("got %v (%T), wanted error", out, out)
	}
	// The original host error must remain inspectable via errors.Is.
	if !errors.Is(err, errBoom) {
		t.Errorf("errors.Is(%v, errBoom) = false, wanted true", err)
	}
	// The function name and call expression id must be preserved.
	var fnErr FunctionCallError
	if !errors.As(err, &fnErr) {
		t.Fatalf("errors.As(%v, FunctionCallError) = false, wanted true", err)
	}
	if fnErr.FunctionName() != function {
		t.Errorf("FunctionName() = %q, wanted %q", fnErr.FunctionName(), function)
	}
	if fnErr.ExprID() != callID {
		t.Errorf("ExprID() = %d, wanted %d", fnErr.ExprID(), callID)
	}
	// The wrapped error remains a *types.Err labeled with the call node id.
	celErr, ok := out.(*types.Err)
	if !ok {
		t.Fatalf("got %T, wanted *types.Err", out)
	}
	if celErr.NodeID() != callID {
		t.Errorf("NodeID() = %d, wanted %d", celErr.NodeID(), callID)
	}
	// The call expression id must resolve to the call site source position,
	// which the parser records at the open parenthesis of the call.
	loc := checked.SourceInfo().GetStartLocation(fnErr.ExprID())
	if loc.Line() != wantLine || loc.Column() != wantCol {
		t.Errorf("GetStartLocation(%d) = (%d, %d), wanted (%d, %d)",
			fnErr.ExprID(), loc.Line(), loc.Column(), wantLine, wantCol)
	}
	t.Logf("function %q failed at line %d, col %d (expr id %d): %v",
		fnErr.FunctionName(), loc.Line(), loc.Column(), fnErr.ExprID(), err)
}

// TestFunctionCallErrorDirectCall verifies that a host function error is
// wrapped with the function name and call expression position, and that the
// original error remains reachable via errors.Is.
func TestFunctionCallErrorDirectCall(t *testing.T) {
	checked, out, counter := evalFuncErr(t, `fail(1)`)
	if counter.calls != 1 {
		t.Errorf("fail() called %d times, wanted 1", counter.calls)
	}
	ids := callExprIDs{}
	ast.PostOrderVisit(checked.Expr(), ids)
	mustFunctionCallErr(t, checked, out, "fail", ids.findCallID(t, "fail"), 1, 4)
}

// TestFunctionCallErrorShortCircuit verifies that host functions on untaken
// logical branches are never invoked and produce no error.
func TestFunctionCallErrorShortCircuit(t *testing.T) {
	tests := []struct {
		expr string
		out  ref.Val
	}{
		{expr: `false && fail(1)`, out: types.False},
		{expr: `true || fail(1)`, out: types.True},
		{expr: `true ? 42 : fail(1)`, out: types.Int(42)},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			_, out, counter := evalFuncErr(t, tc.expr)
			// The short-circuited branch must not invoke the host function.
			if counter.calls != 0 {
				t.Errorf("fail() called %d times, wanted 0", counter.calls)
			}
			// The untaken branch must not produce an error.
			if types.IsError(out) {
				t.Errorf("got error %v, wanted %v", out, tc.out)
			} else if out.Equal(tc.out) != types.True {
				t.Errorf("got %v, wanted %v", out, tc.out)
			}
		})
	}
}

// TestFunctionCallErrorNestedCall verifies that host function errors
// propagate through nested calls and operators, retaining the innermost call
// site and suppressing outer host function invocations.
func TestFunctionCallErrorNestedCall(t *testing.T) {
	t.Run("nested_argument", func(t *testing.T) {
		checked, out, counter := evalFuncErr(t, `fail(fail(1))`)
		// The inner call fails; strict evaluation must not invoke the outer call.
		if counter.calls != 1 {
			t.Errorf("fail() called %d times, wanted 1", counter.calls)
		}
		// The error must be labeled with the innermost call expression id.
		innerID := checked.Expr().AsCall().Args()[0].ID()
		mustFunctionCallErr(t, checked, out, "fail", innerID, 1, 9)
	})
	t.Run("nested_operator", func(t *testing.T) {
		checked, out, counter := evalFuncErr(t, `1 + fail(1)`)
		if counter.calls != 1 {
			t.Errorf("fail() called %d times, wanted 1", counter.calls)
		}
		ids := callExprIDs{}
		ast.PostOrderVisit(checked.Expr(), ids)
		mustFunctionCallErr(t, checked, out, "fail", ids.findCallID(t, "fail"), 1, 8)
	})
}

// TestWrapFunctionErr verifies the wrapping helper in isolation: non-error
// values pass through, errors gain function call metadata exactly once, and
// the original error remains reachable via errors.Is.
func TestWrapFunctionErr(t *testing.T) {
	// Non-error values pass through unaltered.
	if got := wrapFunctionErr(1, "fail", types.True); got != types.True {
		t.Errorf("wrapFunctionErr(types.True) = %v, wanted types.True", got)
	}
	// Errors are wrapped with function call metadata and remain CEL errors.
	wrapped := wrapFunctionErr(42, "fail", types.WrapErr(errBoom))
	if !types.IsError(wrapped) {
		t.Fatalf("wrapFunctionErr(err) = %v (%T), wanted CEL error", wrapped, wrapped)
	}
	err := wrapped.(error)
	if !errors.Is(err, errBoom) {
		t.Errorf("errors.Is(%v, errBoom) = false, wanted true", err)
	}
	var fnErr FunctionCallError
	if !errors.As(err, &fnErr) {
		t.Fatalf("errors.As(%v, FunctionCallError) = false, wanted true", err)
	}
	if fnErr.FunctionName() != "fail" || fnErr.ExprID() != 42 {
		t.Errorf("got (%q, %d), wanted (%q, %d)", fnErr.FunctionName(), fnErr.ExprID(), "fail", 42)
	}
	// The original error message is preserved.
	if err.Error() != errBoom.Error() {
		t.Errorf("Error() = %q, wanted %q", err.Error(), errBoom.Error())
	}
	// Already labeled errors are not re-wrapped, preserving the innermost site.
	rewrapped := wrapFunctionErr(7, "outer", wrapped)
	var reFnErr FunctionCallError
	if !errors.As(rewrapped.(error), &reFnErr) {
		t.Fatalf("errors.As(%v, FunctionCallError) = false, wanted true", rewrapped)
	}
	if reFnErr.FunctionName() != "fail" || reFnErr.ExprID() != 42 {
		t.Errorf("got (%q, %d), wanted innermost (%q, %d)", reFnErr.FunctionName(), reFnErr.ExprID(), "fail", 42)
	}
}
