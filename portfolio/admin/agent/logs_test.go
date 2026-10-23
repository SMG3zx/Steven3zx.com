package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// frame builds one Docker multiplexed log frame.
func frame(stream byte, payload string) []byte {
	hdr := make([]byte, 8)
	hdr[0] = stream
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(payload)))
	return append(hdr, payload...)
}

func collect(t *testing.T, in []byte) []logLine {
	t.Helper()
	var out []logLine
	if err := readDockerLogs(bytes.NewReader(in), func(l logLine) error {
		out = append(out, l)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReadDockerLogsMultiplexed(t *testing.T) {
	var in []byte
	in = append(in, frame(1, "2026-10-22T10:00:00.000000001Z GET /a 200\n")...)
	// A line split across two stdout frames must come out as one line.
	in = append(in, frame(1, "2026-10-22T10:00:01Z GET /b ")...)
	in = append(in, frame(1, "404\n")...)
	in = append(in, frame(2, "2026-10-22T10:00:02Z boom\r\n")...)

	got := collect(t, in)
	want := []logLine{
		{Time: "2026-10-22T10:00:00.000000001Z", Stream: "stdout", Text: "GET /a 200"},
		{Time: "2026-10-22T10:00:01Z", Stream: "stdout", Text: "GET /b 404"},
		{Time: "2026-10-22T10:00:02Z", Stream: "stderr", Text: "boom"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestReadDockerLogsTTY(t *testing.T) {
	in := []byte("2026-10-22T10:00:00Z first\n2026-10-22T10:00:01Z second\n")
	got := collect(t, in)
	if len(got) != 2 || got[0].Text != "first" || got[1].Text != "second" {
		t.Fatalf("unexpected lines: %+v", got)
	}
	if got[1].Time != "2026-10-22T10:00:01Z" || got[1].Stream != "stdout" {
		t.Fatalf("unexpected fields: %+v", got[1])
	}
}

func TestParseLogLineWithoutTimestamp(t *testing.T) {
	got := parseLogLine("stdout", []byte("no timestamp here"))
	if got.Time != "" || got.Text != "no timestamp here" {
		t.Fatalf("got %+v", got)
	}
}
