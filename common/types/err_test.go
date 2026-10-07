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

package types

import (
	"errors"
	"testing"
)

func TestHostFuncError(t *testing.T) {
	cause := errors.New("boom")
	err := NewHostFuncError("fail", 42, cause)
	if err.FunctionName() != "fail" {
		t.Errorf("FunctionName() got %q, wanted 'fail'", err.FunctionName())
	}
	if err.NodeID() != 42 {
		t.Errorf("NodeID() got %d, wanted 42", err.NodeID())
	}
	if err.Error() != "boom" {
		t.Errorf("Error() got %q, wanted 'boom'", err.Error())
	}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(%v, cause) got false, wanted true", err)
	}
	if !errors.Is(err, err) {
		t.Errorf("errors.Is(%v, itself) got false, wanted true", err)
	}
	var hostErr HostFuncError
	if !errors.As(err, &hostErr) {
		t.Fatalf("errors.As(%v, HostFuncError) got false, wanted true", err)
	}
	if hostErr.FunctionName() != "fail" || hostErr.NodeID() != 42 {
		t.Errorf("errors.As() got function %q, id %d, wanted 'fail', 42",
			hostErr.FunctionName(), hostErr.NodeID())
	}
}

func TestWrapHostFuncErr(t *testing.T) {
	cause := errors.New("boom")
	wrapped := WrapHostFuncErr("fail", 7, WrapErr(cause))
	errVal, ok := wrapped.(*Err)
	if !ok {
		t.Fatalf("WrapHostFuncErr() got %T, wanted *Err", wrapped)
	}
	if !IsError(errVal) {
		t.Errorf("IsError(%v) got false, wanted true", errVal)
	}
	if errVal.NodeID() != 7 {
		t.Errorf("NodeID() got %d, wanted 7", errVal.NodeID())
	}
	if errVal.Error() != "boom" {
		t.Errorf("Error() got %q, wanted 'boom'", errVal.Error())
	}
	if !errors.Is(errVal, cause) {
		t.Errorf("errors.Is(%v, cause) got false, wanted true", errVal)
	}
	var hostErr HostFuncError
	if !errors.As(errVal, &hostErr) {
		t.Fatalf("errors.As(%v, HostFuncError) got false, wanted true", errVal)
	}
	if hostErr.FunctionName() != "fail" || hostErr.NodeID() != 7 {
		t.Errorf("errors.As() got function %q, id %d, wanted 'fail', 7",
			hostErr.FunctionName(), hostErr.NodeID())
	}
}

func TestWrapHostFuncErrPassthrough(t *testing.T) {
	// Non-error values are returned unaltered.
	if got := WrapHostFuncErr("fail", 7, True); got != True {
		t.Errorf("WrapHostFuncErr() got %v, wanted %v", got, True)
	}
	// Errors already attributed to an AST node are returned unaltered to
	// preserve the innermost failing call site as the error origin.
	labeled := NewErrWithNodeID(3, "inner")
	if got := WrapHostFuncErr("fail", 7, labeled); got != labeled {
		t.Errorf("WrapHostFuncErr() got %v, wanted %v", got, labeled)
	}
}
