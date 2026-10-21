package deployments

import "testing"

func TestCanMove(t *testing.T) {
	cases := []struct {
		name     string
		from, to State
		want     bool
	}{
		{"queued to starting", Queued, Starting, true},
		{"starting to healthy", Starting, Healthy, true},
		{"healthy to stopped", Healthy, Stopped, true},
		{"queued can fail", Queued, Failed, true},
		{"starting can fail", Starting, Failed, true},
		{"owner can stop in flight", Queued, Stopped, true},
		{"failed never becomes healthy", Failed, Healthy, false},
		{"stopped is terminal", Stopped, Healthy, false},
		{"cannot skip starting", Queued, Healthy, false},
		{"no staying put", Healthy, Healthy, false},
		{"unknown state", State("nonsense"), Starting, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanMove(tc.from, tc.to); got != tc.want {
				t.Fatalf("CanMove(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
			}
		})
	}
}

func TestInFlight(t *testing.T) {
	for _, state := range []State{Queued, Starting} {
		if !InFlight(state) {
			t.Fatalf("%s should be in flight", state)
		}
	}
	for _, state := range []State{Healthy, Failed, Stopped} {
		if InFlight(state) {
			t.Fatalf("%s should not be in flight", state)
		}
	}
}
