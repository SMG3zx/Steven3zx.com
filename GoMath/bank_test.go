package main

import "testing"

func TestWorksheetBank(t *testing.T) {
	if len(Worksheet) != 181 {
		t.Fatalf("worksheet has %d entries, want 181 (one per screenshot or Unit 5 problem)", len(Worksheet))
	}
	for _, e := range Worksheet {
		ok, detail := e.Check()
		if !ok {
			t.Errorf("%s: %s", e.Source, detail)
		}
	}
}
