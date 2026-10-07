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
	"testing"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

// hostFuncFixture registers a failing function 'fail' and an identity function
// 'echo', both counting their invocations, and evaluates expressions against
// the environment.
type hostFuncFixture struct {
	env       *Env
	errBoom   error
	failCalls int
	echoCalls int
}

func newHostFuncFixture(t *testing.T) *hostFuncFixture {
	t.Helper()
	f := &hostFuncFixture{errBoom: errors.New("boom")}
	env, err := NewEnv(
		Function("fail",
			Overload("fail_int",
				[]*Type{IntType},
				IntType,
				UnaryBinding(func(arg ref.Val) ref.Val {
					f.failCalls++
					return types.WrapErr(f.errBoom)
				}))),
		Function("echo",
			Overload("echo_int",
				[]*Type{IntType},
				IntType,
				UnaryBinding(func(arg ref.Val) ref.Val {
					f.echoCalls++
					return arg
				}))),
	)
	if err != nil {
		t.Fatalf("NewEnv() failed: %v", err)
	}
	f.env = env
	return f
}

// eval compiles and evaluates the expression, returning the output, the
// compiled AST, and the evaluation error.
func (f *hostFuncFixture) eval(t *testing.T, expr string) (ref.Val, *Ast, error) {
	t.Helper()
	ast, iss := f.env.Compile(expr)
	if iss.Err() != nil {
		t.Fatalf("Compile(%q) failed: %v", expr, iss.Err())
	}
	prg, err := f.env.Program(ast)
	if err != nil {
		t.Fatalf("Program(%q) failed: %v", expr, err)
	}
	out, _, err := prg.Eval(NoVars())
	return out, ast, err
}

// hostFuncErr extracts the HostFuncError from the evaluation error and checks
// that the original cause is preserved for errors.Is.
func hostFuncErr(t *testing.T, err, cause error) types.HostFuncError {
	t.Helper()
	if err == nil {
		t.Fatal("Eval() error got nil, wanted host function error")
	}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(%v, %v) got false, wanted true", err, cause)
	}
	var hostErr types.HostFuncError
	if !errors.As(err, &hostErr) {
		t.Fatalf("errors.As(%v, types.HostFuncError) got false, wanted true", err)
	}
	return hostErr
}

func TestHostFuncErrorDirectCall(t *testing.T) {
	f := newHostFuncFixture(t)
	out, ast, err := f.eval(t, "fail(1)")
	// The failure must surface as a CEL error value, not a normal value.
	if !types.IsError(out) {
		t.Errorf("Eval() got %v (%T), wanted CEL error value", out, out)
	}
	hostErr := hostFuncErr(t, err, f.errBoom)
	if hostErr.FunctionName() != "fail" {
		t.Errorf("FunctionName() got %q, wanted 'fail'", hostErr.FunctionName())
	}
	if err.Error() != "boom" {
		t.Errorf("Eval() error message got %q, wanted 'boom'", err.Error())
	}
	// The error must be attributed to the 'fail(1)' call expression. The
	// parser records the call expression offset at the '(' token.
	callID := ast.NativeRep().Expr().ID()
	if hostErr.NodeID() != callID {
		t.Errorf("NodeID() got %d, wanted call expr id %d", hostErr.NodeID(), callID)
	}
	loc := ast.NativeRep().SourceInfo().GetStartLocation(hostErr.NodeID())
	if loc.Line() != 1 || loc.Column() != 4 {
		t.Errorf("error location got (%d, %d), wanted (1, 4)", loc.Line(), loc.Column())
	}
	if f.failCalls != 1 {
		t.Errorf("fail() call count got %d, wanted 1", f.failCalls)
	}
}

func TestHostFuncErrorShortCircuit(t *testing.T) {
	tests := []struct {
		expr string
		out  ref.Val
	}{
		{expr: "false && fail(1) == 1", out: types.False},
		{expr: "true || fail(1) == 1", out: types.True},
		{expr: "true ? 1 : fail(1)", out: types.Int(1)},
	}
	for _, tst := range tests {
		t.Run(tst.expr, func(t *testing.T) {
			f := newHostFuncFixture(t)
			out, _, err := f.eval(t, tst.expr)
			if err != nil {
				t.Errorf("Eval(%q) got error %v, wanted nil", tst.expr, err)
			}
			if out != tst.out {
				t.Errorf("Eval(%q) got %v, wanted %v", tst.expr, out, tst.out)
			}
			// The short-circuited branch must not invoke the function.
			if f.failCalls != 0 {
				t.Errorf("fail() call count got %d, wanted 0", f.failCalls)
			}
		})
	}
}

func TestHostFuncErrorNestedCall(t *testing.T) {
	f := newHostFuncFixture(t)
	out, ast, err := f.eval(t, "echo(fail(1))")
	if !types.IsError(out) {
		t.Errorf("Eval() got %v (%T), wanted CEL error value", out, out)
	}
	hostErr := hostFuncErr(t, err, f.errBoom)
	// The error is attributed to the innermost failing call, not the outer
	// 'echo' call which never executed.
	if hostErr.FunctionName() != "fail" {
		t.Errorf("FunctionName() got %q, wanted 'fail'", hostErr.FunctionName())
	}
	innerCallID := ast.NativeRep().Expr().AsCall().Args()[0].ID()
	if hostErr.NodeID() != innerCallID {
		t.Errorf("NodeID() got %d, wanted inner call expr id %d", hostErr.NodeID(), innerCallID)
	}
	loc := ast.NativeRep().SourceInfo().GetStartLocation(hostErr.NodeID())
	if loc.Line() != 1 || loc.Column() != 9 {
		t.Errorf("error location got (%d, %d), wanted (1, 9)", loc.Line(), loc.Column())
	}
	if f.failCalls != 1 {
		t.Errorf("fail() call count got %d, wanted 1", f.failCalls)
	}
	if f.echoCalls != 0 {
		t.Errorf("echo() call count got %d, wanted 0", f.echoCalls)
	}
}

func TestHostFuncErrorNestedChain(t *testing.T) {
	f := newHostFuncFixture(t)
	_, ast, err := f.eval(t, "fail(echo(1))")
	hostErr := hostFuncErr(t, err, f.errBoom)
	// The inner 'echo' call succeeds and the outer 'fail' call raises the
	// error at the root call expression.
	if hostErr.FunctionName() != "fail" {
		t.Errorf("FunctionName() got %q, wanted 'fail'", hostErr.FunctionName())
	}
	rootCallID := ast.NativeRep().Expr().ID()
	if hostErr.NodeID() != rootCallID {
		t.Errorf("NodeID() got %d, wanted root call expr id %d", hostErr.NodeID(), rootCallID)
	}
	loc := ast.NativeRep().SourceInfo().GetStartLocation(hostErr.NodeID())
	if loc.Line() != 1 || loc.Column() != 4 {
		t.Errorf("error location got (%d, %d), wanted (1, 4)", loc.Line(), loc.Column())
	}
	if f.failCalls != 1 || f.echoCalls != 1 {
		t.Errorf("call counts got fail=%d, echo=%d, wanted fail=1, echo=1", f.failCalls, f.echoCalls)
	}
}
