# pxi for Mac

A native window for px0: the same px0 binary from this repo, run by a small Swift
app (WKWebView, the engine Safari uses). No Electron, no browser tab.

## Build and install

```bash
macos/build.sh             # builds macos/dist/pxi.app (Apple silicon + Intel)
macos/build.sh --install   # and copies it to /Applications
```

Needs Go, Node and the Xcode command line tools. The app is ad-hoc signed for
local use; on another Mac, right-click it and choose **Open** the first time.

## How it works

- pxi starts the bundled `px0` as a workbench on `127.0.0.1:47770` with your login
  shell's environment, so `git`, `gh`, `claude`, `codex` and tokens such as
  `GITHUB_TOKEN` are found as in a terminal.
- If a px0 workbench is already running (the `px0` CLI), pxi uses it and leaves it
  running on quit. Otherwise quitting pxi stops px0 and every workspace it opened.
- px0's own pages open in pxi windows (as tabs); outside links open in your browser.
- Closing a window or quitting asks first when px0 has unsaved edits or an agent is
  still changing files.
- Log: `~/Library/Logs/pxi/px0.log` (Help → Show px0 Log).

## Opening folders

**File → Open Folder…** (Cmd+O), drop a folder on the Dock icon, or from a terminal:

```bash
open -a pxi ~/code/platform
```

## Shortcuts

Every px0 shortcut works; the page gets each key before the menu. pxi's own:

| Keys | Action |
|---|---|
| Cmd+O | Open folder |
| Cmd+T | New tab |
| Cmd+Shift+N | New window |
| Cmd+Shift+W | Close window (Cmd+W closes px0's file tab) |
| Cmd+Shift+H | Back to the workbench |
| Cmd+Option+R | Reload page |
| Cmd+= / Cmd+- / Cmd+0 | Zoom |

## Testing

`PXI_SNAPSHOT=<dir> open --env PXI_SNAPSHOT=<dir> macos/dist/pxi.app` saves a PNG of
every page it loads into `<dir>`, for checking the app from scripts.
