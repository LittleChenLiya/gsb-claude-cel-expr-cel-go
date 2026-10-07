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

	"cel.dev/cel-go/common/types/ref"
)

func TestBindingKeySetsMapActivation(t *testing.T) {
	calls := 0
	a, err := NewActivation(map[string]any{
		"x":    1,
		"nilv": nil,
		"lazy": func() any {
			calls++
			return 2
		},
		"lazyVal": func() ref.Val {
			calls++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewActivation() failed: %v", err)
	}
	present, nilBound := BindingKeySets(a)
	if calls != 0 {
		t.Fatalf("lazy bindings evaluated %d times during key inspection", calls)
	}
	for _, name := range []string{"x", "lazy", "lazyVal"} {
		if !present[name] {
			t.Errorf("expected present key %q in %v", name, present)
		}
	}
	if !nilBound["nilv"] {
		t.Errorf("expected nil-bound key nilv in %v", nilBound)
	}
	if present["nilv"] {
		t.Errorf("nil binding must not be reported as non-nil present")
	}

	// BindingKeys merges both sets and keeps nil keys present.
	keys := BindingKeys(a)
	for _, name := range []string{"x", "nilv", "lazy", "lazyVal"} {
		if !keys[name] {
			t.Errorf("BindingKeys() missing %q in %v", name, keys)
		}
	}
	if calls != 0 {
		t.Errorf("lazy bindings evaluated %d times during BindingKeys", calls)
	}
}

func TestBindingKeySetsHierarchy(t *testing.T) {
	parent, err := NewActivation(map[string]any{"p": 1, "pnil": nil})
	if err != nil {
		t.Fatalf("NewActivation() failed: %v", err)
	}
	child, err := NewActivation(map[string]any{"c": 2, "cnil": nil})
	if err != nil {
		t.Fatalf("NewActivation() failed: %v", err)
	}
	hier := NewHierarchicalActivation(parent, child)
	present, nilBound := BindingKeySets(hier)
	for _, name := range []string{"p", "c"} {
		if !present[name] {
			t.Errorf("missing present key %q in %v", name, present)
		}
	}
	for _, name := range []string{"pnil", "cnil"} {
		if !nilBound[name] {
			t.Errorf("missing nil key %q in %v", name, nilBound)
		}
	}

	part, err := NewPartialActivation(parent)
	if err != nil {
		t.Fatalf("NewPartialActivation() failed: %v", err)
	}
	if keys := BindingKeys(part); !keys["p"] || !keys["pnil"] {
		t.Errorf("partial activation keys = %v, want p and pnil", keys)
	}
}
