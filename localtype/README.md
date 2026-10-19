# LocalType

A free, Windows-first, local voice dictation app written in Go.

## Current MVP

- Open a microphone setup window at launch and keep a tray icon for app status and quitting.
- Choose from the installed DirectShow microphones. The selected device is saved and restored next time.
- Show a live input level meter and scrolling signal waveform while the selected microphone is active.
- Keep the audio stream on-device; audio is buffered in memory only while dictation is active.
- Hold **Ctrl+Shift+Space** to record; release any key in the chord to stop and transcribe.
- Play a short, distinct tone when recording starts and stops.
- Remove common spoken fillers and adjacent stutters, and format clearly spoken numbered steps and introduced lists locally before pasting.
- Use the window's dictation button or hotkey to start and stop recording.
- Paste the result into the application that had focus before transcription.
- The English Whisper `small.en` model (about 465 MiB) is downloaded once into `%APPDATA%\LocalType` and checked against its published SHA-256.

## Speech engine

The MVP captures audio with `ffmpeg`'s DirectShow support and transcribes it with `whisper.cpp`, both locally. The included `setup.ps1` installs FFmpeg and the CUDA-enabled Windows build of Whisper.cpp into your user profile and adds them to your user `PATH`. On the first dictation, LocalType downloads the English `small.en` model into its app data folder and verifies its SHA-256. No cloud speech service is used. The model and runtime are separate third-party components.

To build and run:

```powershell
go build -o LocalType.exe .
.\LocalType.exe
```

To stop the app, use **Quit** in the tray menu. The recording limit is five minutes. App logs are written through Go's standard logger to the process output.

## Current limitations

- English `small.en` model only.
- Transcription starts after recording ends; the live waveform is a microphone level preview, not live captions.
- Cleanup is intentionally conservative: it removes common fillers and nearby repeated words or short phrases, and formats clearly introduced or numbered lists. It does not rewrite sentence meaning. Punctuation editing, a custom dictionary, profile/tone adaptation, and settings UI are not implemented yet.
