package main

import "testing"

func TestWorksheetBank(t *testing.T) {
	if len(Worksheet) != 183 {
		t.Fatalf("worksheet has %d entries, want 183 (one per screenshot or Unit 5 problem)", len(Worksheet))
	}
	for _, e := range Worksheet {
		ok, detail := e.Check()
		if !ok {
			t.Errorf("%s: %s", e.Source, detail)
		}
	}
}
