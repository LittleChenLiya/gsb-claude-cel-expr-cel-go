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
	"testing"

	"cel.dev/cel-go/common/types"
)

func TestInterpreter_HostCallLimit(t *testing.T) {
	// Three host calls, budget of two: the third call is rejected before the host
	// function is entered.
	tc := testCase{
		expr: `[size('a'), size('bb'), size('ccc')]`,
	}
	prg, frame, err := program(t, &tc, HostCallLimit(2))
	if err != nil {
		t.Fatalf("program(%s) failed: %v", tc.expr, err)
	}
	out := prg.Exec(frame)
	frame.Close()
	if !types.IsError(out) || out.(*types.Err).String() != "host call limit exceeded" {
		t.Errorf("got %v, wanted 'host call limit exceeded' error", out)
	}
}

func TestInterpreter_HostCallLimitResetsPerEval(t *testing.T) {
	// Two host calls with a budget of two must succeed on every evaluation pass since
	// the budget resets at the start of each pass.
	tc := testCase{
		expr: `[size('a'), size('bb')]`,
	}
	prg, frame, err := program(t, &tc, HostCallLimit(2))
	if err != nil {
		t.Fatalf("program(%s) failed: %v", tc.expr, err)
	}
	for i := 0; i < 2; i++ {
		out := prg.Exec(frame)
		if types.IsError(out) {
			t.Errorf("eval pass %d got error %v, wanted [1, 2]", i, out)
		}
	}
	frame.Close()
}

func TestExecutionFrameHostCallBudget(t *testing.T) {
	frame, err := NewExecutionFrame(EmptyActivation())
	if err != nil {
		t.Fatalf("NewExecutionFrame() failed: %v", err)
	}
	defer frame.Close()

	// Without a configured limit, charges are unbounded.
	for i := 0; i < 10; i++ {
		if !frame.ChargeHostCall() {
			t.Fatalf("ChargeHostCall() rejected call %d without a configured limit", i)
		}
	}

	// A limit admits exactly that many charges, then rejects before the host is entered.
	frame.SetHostCallLimit(2)
	if !frame.ChargeHostCall() || !frame.ChargeHostCall() {
		t.Fatal("ChargeHostCall() rejected calls within the budget")
	}
	if frame.ChargeHostCall() {
		t.Fatal("ChargeHostCall() admitted a call beyond the budget")
	}

	// Setting the limit again resets the budget for the next evaluation pass.
	frame.SetHostCallLimit(2)
	if !frame.ChargeHostCall() {
		t.Fatal("ChargeHostCall() did not reset the budget for a new evaluation pass")
	}
}
