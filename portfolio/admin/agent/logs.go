package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

// logTail is how many existing lines a log stream starts with before it follows.
const logTail = 300

// containerIDPattern accepts Docker container IDs and names. It keeps a path
// segment from carrying a slash or query into the Docker API URL.
var containerIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// logLine is one line of container output as the admin server forwards it.
type logLine struct {
	Time   string `json:"time,omitempty"`
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// openLogs starts a follow-mode log stream for a container. The response body
// stays open until ctx is canceled or the container stops.
func (d *dockerClient) openLogs(ctx context.Context, id string) (io.ReadCloser, error) {
	q := url.Values{
		"stdout":     {"1"},
		"stderr":     {"1"},
		"timestamps": {"1"},
		"tail":       {fmt.Sprint(logTail)},
		"follow":     {"1"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/"+id+"/logs?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.stream.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, errNoContainer
		}
		return nil, fmt.Errorf("docker logs: %s", resp.Status)
	}
	return resp.Body, nil
}

var errNoContainer = errors.New("no such container")

// readDockerLogs decodes Docker's log stream and calls emit once per line.
//
// A container without a TTY sends frames: an 8-byte header (stream type 1 for
// stdout or 2 for stderr, three zero bytes, then the payload length as a
// big-endian uint32) followed by the payload. A container with a TTY sends raw
// text. The first eight bytes tell the two apart.
func readDockerLogs(r io.Reader, emit func(logLine) error) error {
	br := bufio.NewReaderSize(r, 64<<10)
	head, _ := br.Peek(8)
	if len(head) == 8 && (head[0] == 1 || head[0] == 2) && head[1] == 0 && head[2] == 0 && head[3] == 0 {
		return readFrames(br, emit)
	}
	return readRaw(br, "stdout", emit)
}

func readFrames(br *bufio.Reader, emit func(logLine) error) error {
	// Partial lines wait here until their newline arrives in a later frame.
	carry := map[string][]byte{}
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(br, hdr); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		stream := "stdout"
		if hdr[0] == 2 {
			stream = "stderr"
		}
		payload := make([]byte, binary.BigEndian.Uint32(hdr[4:]))
		if _, err := io.ReadFull(br, payload); err != nil {
			return err
		}
		buf := append(carry[stream], payload...)
		for {
			i := bytes.IndexByte(buf, '\n')
			if i < 0 {
				break
			}
			if err := emit(parseLogLine(stream, buf[:i])); err != nil {
				return err
			}
			buf = buf[i+1:]
		}
		carry[stream] = append([]byte(nil), buf...)
	}
}

func readRaw(r io.Reader, stream string, emit func(logLine) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		if err := emit(parseLogLine(stream, sc.Bytes())); err != nil {
			return err
		}
	}
	return sc.Err()
}

// parseLogLine splits off the RFC 3339 timestamp Docker adds when timestamps=1.
func parseLogLine(stream string, raw []byte) logLine {
	raw = bytes.TrimSuffix(raw, []byte("\r"))
	if i := bytes.IndexByte(raw, ' '); i > 0 {
		if t, err := time.Parse(time.RFC3339Nano, string(raw[:i])); err == nil {
			return logLine{Time: t.UTC().Format(time.RFC3339Nano), Stream: stream, Text: string(raw[i+1:])}
		}
	}
	return logLine{Stream: stream, Text: string(raw)}
}
