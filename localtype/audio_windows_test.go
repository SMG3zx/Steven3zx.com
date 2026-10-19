package main

import "testing"

func TestAudioSessionBuffersOnlyWhileRecording(t *testing.T) {
	s := &audioSession{waveform: make([]float64, 0, 220)}
	samples := []int16{1000, -1000, 2000, -2000}

	s.consume(samples)
	if got := len(s.finishRecording()); got != 0 {
		t.Fatalf("buffered %d samples while idle, want 0", got)
	}

	s.beginRecording()
	s.consume(samples)
	got := s.finishRecording()
	if len(got) != len(samples) {
		t.Fatalf("buffered %d samples, want %d", len(got), len(samples))
	}
	for i := range samples {
		if got[i] != samples[i] {
			t.Errorf("sample %d = %d, want %d", i, got[i], samples[i])
		}
	}
	level, waveform, recording := s.snapshot()
	if level <= 0 || len(waveform) == 0 {
		t.Errorf("audio level/waveform did not reflect input: level=%f points=%d", level, len(waveform))
	}
	if recording {
		t.Error("recording remained active after finishRecording")
	}
}
