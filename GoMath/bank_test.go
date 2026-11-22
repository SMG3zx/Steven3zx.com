package main

import "testing"

func TestWorksheetBank(t *testing.T) {
	if len(Worksheet) != 218 {
		t.Fatalf("worksheet has %d entries, want 218 (one per screenshot or Unit 5 or Unit 6 problem part)", len(Worksheet))
	}
	for _, e := range Worksheet {
		ok, detail := e.Check()
		if !ok {
			t.Errorf("%s: %s", e.Source, detail)
		}
	}
}
