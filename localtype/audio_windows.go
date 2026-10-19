package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type audioDevice struct {
	Name  string
	Input string
}

type audioSession struct {
	cmd        *exec.Cmd
	stdin      *os.File
	stderr     bytes.Buffer
	readerDone chan struct{}
	waitDone   chan struct{}
	mu         sync.Mutex
	recording  bool
	pcm        []int16
	level      float64
	waveform   []float64
	stopOnce   sync.Once
}

var (
	sessionMu  sync.RWMutex
	session    *audioSession
	ffmpegPath string
	deviceLine = regexp.MustCompile(`"(.*)" \(audio\)`)
	altLine    = regexp.MustCompile(`Alternative name "(.*)"`)
)

func currentSession() *audioSession {
	sessionMu.RLock()
	defer sessionMu.RUnlock()
	return session
}

func findFFmpeg() (string, error) {
	for _, name := range []string{"ffmpeg.exe", "ffmpeg"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		matches, _ := filepath.Glob(filepath.Join(localAppData, "Programs", "LocalType", "tools", "ffmpeg", "*", "bin", "ffmpeg.exe"))
		if len(matches) > 0 {
			return matches[0], nil
		}
	}
	return "", fmt.Errorf("ffmpeg.exe is missing from PATH; run setup.ps1")
}

func listAudioDevices(ffmpeg string) ([]audioDevice, error) {
	cmd := exec.Command(ffmpeg, "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	var output bytes.Buffer
	cmd.Stderr = &output
	_ = cmd.Run() // ffmpeg returns nonzero after printing the DirectShow device list.

	var devices []audioDevice
	lastAudio := -1
	for _, line := range strings.Split(output.String(), "\n") {
		line = strings.TrimSpace(line)
		if match := deviceLine.FindStringSubmatch(line); len(match) == 2 {
			devices = append(devices, audioDevice{Name: match[1], Input: match[1]})
			lastAudio = len(devices) - 1
			continue
		}
		if match := altLine.FindStringSubmatch(line); len(match) == 2 && lastAudio >= 0 {
			devices[lastAudio].Input = match[1]
			lastAudio = -1
			continue
		}
		if strings.Contains(line, "(video)") {
			lastAudio = -1
		}
	}
	seen := make(map[string]int)
	for i := range devices {
		seen[devices[i].Name]++
		if seen[devices[i].Name] > 1 {
			devices[i].Name = fmt.Sprintf("%s (#%d)", devices[i].Name, seen[devices[i].Name])
		}
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("Windows did not report any microphones to FFmpeg")
	}
	return devices, nil
}

func selectMicrophone(device audioDevice) error {
	sessionMu.Lock()
	old := session
	if old != nil && old.isRecording() {
		sessionMu.Unlock()
		return fmt.Errorf("finish dictation before changing microphones")
	}
	session = nil
	sessionMu.Unlock()
	if old != nil {
		old.stop()
	}

	ffmpeg := ffmpegPath
	if ffmpeg == "" {
		var err error
		ffmpeg, err = findFFmpeg()
		if err != nil {
			return err
		}
		ffmpegPath = ffmpeg
	}
	next, err := startAudioSession(ffmpeg, device.Input)
	if err != nil {
		return err
	}
	sessionMu.Lock()
	session = next
	sessionMu.Unlock()
	if err := saveSelectedDevice(device.Input); err != nil {
		return fmt.Errorf("microphone is active but setting could not be saved: %w", err)
	}
	return nil
}

func startAudioSession(ffmpeg, device string) (*audioSession, error) {
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		stdoutR.Close()
		stdoutW.Close()
		return nil, err
	}
	s := &audioSession{stdin: stdinW, readerDone: make(chan struct{}), waitDone: make(chan struct{}), waveform: make([]float64, 0, 220)}
	cmd := exec.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error", "-f", "dshow", "-audio_buffer_size", "100",
		"-i", "audio="+device, "-ac", "1", "-ar", fmt.Sprint(sampleRate), "-f", "s16le", "-acodec", "pcm_s16le", "pipe:1")
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	cmd.Stderr = &s.stderr
	if err := cmd.Start(); err != nil {
		stdoutR.Close()
		stdoutW.Close()
		stdinR.Close()
		stdinW.Close()
		return nil, err
	}
	stdoutW.Close()
	stdinR.Close()
	s.cmd = cmd
	go s.readAudio(stdoutR)
	go func() { _ = cmd.Wait(); close(s.waitDone) }()
	select {
	case <-s.waitDone:
		<-s.readerDone
		stdinW.Close()
		return nil, fmt.Errorf("FFmpeg could not open microphone: %s", strings.TrimSpace(s.stderr.String()))
	case <-time.After(350 * time.Millisecond):
		return s, nil
	}
}

func (s *audioSession) readAudio(r io.ReadCloser) {
	defer close(s.readerDone)
	defer r.Close()
	buf := make([]byte, 8192)
	for {
		n, err := r.Read(buf)
		if n > 1 {
			count := n / 2
			samples := make([]int16, count)
			for i := range samples {
				samples[i] = int16(binary.LittleEndian.Uint16(buf[i*2:]))
			}
			s.consume(samples)
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("microphone stream: %v", err)
			}
			return
		}
	}
}

func (s *audioSession) consume(samples []int16) {
	if len(samples) == 0 {
		return
	}
	var sumSquares float64
	for _, sample := range samples {
		value := float64(sample) / 32768
		sumSquares += value * value
	}
	rms := math.Sqrt(sumSquares / float64(len(samples)))
	s.mu.Lock()
	s.level = math.Min(1, rms*3.5)
	if s.recording {
		remaining := sampleRate*maxSeconds - len(s.pcm)
		if remaining > 0 {
			if len(samples) > remaining {
				samples = samples[:remaining]
			}
			s.pcm = append(s.pcm, samples...)
		}
		if len(s.pcm) >= sampleRate*maxSeconds {
			s.recording = false
			go setStatus("Five-minute dictation limit reached")
		}
	}
	step := len(samples) / 12
	if step < 1 {
		step = 1
	}
	for i := 0; i < len(samples); i += step {
		s.waveform = append(s.waveform, float64(samples[i])/32768)
	}
	if len(s.waveform) > 220 {
		s.waveform = append(s.waveform[:0], s.waveform[len(s.waveform)-220:]...)
	}
	s.mu.Unlock()
}

func (s *audioSession) snapshot() (float64, []float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.level, append([]float64(nil), s.waveform...), s.recording
}

func (s *audioSession) isRecording() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recording
}

func (s *audioSession) beginRecording() {
	s.mu.Lock()
	s.pcm = s.pcm[:0]
	s.recording = true
	s.mu.Unlock()
}

func (s *audioSession) finishRecording() []int16 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recording = false
	return append([]int16(nil), s.pcm...)
}

func (s *audioSession) stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		_, _ = s.stdin.Write([]byte("q\n"))
		_ = s.stdin.Close()
		select {
		case <-s.waitDone:
		case <-time.After(3 * time.Second):
			_ = s.cmd.Process.Kill()
			<-s.waitDone
		}
		select {
		case <-s.readerDone:
		case <-time.After(time.Second):
		}
	})
}

func stopAudioSession() {
	sessionMu.Lock()
	old := session
	session = nil
	sessionMu.Unlock()
	if old != nil {
		old.stop()
	}
}
