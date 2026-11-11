package main

import "testing"

func TestWorksheetBank(t *testing.T) {
	if len(Worksheet) != 133 {
		t.Fatalf("worksheet has %d entries, want 133 (one per screenshot)", len(Worksheet))
	}
	for _, e := range Worksheet {
		ok, detail := e.Check()
		if !ok {
			t.Errorf("%s: %s", e.Source, detail)
		}
	}
}
