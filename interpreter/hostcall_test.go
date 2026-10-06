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
)

func TestFrameTrackHostCall(t *testing.T) {
	// Without a configured limit, host calls are not accounted.
	frame, err := NewExecutionFrame(map[string]any{})
	if err != nil {
		t.Fatalf("NewExecutionFrame() failed: %v", err)
	}
	for i := 0; i < 3; i++ {
		if !frame.TrackHostCall() {
			t.Fatalf("TrackHostCall() without a limit got false on call %d, wanted true", i+1)
		}
	}
	frame.Close()

	// With a limit of two, the third call is rejected before entering the host function.
	frame, err = NewExecutionFrame(map[string]any{})
	if err != nil {
		t.Fatalf("NewExecutionFrame() failed: %v", err)
	}
	frame.SetHostCallLimit(2)
	for i, want := range []bool{true, true, false} {
		if got := frame.TrackHostCall(); got != want {
			t.Fatalf("TrackHostCall() call %d got %t, wanted %t", i+1, got, want)
		}
	}
	// Child frames (e.g. comprehension iterations) share the evaluation budget.
	childScope, err := NewActivation(map[string]any{})
	if err != nil {
		t.Fatalf("NewActivation() failed: %v", err)
	}
	child := frame.Push(childScope)
	if child.TrackHostCall() {
		t.Error("child frame TrackHostCall() got true, wanted false: budget must be shared")
	}
	frame.Pop()
	frame.Close()

	// A subsequent independent evaluation starts with a fresh budget.
	frame, err = NewExecutionFrame(map[string]any{})
	if err != nil {
		t.Fatalf("NewExecutionFrame() failed: %v", err)
	}
	frame.SetHostCallLimit(2)
	if !frame.TrackHostCall() {
		t.Error("TrackHostCall() on a fresh evaluation got false, wanted true")
	}
	frame.Close()
}
