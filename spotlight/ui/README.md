# spotlight-ui

A small launcher window for the spotlight index. Press the hotkey (default **Win+Space**), type, press Enter. It is a [Wails v2](https://wails.io) app (Go + WebView2) and talks to the `indexd` daemon over `127.0.0.1:7788`, so start `indexd` first.

It is a separate Go module so the daemon keeps its two dependencies.

## Build

Requirements: Go 1.24+, the [Wails CLI](https://wails.io/docs/gettingstarted/installation) (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`), and the WebView2 runtime (included in Windows 11). The frontend is plain HTML, so no Node is needed.

```
cd ui
go mod tidy
wails build          # produces build/bin/spotlight-ui.exe
wails dev            # run with live reload while changing the UI
```

## Use

| Key | Action |
| --- | --- |
| hotkey | show / hide the window |
| Up / Down | move the selection |
| Enter | open the file |
| Ctrl+Enter | show the file in its folder |
| Esc | hide |

Results show the file name, folder, a snippet with your words highlighted, and whether it matched on text, meaning, or both.

## Configure

Copy `ui.example.json` to `ui.json` next to the executable (or to `%AppData%\spotlight\ui.json`), or pass `-config path`.

- `hotkey`: `modifier+modifier+key`. Modifiers: `win`, `ctrl`, `alt`, `shift`. Keys: letters, digits, `f1`-`f24`, `space`, `enter`, `tab`, `` ` `` and punctuation. At least one modifier is required. Examples: `alt+space`, `ctrl+shift+f`.
- `daemon_url`, `max_results` (1-100), `hide_on_blur`.

## Notes on the hotkey

- Windows reserves Win+Space (it switches keyboard layouts), so `RegisterHotKey` can't claim it. The launcher installs a low-level keyboard hook instead, swallows the combo before Windows sees it, and cancels the Start menu that a bare Win release would otherwise open.
- A hook cannot see keystrokes sent to an elevated (administrator) window unless the launcher is elevated too, so the hotkey does nothing while such a window has focus.
- Another tool that hooks the same combo (PowerToys Run, Flow Launcher) will conflict. Quit it or choose a different hotkey.
- On Linux and macOS there is no global hotkey support. Bind a desktop shortcut to the executable: launching it again toggles the window.
- If the hotkey can't be registered, the window opens at startup and the footer says why.

## Status

Written without a Windows machine or the Wails modules available, so it has only been compiled against a stand-in for the Wails API. The hotkey matching, config and daemon client have unit tests; the window, hook and focus code need a first run on your machine.
