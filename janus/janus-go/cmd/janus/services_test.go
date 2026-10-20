package main

import (
	"reflect"
	"testing"
)

func TestTheComponentTableHasTheDocumentedGroupsInStartOrder(t *testing.T) {
	var names []string
	for _, g := range newApplication(nil, nil).groups() {
		names = append(names, g.name)
		if len(g.components) == 0 {
			t.Errorf("group %q has no components", g.name)
		}
	}
	want := []string{"foundation", "identity", "projects and builds", "runtime", "web"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("groups = %v, want %v", names, want)
	}
}
