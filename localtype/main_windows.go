package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/getlantern/systray"
	"golang.org/x/sys/windows"
)

const (
	sampleRate  = 16000
	maxSeconds  = 300
	whisperURL  = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.en.bin"
	modelSHA256 = "c6138d6d58ecc8322097e0f987c32f1be8bb0a18532a3f88f734d1bbf9c41e5d"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey   = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey = user32.NewProc("UnregisterHotKey")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procSendInput        = user32.NewProc("SendInput")
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	k32                  = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalAlloc      = k32.NewProc("GlobalAlloc")
	procGlobalLock       = k32.NewProc("GlobalLock")
	procGlobalUnlock     = k32.NewProc("GlobalUnlock")
	procBeep             = k32.NewProc("Beep")
	statusItem           *systray.MenuItem
)

type winMessage struct {
	Hwnd    windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

func main() {
	if err := stopPreviousInstances(); err != nil {
		log.Printf("could not stop previous LocalType instance: %v", err)
	}
	systray.Run(onReady, func() {})
}

func stopPreviousInstances() error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return fmt.Errorf("list processes: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	currentPID := uint32(os.Getpid())
	currentName := filepath.Base(os.Args[0])
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return fmt.Errorf("read process list: %w", err)
	}
	for {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if entry.ProcessID != currentPID && strings.EqualFold(name, currentName) {
			process, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, entry.ProcessID)
			if err != nil {
				return fmt.Errorf("open previous process %d: %w", entry.ProcessID, err)
			}
			err = windows.TerminateProcess(process, 0)
			if err == nil {
				_, err = windows.WaitForSingleObject(process, 5000)
			}
			_ = windows.CloseHandle(process)
			if err != nil {
				return fmt.Errorf("stop previous process %d: %w", entry.ProcessID, err)
			}
			log.Printf("stopped previous %s process (pid %d)", currentName, entry.ProcessID)
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return nil
}

func onReady() {
	systray.SetTitle("LocalType")
	systray.SetTooltip("LocalType — Ctrl+Shift+Space to dictate")
	statusItem = systray.AddMenuItem("Starting microphone setup…", "")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "Exit LocalType")
	go hotkeyLoop()
	go runAudioWindow()
	go func() { <-quit.ClickedCh; stopAudioSession(); systray.Quit() }()
}

func setStatus(s string) {
	if statusItem != nil {
		statusItem.SetTitle(s)
	}
	postWindowStatus(s)
	log.Println(s)
}

func hotkeyLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	const id = 1
	// Ctrl + Shift + Space. A single registered hotkey naturally gives push-to-talk semantics.
	const modNoRepeat = 0x4000
	r, _, err := procRegisterHotKey.Call(0, id, 0x0002|0x0004|modNoRepeat, 0x20)
	if r == 0 {
		lastErr := windows.GetLastError()
		setStatus("Hotkey unavailable; another app may own Ctrl+Shift+Space")
		log.Printf("RegisterHotKey(Ctrl+Shift+Space) failed: %v (call error: %v)", lastErr, err)
		return
	}
	log.Printf("registered global hotkey Ctrl+Shift+Space")
	defer procUnregisterHotKey.Call(0, id)
	var msg winMessage
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) == -1 {
			log.Printf("GetMessageW failed: %v", windows.GetLastError())
			return
		}
		if r == 0 {
			return
		}
		if msg.Message == 0x0312 && msg.WParam == id && hotkeyChordHeld() {
			log.Printf("received Ctrl+Shift+Space keydown")
			if startRecording() {
				for hotkeyChordHeld() {
					time.Sleep(10 * time.Millisecond)
				}
				finishRecording()
			}
		}
	}
}

func toggleRecording() {
	r := currentSession()
	if r == nil {
		setStatus("Select a microphone to begin")
		return
	}
	if r.isRecording() {
		finishRecording()
		return
	}
	startRecording()
}

func startRecording() bool {
	r := currentSession()
	if r == nil {
		setStatus("Select a microphone to begin")
		return false
	}
	if r.isRecording() {
		return false
	}
	// Play the start tone before buffering so the cue itself cannot enter the
	// recording and be mistaken for speech. Let DirectShow's input buffer drain
	// before accepting samples; the stream continues running while this waits.
	playRecordingCue(true)
	time.Sleep(200 * time.Millisecond)
	r.beginRecording()
	setStatus("Recording · release the hotkey or click Finish")
	return true
}

func finishRecording() {
	r := currentSession()
	if r == nil || !r.isRecording() {
		return
	}
	pcm := r.finishRecording()
	playRecordingCue(false)
	setStatus("Transcribing locally…")
	go transcribeAndPaste(pcm)
}

func hotkeyChordHeld() bool {
	for _, key := range []uintptr{0x11, 0x10, 0x20} { // Ctrl, Shift, Space
		state, _, _ := procGetAsyncKeyState.Call(key)
		if state&0x8000 == 0 {
			return false
		}
	}
	return true
}

func playRecordingCue(start bool) {
	frequency := uintptr(900)
	if start {
		frequency = 1400
	}
	if result, _, err := procBeep.Call(frequency, 55); result == 0 {
		log.Printf("recording audio cue failed: %v", err)
	}
}

