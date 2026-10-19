package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/getlantern/systray"
	"github.com/lxn/walk"
	d "github.com/lxn/walk/declarative"
)

type audioWindow struct {
	*walk.MainWindow
	microphones *walk.ComboBox
	levelBar    *walk.ProgressBar
	levelLabel  *walk.Label
	waveform    *walk.CustomWidget
	status      *walk.Label
	dictate     *walk.PushButton
	devices     []audioDevice
	selected    string
	refreshing  bool
	done        chan struct{}
}

type localConfig struct {
	Microphone string `json:"microphone"`
}

var (
	audioWindowRefMu sync.RWMutex
	audioWindowRef   *audioWindow
)

func configFile() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "LocalType", "settings.json")
}

func loadSelectedDevice() string {
	path := configFile()
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var cfg localConfig
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	return cfg.Microphone
}

func saveSelectedDevice(value string) error {
	path := configFile()
	if path == "" {
		return fmt.Errorf("cannot locate the LocalType settings directory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(localConfig{Microphone: value}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}

func runAudioWindow() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ui := &audioWindow{selected: loadSelectedDevice(), done: make(chan struct{})}
	definition := d.MainWindow{
		AssignTo: &ui.MainWindow,
		Title:    "LocalType · Microphone",
		Visible:  true,
		MinSize:  d.Size{Width: 520, Height: 450},
		Size:     d.Size{Width: 600, Height: 500},
		Layout:   d.VBox{Margins: d.Margins{Left: 26, Top: 24, Right: 26, Bottom: 22}, Spacing: 11},
		Children: []d.Widget{
			d.Label{Text: "YOUR MICROPHONE", Font: d.Font{PointSize: 16, Bold: true}},
			d.Label{Text: "Choose an input and confirm the live signal before dictating."},
			d.Composite{Layout: d.HBox{Spacing: 8}, Children: []d.Widget{
				d.ComboBox{AssignTo: &ui.microphones, Model: []string{}, StretchFactor: 1, OnCurrentIndexChanged: ui.microphoneChanged},
				d.PushButton{Text: "Refresh", OnClicked: ui.refreshMicrophones},
			}},
			d.Label{Text: "LIVE INPUT LEVEL", Font: d.Font{PointSize: 9, Bold: true}},
			d.Composite{Layout: d.HBox{Spacing: 10}, Children: []d.Widget{
				d.ProgressBar{AssignTo: &ui.levelBar, MinValue: 0, MaxValue: 100, StretchFactor: 1},
				d.Label{AssignTo: &ui.levelLabel, Text: "0%", MinSize: d.Size{Width: 45}},
			}},
			d.CustomWidget{AssignTo: &ui.waveform, MinSize: d.Size{Width: 480, Height: 150}, ClearsBackground: true, Paint: ui.paintWaveform},
			d.Label{AssignTo: &ui.status, Text: "Checking available microphones…"},
			d.Composite{Layout: d.HBox{Spacing: 10}, Children: []d.Widget{
				d.PushButton{AssignTo: &ui.dictate, Text: "Start dictation", OnClicked: toggleRecording},
				d.Label{Text: "Ctrl + Shift + Space", TextAlignment: d.AlignNear},
			}},
			d.Label{Text: "Audio stays on this PC. The meter is live; audio is buffered only during dictation."},
		},
	}
	if err := definition.Create(); err != nil {
		log.Printf("create microphone window: %v", err)
		setStatus("Microphone window could not open")
		close(ui.done)
		return
	}
	audioWindowRefMu.Lock()
	audioWindowRef = ui
	audioWindowRefMu.Unlock()
	ui.Closing().Attach(func(canceled *bool, _ walk.CloseReason) {
		stopAudioSession()
		close(ui.done)
		audioWindowRefMu.Lock()
		audioWindowRef = nil
		audioWindowRefMu.Unlock()
		systray.Quit()
	})
	ui.refreshMicrophones()
	go ui.updateMeter()
	ui.Run()
	var err error
	stopAudioSession()
	select {
	case <-ui.done:
	default:
		close(ui.done)
	}
	audioWindowRefMu.Lock()
	audioWindowRef = nil
	audioWindowRefMu.Unlock()
	if err != nil {
		log.Printf("microphone window: %v", err)
	}
}

func (ui *audioWindow) refreshMicrophones() {
	ffmpeg, err := findFFmpeg()
	if err != nil {
		setStatus(err.Error())
		return
	}
	ffmpegPath = ffmpeg
	devices, err := listAudioDevices(ffmpeg)
	if err != nil {
		setStatus(err.Error())
		return
	}
	ui.devices = devices
	labels := make([]string, len(devices))
	for i, device := range devices {
		labels[i] = device.Name
	}
	ui.refreshing = true
	_ = ui.microphones.SetModel(labels)
	selectedIndex := 0
	for i, device := range devices {
		if device.Input == ui.selected {
			selectedIndex = i
			break
		}
	}
	_ = ui.microphones.SetCurrentIndex(selectedIndex)
	ui.refreshing = false
	ui.chooseDevice(selectedIndex)
}

func (ui *audioWindow) microphoneChanged() {
	if ui.refreshing {
		return
	}
	ui.chooseDevice(ui.microphones.CurrentIndex())
}

func (ui *audioWindow) chooseDevice(index int) {
	if index < 0 || index >= len(ui.devices) {
		return
	}
	device := ui.devices[index]
	if device.Input == ui.selected && currentSession() != nil {
		return
	}
	setStatus("Connecting to " + device.Name + "…")
	if err := selectMicrophone(device); err != nil {
		setStatus("Microphone could not start: " + err.Error())
		return
	}
	ui.selected = device.Input
	setStatus("Live input · " + device.Name)
}

func (ui *audioWindow) updateMeter() {
	ticker := time.NewTicker(60 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ui.done:
			return
		case <-ticker.C:
			s := currentSession()
			if s == nil {
				continue
			}
			level, points, recording := s.snapshot()
			ui.Synchronize(func() {
				if ui.levelBar == nil {
					return
				}
				value := int(math.Round(math.Min(1, level*4) * 100))
				ui.levelBar.SetValue(value)
				_ = ui.levelLabel.SetText(fmt.Sprintf("%d%%", value))
				if recording {
					_ = ui.dictate.SetText("Finish dictation")
				} else {
					_ = ui.dictate.SetText("Start dictation")
				}
				if ui.waveform != nil {
					_ = ui.waveform.Invalidate()
				}
				if level < 0.0004 && ui.status.Text() == "Live input · "+ui.selectedName() {
					_ = ui.status.SetText("Live input · waiting for sound")
				}
				if level >= 0.0004 && strings.Contains(ui.status.Text(), "waiting for sound") {
					_ = ui.status.SetText("Live input · sound detected")
				}
			})
			_ = points
		}
	}
}

func (ui *audioWindow) selectedName() string {
	for _, device := range ui.devices {
		if device.Input == ui.selected {
			return device.Name
		}
	}
	return ""
}

func (ui *audioWindow) paintWaveform(canvas *walk.Canvas, _ walk.Rectangle) error {
	bounds := ui.waveform.ClientBounds()
	if bounds.Width < 2 || bounds.Height < 2 {
		return nil
	}
	background, err := walk.NewSolidColorBrush(walk.RGB(18, 25, 32))
	if err != nil {
		return err
	}
	defer background.Dispose()
	if err := canvas.FillRectangle(background, bounds); err != nil {
		return err
	}
	gridPen, err := walk.NewCosmeticPen(walk.PenSolid, walk.RGB(48, 60, 68))
	if err != nil {
		return err
	}
	defer gridPen.Dispose()
	mid := bounds.Height / 2
	if err := canvas.DrawLine(gridPen, walk.Point{X: 0, Y: mid}, walk.Point{X: bounds.Width, Y: mid}); err != nil {
		return err
	}
	for i := 1; i < 4; i++ {
		y := i * bounds.Height / 4
		if y != mid {
			if err := canvas.DrawLine(gridPen, walk.Point{X: 0, Y: y}, walk.Point{X: bounds.Width, Y: y}); err != nil {
				return err
			}
		}
	}
	s := currentSession()
	if s == nil {
		return nil
	}
	_, history, _ := s.snapshot()
	if len(history) < 2 {
		return nil
	}
	points := make([]walk.Point, len(history))
	for i, sample := range history {
		x := i * (bounds.Width - 1) / (len(history) - 1)
		y := mid - int(math.Max(-1, math.Min(1, sample*4))*float64(bounds.Height)*0.44)
		points[i] = walk.Point{X: x, Y: y}
	}
	wavePen, err := walk.NewCosmeticPen(walk.PenSolid, walk.RGB(52, 211, 153))
	if err != nil {
		return err
	}
	defer wavePen.Dispose()
	return canvas.DrawPolyline(wavePen, points)
}

func postWindowStatus(text string) {
	audioWindowRefMu.RLock()
	ui := audioWindowRef
	audioWindowRefMu.RUnlock()
	if ui == nil || ui.MainWindow == nil {
		return
	}
	ui.Synchronize(func() {
		if ui.status != nil {
			_ = ui.status.SetText(text)
		}
	})
}
