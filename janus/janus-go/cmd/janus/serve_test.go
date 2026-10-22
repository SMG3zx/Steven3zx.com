package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestServeNamesTheServicesToRun(t *testing.T) {
	tests := []struct {
		args    []string
		want    []string
		failure string
	}{
		{args: []string{"all"}, want: serviceNames},
		{args: []string{"gateway"}, want: []string{"gateway"}},
		{args: []string{"fleet", "identity", "fleet"}, want: []string{"identity", "fleet"}},
		{args: nil, failure: "usage: janus"},
		{args: []string{"all", "gateway"}, failure: "serve all runs every service"},
		{args: []string{"database"}, failure: `unknown service "database"`},
	}
	for _, tt := range tests {
		got, err := parseServe(tt.args)
		if tt.failure != "" {
			if err == nil || !strings.Contains(err.Error(), tt.failure) {
				t.Errorf("parseServe(%v) = %v, %v, want an error containing %q", tt.args, got, err, tt.failure)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseServe(%v) = %v, %v, want %v", tt.args, got, err, tt.want)
		}
	}
}

func TestAProcessRunsOnlyTheComponentsOfItsServices(t *testing.T) {
	components := func(services ...string) []string {
		var names []string
		for _, g := range newServing(nil, nil, services).groups() {
			for _, c := range g.components {
				names = append(names, c.name)
			}
		}
		return names
	}
	if got, want := components("identity"), []string{"audit", "telemetry", "nats", "audit stream", "identity service"}; !reflect.DeepEqual(got, want) {
		t.Errorf("identity alone runs %v, want %v", got, want)
	}
	if got, want := components("audit", "gateway"), []string{"audit", "telemetry", "nats", "audit service", "authentication", "authorization", "service clients", "http"}; !reflect.DeepEqual(got, want) {
		t.Errorf("audit and the gateway run %v, want %v", got, want)
	}
	if got, want := components("builds", "runs", "fleet"), []string{"audit", "telemetry", "nats", "audit stream", "builds service", "runs service", "fleet"}; !reflect.DeepEqual(got, want) {
		t.Errorf("builds, runs and fleet run %v, want %v", got, want)
	}
}
