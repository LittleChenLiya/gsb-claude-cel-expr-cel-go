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

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

// FunctionCallError describes an error raised by a host (Go) function during
// the evaluation of a single CEL call expression.
//
// The error retains the name of the function which was invoked and the id of
// the call expression within the AST, while keeping the original error
// produced by the host function inspectable via errors.Is and errors.As.
type FunctionCallError interface {
	error

	// FunctionName returns the name of the CEL function which raised the error.
	FunctionName() string

	// ExprID returns the AST node id of the call expression which raised the
	// error. The id may be resolved to a source location through the AST
	// SourceInfo.
	ExprID() int64

	// Unwrap returns the original error produced by the host function.
	Unwrap() error
}

// functionError is the default FunctionCallError implementation.
type functionError struct {
	function string
	id       int64
	cause    error
}

// Error implements the error interface, preserving the original error message.
func (e *functionError) Error() string {
	return e.cause.Error()
}

// FunctionName implements the FunctionCallError interface method.
func (e *functionError) FunctionName() string {
	return e.function
}

// ExprID implements the FunctionCallError interface method.
func (e *functionError) ExprID() int64 {
	return e.id
}

// Unwrap implements the FunctionCallError interface method.
func (e *functionError) Unwrap() error {
	return e.cause
}

// wrapFunctionErr labels error values returned by host function calls with the
// function name and call expression id, keeping the original error reachable
// through errors.Is and errors.As.
//
// Non-error values are returned unaltered, as are errors which already carry
// function call metadata, ensuring that the innermost call site of a nested
// error chain is preserved. The wrapped value remains a *types.Err so that
// evaluation continues to treat the failure as a CEL error rather than a
// normal value.
func wrapFunctionErr(id int64, function string, val ref.Val) ref.Val {
	err, ok := val.(*types.Err)
	if !ok {
		return val
	}
	var fnErr FunctionCallError
	if errors.As(err, &fnErr) {
		return val
	}
	return types.LabelErrNode(id, types.WrapErr(&functionError{
		function: function,
		id:       id,
		cause:    err,
	}))
}
