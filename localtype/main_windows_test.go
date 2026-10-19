package main

import "testing"

func TestIsBlankAudio(t *testing.T) {
	for _, input := range []string{"", "   ", "[BLANK_AUDIO]", "  [blank_audio]  "} {
		if !isBlankAudio(input) {
			t.Errorf("isBlankAudio(%q) = false, want true", input)
		}
	}
	for _, input := range []string{"hello there", "[BLANK_AUDIO] please"} {
		if isBlankAudio(input) {
			t.Errorf("isBlankAudio(%q) = true, want false", input)
		}
	}
}