func transcribeAndPaste(pcm []int16) {
	if len(pcm) < sampleRate/3 {
		setStatus("No speech captured · ready")
		return
	}
	if len(pcm) >= sampleRate*maxSeconds {
		log.Printf("recording hit %d-second limit", maxSeconds)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		setStatus("Cannot locate app data directory")
		return
	}
	dir := filepath.Join(base, "LocalType")
	if err := os.MkdirAll(dir, 0700); err != nil {
		setStatus("Cannot create app data directory")
		return
	}
	model := filepath.Join(dir, "ggml-small.en.bin")
	if _, err := os.Stat(model); os.IsNotExist(err) {
		setStatus("Downloading English model (one time)…")
		if err := downloadModel(model); err != nil {
			setStatus("Model download failed · see log")
			log.Printf("model download: %v", err)
			return
		}
	}
	wav := filepath.Join(dir, "last-recording.wav")
	defer os.Remove(wav)
	if err := writeWav(wav, pcm); err != nil {
		setStatus("Could not prepare audio")
		log.Printf("write wav: %v", err)
		return
	}
	whisper, err := ensureWhisper(dir)
	if err != nil {
		setStatus("Speech engine setup failed · see log")
		log.Printf("whisper setup: %v", err)
		return
	}
	out := filepath.Join(dir, "transcript")
	cmd := exec.Command(whisper, "-m", model, "-f", wav, "-otxt", "-of", out, "-nt")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		setStatus("Transcription failed · see log")
		log.Printf("whisper: %v: %s", err, stderr.String())
		return
	}
	text, err := os.ReadFile(out + ".txt")
	_ = os.Remove(out + ".txt")
	if err != nil {
		setStatus("No transcript produced")
		return
	}
	textValue := strings.TrimSpace(string(text))
	textValue = polishTranscript(textValue)
	if isBlankAudio(textValue) {
		setStatus("No speech recognized · ready")
		return
	}
	if err := pasteText(textValue); err != nil {
		setStatus("Clipboard paste failed · see log")
		log.Printf("paste: %v", err)
		return
	}
	setStatus("Inserted locally · ready")
}

func isBlankAudio(text string) bool {
	return strings.TrimSpace(text) == "" || strings.EqualFold(strings.TrimSpace(text), "[BLANK_AUDIO]")
}

func writeWav(path string, pcm []int16) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dataBytes := uint32(len(pcm) * 2)
	if _, err = f.Write([]byte("RIFF")); err != nil {
		return err
	}
	if err = binary.Write(f, binary.LittleEndian, uint32(36)+dataBytes); err != nil {
		return err
	}
	if _, err = f.Write([]byte("WAVEfmt ")); err != nil {
		return err
	}
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(sampleRate), uint32(sampleRate * 2), uint16(2), uint16(16)} {
		if err = binary.Write(f, binary.LittleEndian, v); err != nil {
			return err
		}
	}
	if _, err = f.Write([]byte("data")); err != nil {
		return err
	}
	if err = binary.Write(f, binary.LittleEndian, dataBytes); err != nil {
		return err
	}
	return binary.Write(f, binary.LittleEndian, pcm)
}

func downloadModel(path string) error {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$ProgressPreference='SilentlyContinue'; Invoke-WebRequest -Uri $args[0] -OutFile $args[1]", whisperURL, path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() != 487_614_201 {
		os.Remove(path)
		return fmt.Errorf("downloaded model has unexpected size (%d bytes)", info.Size())
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(h, file)
	_ = file.Close()
	if err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != modelSHA256 {
		os.Remove(path)
		return fmt.Errorf("model SHA-256 verification failed")
	}
	return nil
}

func ensureWhisper(dir string) (string, error) {
	if p, err := exec.LookPath("whisper-cli"); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath("whisper-cli.exe"); err == nil {
		return p, nil
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		installed := filepath.Join(localAppData, "Programs", "LocalType", "tools", "whisper-cuda", "Release", "whisper-cli.exe")
		if info, err := os.Stat(installed); err == nil && !info.IsDir() {
			return installed, nil
		}
	}
	return "", fmt.Errorf("whisper.cpp is not installed. Install a Windows whisper.cpp release and add whisper-cli.exe to PATH")
}

func pasteText(s string) error {
	utf16, err := syscall.UTF16FromString(s)
	if err != nil {
		return err
	}
	r, _, e := procOpenClipboard.Call(0)
	if r == 0 {
		return fmt.Errorf("open clipboard: %w", e)
	}
	defer procCloseClipboard.Call()
	if r, _, e := procEmptyClipboard.Call(); r == 0 {
		return fmt.Errorf("empty clipboard: %w", e)
	}
	bytesNeeded := uintptr(len(utf16) * 2)
	h, _, e := procGlobalAlloc.Call(0x0002|0x0040, bytesNeeded)
	if h == 0 {
		return fmt.Errorf("allocate clipboard: %w", e)
	}
	p, _, e := procGlobalLock.Call(h)
	if p == 0 {
		return fmt.Errorf("lock clipboard: %w", e)
	}
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(p)), len(utf16)), utf16)
	procGlobalUnlock.Call(h)
	if r, _, e := procSetClipboardData.Call(13, h); r == 0 {
		return fmt.Errorf("set clipboard: %w", e)
	}
	// Ctrl+V via SendInput.
	type input struct {
		Type          uint32
		Padding       uint32
		WVk, WScan    uint16
		DwFlags, Time uint32
		Extra         uintptr
		Reserve       [8]byte
	}
	inputs := []input{{Type: 1, WVk: 0x11}, {Type: 1, WVk: 0x56}, {Type: 1, WVk: 0x56, DwFlags: 0x0002}, {Type: 1, WVk: 0x11, DwFlags: 0x0002}}
	for i := range inputs {
		procSendInput.Call(1, uintptr(unsafe.Pointer(&inputs[i])), unsafe.Sizeof(inputs[i]))
	}
	return nil
}
