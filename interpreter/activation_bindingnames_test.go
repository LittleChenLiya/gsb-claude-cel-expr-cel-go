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

func TestRootBindingNamesMapActivation(t *testing.T) {
	calls := 0
	act, err := NewActivation(map[string]any{
		"a": int64(1),
		"b": func() any {
			calls++
			return int64(2)
		},
		"c": func() ref.Val {
			calls++
			return nil
		},
		"d": nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	names, complete := RootBindingNames(act)
	if !complete {
		t.Fatal("map activation should be fully enumerable")
	}
	for _, want := range []string{"a", "b", "c", "d"} {
		if !names[want] {
			t.Errorf("missing root name %q in %v", want, names)
		}
	}
	if calls != 0 {
		t.Errorf("enumerating names invoked %d lazy bindings, want 0", calls)
	}
}

func TestRootBindingNamesHierarchy(t *testing.T) {
	parent, err := NewActivation(map[string]any{"a": int64(1), "b": int64(2)})
	if err != nil {
		t.Fatal(err)
	}
	child, err := NewActivation(map[string]any{"b": int64(3), "c": int64(4)})
	if err != nil {
		t.Fatal(err)
	}
	hier := NewHierarchicalActivation(parent, child)
	names, complete := RootBindingNames(hier)
	if !complete {
		t.Fatal("hierarchical activation should be fully enumerable")
	}
	for _, want := range []string{"a", "b", "c"} {
		if !names[want] {
			t.Errorf("missing root name %q in %v", want, names)
		}
	}
	if len(names) != 3 {
		t.Errorf("got %d names, want 3: %v", len(names), names)
	}
}

func TestRootBindingNamesPartialAndFunction(t *testing.T) {
	part, err := NewPartialActivation(map[string]any{"a": int64(1)}, NewAttributePattern("b"))
	if err != nil {
		t.Fatal(err)
	}
	if names, complete := RootBindingNames(part); !complete || !names["a"] || names["b"] {
		t.Errorf("partial activation names = %v, complete=%v, want only {a}", names, complete)
	}

	funcAct, err := NewFunctionActivation(map[string]any{"a": int64(1)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if names, complete := RootBindingNames(funcAct); !complete || !names["a"] {
		t.Errorf("function activation names = %v, complete=%v, want {a}", names, complete)
	}
}

// enumerableActivation is a custom activation which explicitly supports name enumeration.
type enumerableActivation struct {
	bindings map[string]any
}

func (a *enumerableActivation) ResolveName(name string) (any, bool) {
	v, ok := a.bindings[name]
	return v, ok
}

func (a *enumerableActivation) Parent() Activation { return nil }

func (a *enumerableActivation) BindingNames() (map[string]bool, bool) {
	names := make(map[string]bool, len(a.bindings))
	for k := range a.bindings {
		names[k] = true
	}
	return names, true
}

// opaqueActivation is a custom activation which cannot enumerate its names.
type opaqueActivation struct{}

func (opaqueActivation) ResolveName(string) (any, bool) { return nil, false }
func (opaqueActivation) Parent() Activation             { return nil }

func TestRootBindingNamesCustomActivations(t *testing.T) {
	enumerable := &enumerableActivation{bindings: map[string]any{"a": int64(1)}}
	if names, complete := RootBindingNames(enumerable); !complete || !names["a"] {
		t.Errorf("enumerable activation names = %v, complete=%v, want {a}", names, complete)
	}
	if _, complete := RootBindingNames(opaqueActivation{}); complete {
		t.Error("opaque activation should report incomplete enumeration")
	}
}

func TestRootBindingNamesEmpty(t *testing.T) {
	names, complete := RootBindingNames(EmptyActivation())
	if !complete || len(names) != 0 {
		t.Errorf("empty activation names = %v, complete=%v, want empty and complete", names, complete)
	}
}
