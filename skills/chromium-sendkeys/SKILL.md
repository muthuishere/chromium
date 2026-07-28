---
name: chromium-sendkeys
description: >
  Operational runbook for this fork's sendkeys input-injection spike
  (see //CHROMIUM_SENDKEYS_SPEC.md) — building the modified Chromium,
  launching it with CHROMIUM_SENDKEYS_DIR set, and driving it via
  chromesendkeys.cjs. Full command surface (see the §3b catalog): input
  (type/key/click/rightclick), navigation + tabs (goto/newtab/newwindow/
  selecttab/closetab/listtabs), scripting (eval/getdom/http/waitfor),
  observation (screenshot/netlog), microphone audio injection
  (AUDIOSTART/PLAYWAV/AUDIOSTOP), and camera video injection
  (VIDEOSTART/VIDEOSTOP — push I420 frames, e.g. an ffmpeg-decoded mp4, as the
  webcam). Both mic and camera are faked by default (each via an independent
  switch — kUseFakeAudioInputOnly / kUseFakeVideoInputOnly — so either can be
  disabled to keep the real device); until you inject, the fake mic is silent
  and the fake camera is black. All without CDP or OS-level UI
  automation. Trigger on: "build the sendkeys spike", "launch chromium with
  sendkeys", "inject keys/clicks into chromium", "drive this chromium build for
  e2e", "inject microphone audio", "inject camera/webcam video", or any mention
  of CHROMIUM_SENDKEYS_DIR / chromesendkeys.cjs.
---

# chromium-sendkeys

Fork-local spike, not upstream Chromium behavior. Full design in
`//CHROMIUM_SENDKEYS_SPEC.md` — read that first if anything here is
ambiguous. This file is the day-to-day runbook: build, launch, inject,
verify, clean up.

## 1. Build

```bash
autoninja -C out/Default chrome
```

- First build after touching `chrome/browser/sendkeys_watcher.{h,cc}` or
  `chrome/browser/chrome_browser_main.cc` will relink `chrome`; expect a
  real Chromium build to take a long time even incrementally (many minutes
  to hours depending on the machine and whether `out/Default` already has a
  warm build). Don't assume a quick turnaround.
- If `out/Default` doesn't exist yet: `gn gen out/Default` first (with
  whatever `args.gn` this checkout normally uses — check for an existing
  `out/*/args.gn` before inventing one).
- Watch for `gn format`/presubmit complaints about `sources` ordering in
  `chrome/browser/BUILD.gn` — the two new files were inserted next to
  `chrome_browser_main.cc`, not in strict alphabetical position (see the
  spec's "known gotchas").

## 2. Launch with the watcher enabled

**Portable launcher (any OS — recommended).** `chromium-agent-launch.cjs`
locates the built binary for the current platform, creates the spool dir,
sets `CHROMIUM_SENDKEYS_DIR`, and spawns the browser with the profile:

```bash
node chromium-agent-launch.cjs https://example.com
```

It resolves the right binary per-OS automatically:

| OS       | built binary                                   |
|----------|------------------------------------------------|
| macOS    | `out/Default/Chromium.app/Contents/MacOS/Chromium` |
| Windows  | `out\Default\chrome.exe`                        |
| Linux    | `out/Default/chrome`                            |

Override any default with an env var: `CHROMIUM_SENDKEYS_OUT` (build dir,
default `out/Default`), `CHROMIUM_SENDKEYS_DIR` (spool, default
`~/chrome-agent-sendkeys`), `CHROMIUM_AGENT_PROFILE` (profile, default
`~/chrome-agent-profile`). Pass extra Chromium flags after `--`.

**Direct launch (macOS, if `chromium-agent` is symlinked onto PATH):**

```bash
mkdir -p ~/chrome-agent-sendkeys                       # watcher refuses to start if missing
CHROMIUM_SENDKEYS_DIR="$HOME/chrome-agent-sendkeys" \
  chromium-agent \
  --user-data-dir="$HOME/chrome-agent-profile"
```

- The spool directory **must already exist** — the watcher errors (logged,
  not a crash) and does not start if it's missing, and it never creates it
  (the portable launcher above creates it for you).
- Confirm the watcher started: look for `sendkeys watcher started, spool
  dir ...` in stdout/stderr (a plain `LOG(INFO)`, not gated behind a flag
  for this spike — always emitted when the env var is set and the directory
  exists).
- A tab must exist and be the active tab in the last-activated browser
  window before any command is delivered (`GetLastActiveBrowserWindowInterfaceWithAnyProfile()`
  needs a window; `GetActiveTabInterface()` needs a tab) — open one first if
  launching to a blank state.

## 3. Inject commands

Via the producer CLI (repo root, no npm deps):

```bash
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys goto  "https://example.com"
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys type  "hello world"
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys key   "ctrl+a"
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys click 400 300
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys rightclick 400 300
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys screenshot /tmp/shot.png

# DOM read/write and HTTP -- all three are just JS from the page's side,
# and these subcommands block and print the result (polled from results/):
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys getdom
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys eval "document.title"
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys eval "document.querySelector('#x').value = 'hi'"
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys http "https://example.com/api"

# Wait for a condition (polls every 100ms in-browser, up to the timeout):
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys waitfor 5000 "!!document.querySelector('.loaded')"

# Tab / window management (act on the last-active browser window).
# GOTO/EVAL/etc. default to the ACTIVE tab; to target a SPECIFIC tab by UUID,
# prefix any command line with TAB:<tabId>| (see the per-tab targeting note below).
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys newtab "https://example.com"  # new foreground tab; acks {"ok":true,"tabId":"<uuid>"}
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys newwindow "https://x.com"     # new window
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys selecttab 2                    # activate tab by index
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys closetab 2                     # close tab by index (omit = active)
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys listtabs   # -> [{tabId,window,index,title,url,active}] across ALL windows

# Network log (resource-load-completion only, not a live interceptor):
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys netlog start
# ... drive the page ...
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys netlog stop /tmp/netlog.json
```

Or set `--dir` once via `export CHROMIUM_SENDKEYS_DIR=~/chrome-agent-sendkeys`
and drop `--dir` from every call.

**Per-tab targeting (`TAB:<tabId>|`).** Every tab carries a stable UUID. Prefix
any command line with `TAB:<tabId>|` to pin it to that tab regardless of window
focus or stray tabs — prefixable on `GOTO`/`EVAL`/`EVALASYNC`/`CLICK`/`TEXT`/
`KEY`/`SCREENSHOT`/`WAITFOR`/`NETLOG`. Learn a tab's UUID from `newtab`'s ack
(`{"ok":true,"tabId":"<uuid>"}`) or `listtabs` (each row has `tabId`). An
**unknown** tabId writes `{"ok":false,"error":"unknown tabId"}` and never falls
back to the last-active tab (this is what makes multi-tab driving race-free — no
prefix keeps the old last-active default). Send it as a raw line, e.g.
`node chromesendkeys.cjs --dir "$S" send "TAB:<uuid>|GOTO:https://example.com"`.

**`EVALASYNC:<id>|<body>`** runs an async function body natively in the fork —
it may `await` and `return`, and acks `{"ok":true,"value":...}` /
`{"ok":false,"error":...}`. Use it instead of `eval` whenever you need to await
(e.g. `await fetch(...)`); plain `eval` is single-expression, no await. Send via
`send "EVALASYNC:<id>|return (await fetch('/api')).status"`.

## 3a. Audio & video in / out — raw media over WebSocket (no virtual driver)

Design decision: audio AND video are streamed as **raw frames over local WebSockets**, NOT
through a virtual audio/camera driver (no BlackHole, no OBS virtual-cam). Each stream is its own
**WebSocket-backed virtual device** — a "driver" implemented as a WebSocket, replacing what a
kernel/OS audio or camera driver would do. **TWO independent servers** — audio and video —
each **OFF by default**, each **started on its own port** and **stopped independently** (run
audio-only, video-only, or both). Each server is **two-way**. Bound **127.0.0.1 only** (expose
via a cloudflared tunnel if ever needed — never bind 0.0.0.0).

Audio server (`AUDIOSTART:<port>`):
- `ws://127.0.0.1:<port>/mic`  — **you send** PCM in → becomes the tab's microphone. **BUILT + VERIFIED.**
- `ws://127.0.0.1:<port>/tap`  — **you receive** the tab's audio output PCM out. *(not built yet)*

Video server (`VIDEOSTART:<port>`, its own port):
- `ws://127.0.0.1:<port>/cam`  — **you send** frames in → becomes the tab's camera. **BUILT + VERIFIED.**
- `ws://127.0.0.1:<port>/vtap` — **you receive** the tab's rendered video frames out. *(not built yet)*

`/cam` frame format: each binary WebSocket message = **`[int32 LE width][int32 LE height][tightly-packed
I420 bytes]`** (even dimensions). No launch flag needed — the fork defaults `--use-fake-video-input-only`
(fakes ONLY the camera; real mic untouched) and forces the capture service in-process. Enumerates as
"Agent Virtual Camera". Stream an actual MP4 by decoding it to I420 with ffmpeg and pushing the frames:
`ffmpeg -stream_loop -1 -re -i movie.mp4 -vf scale=640:480 -pix_fmt yuv420p -f rawvideo -` → prepend the
8-byte `[w][h]` header per 460800-byte (640×480) frame → send to `/cam` (verified: an H.264 mp4's
red/green/blue frames came out of getUserMedia({video}) with motion).

Formats: `/mic` audio = **binary WebSocket frames of interleaved int16 PCM, mono, 48 kHz** (the
fork downmixes/resamples internally, so any rate/channel input is fine but 48 kHz mono is the
zero-conversion path). Video (when built) = raw **I420** with a JSON handshake frame, e.g.
`{"width":1280,"height":720,"fps":30,"format":"I420"}`.

**Per-tab?** RECEIVE (`/tap`, `/vtap`) is per-tab (bound to the active/selected WebContents).
SEND (`/mic`, `/cam`) is a browser-global fake device — whichever tab calls getUserMedia
consumes it, so use `selecttab` to control which tab captures. You can't feed two tabs
different streams from one source.

**NO LAUNCH FLAG NEEDED (mic in).** The fork defaults **`--use-fake-audio-input-only`** ON (forced
in `chrome_main_delegate.cc` for the browser process), so `getUserMedia()`'s **microphone is already
the bridge**. (The camera has its own independent default — see `/cam` below; the two switches don't
affect each other, so faking the mic never touches the camera factory and vice-versa.) The fork also
forces the audio service **in-process** (`--disable-features=AudioServiceOutOfProcess`) so the
browser-process bridge and the fake capture stream share one process. You pass none of this — just
launch the fork normally (`chromeagent`) and issue the spool commands below. Until you arm/stream,
the mic is **silent** (not beeping).

> **Default note:** the fork fakes BOTH the mic (`--use-fake-audio-input-only`) AND the camera
> (`--use-fake-video-input-only`) by default, so `chromeagent` presents a silent mic + black camera
> until you inject. The two switches are independent (faking one leaves the other's real device
> alone), but both are force-appended in `chrome_main_delegate.cc`. To keep a REAL mic or camera by
> default, remove that switch's default there.

**Command surface = raw spool lines.** `chromesendkeys.cjs` has no dedicated `audio` verb, but its
generic **`send <RAWLINE>`** verb publishes any spool line — so
`chromesendkeys.cjs --dir ~/chrome-agent-sendkeys send AUDIOSTART:38701` is the one-liner CLI path
(equivalent to the stage-then-rename below). BUILT + VERIFIED end-to-end (a 440 Hz int16 sine sent
to `/mic` read back by `getUserMedia()` at the exact expected RMS; also verified live in the running
fork by an open port):

```text
AUDIOSTART:<port>     # boot the localhost mic WebSocket on 127.0.0.1:<port>, arm the bridge
PLAYWAV:/abs/a.wav    # one-shot: decode a 16-bit PCM WAV and push it into the mic (no socket needed)
AUDIOSTOP             # tear the server down, disarm + flush the bridge
```

Only ONE audio server runs at a time — a second `AUDIOSTART` replaces (tears down) the first.

**How to handle it, concretely** (this is exactly how it was driven live):

```bash
S=~/chrome-agent-sendkeys                       # the spool dir the fork watches
# 1) arm the mic WebSocket on a port (stage-then-rename; never write directly in the dir)
printf 'AUDIOSTART:38701\n' > "$S/.stage" && mv "$S/.stage" "$S/mic-on.txt"
#    confirm it's listening:  nc -z 127.0.0.1 38701  -> open
# 2) stream binary int16 mono 48 kHz PCM to it from any client (page/CLI/python):
#      const ws = new WebSocket("ws://127.0.0.1:38701/mic");
#      ws.binaryType = "arraybuffer"; ws.send(int16buf.buffer);
# 3) or push a WAV file one-shot (no socket): 
printf 'PLAYWAV:/abs/voice.wav\n' > "$S/.stage" && mv "$S/.stage" "$S/say.txt"
# 4) done — disarm:
printf 'AUDIOSTOP\n' > "$S/.stage" && mv "$S/.stage" "$S/mic-off.txt"
```

> STATUS (2026-07-24): **shipped in `main` and verified.** `AUDIOSTART`/`AUDIOSTOP` (the `/mic`
> `net::HttpServer`) + `PLAYWAV`, backed by `media/audio/agent_audio_bridge.{h,cc}` feeding
> `fake_audio_input_stream.cc`; `net/server/web_socket_encoder.cc` handles binary frames (decode +
> encode). Mic faked by `kUseFakeAudioInputOnly` (`media_switches` + `audio_manager_base.cc`).
> **`/cam` also shipped** (VIDEOSTART/VIDEOSTOP + a fake `VideoCaptureDevice` fed by
> `media/capture/video/agent_video_bridge`; `kUseFakeVideoInputOnly` fakes the camera independently of
> the mic; capture forced in-process via `content_features.cc` + shared-memory buffers via
> `--disable-video-capture-use-gpu-memory-buffer`). Verified with a real ffmpeg-decoded mp4.
> STILL TO BUILD (trigger "build the tap and video bridges"): `/tap` (receive tab audio via
> `audio_loopback_stream_broker`, uses the binary encode) and `/vtap` (receive rendered frames via
> `CopyFromSurface`).

Raw protocol (bypassing the CLI, e.g. from another language): stage lines
into a file, then atomically publish — never write directly into the spool
directory:

```bash
printf 'GOTO:https://example.com\nTEXT:hi\nKEY:enter\n' > ~/chrome-agent-sendkeys/.manual-staging
mv ~/chrome-agent-sendkeys/.manual-staging \
   "$HOME/chrome-agent-sendkeys/$(date +%s)-manual.txt"
```

`mv`/`rename()` within the same filesystem is atomic on POSIX — this is why
the protocol requires stage-then-rename rather than direct writes or
appends inside the watched directory.

## 3b. Command catalog — every command + when an agent reaches for it

The complete surface. `verb` = the `chromesendkeys.cjs` subcommand (rows marked
*(raw)* have no dedicated verb — send the spool line via `send <line>` or
stage-then-rename); `SPOOL:` = the raw line written. Commands with a
`results/<id>.json` return value are marked **←reads back**.

| verb (CLI) | raw spool line | what it does | WHEN an agent uses it |
|---|---|---|---|
| `goto <url>` | `GOTO:<url>` | navigate the active tab's main frame | first step of almost any task — get to the page |
| `type <text>` | `TEXT:<text>` | type text, one synthetic key event per char, into the focused element | fill a field the caret is already in; a bare line also = TEXT |
| `key <chord>` | `KEY:<chord>` | one key chord: `enter`, `ctrl+a`, `cmd+shift+t`, `tab`… | submit a form, trigger a shortcut, move focus — one chord per line |
| `click <x> <y>` | `CLICK:<x>,<y>` | left mousedown+up at widget-relative px | click something you located by coordinates (from a screenshot/eval rect) |
| `rightclick <x> <y>` | `RIGHTCLICK:<x>,<y>` | right click → opens the **native** context menu | only when you actually need the OS context menu (it's not a DOM menu) |
| `eval <js>` | `EVAL:<id>\|<js>` | run a single JS expression in the page's main frame (no await); JSON result **←reads back** | read/mutate DOM, click by selector (`el.click()`). Prefer this over blind x/y clicks. Use `evalAsync` when you need to `await` |
| *(raw)* | `EVALASYNC:<id>\|<body>` | run `<body>` as an **async function body** (may `await`/`return`); acks `{ok,value}`/`{ok,error}` **←reads back** | native eval-await: `await fetch()` in-page, or any async DOM wait. Replaces the old base64 `eval(atob())` async hack |
| `getdom` | `EVAL:<id>\|documentElement.outerHTML` | dump the DOM **←reads back** | inspect page structure before deciding what to click/type |
| `http <url>` | `EVAL:<id>\|<sync-XHR>` | HTTP request **from inside the page** (its cookies/origin) **←reads back** | call an API as the logged-in page — no separate auth |
| `waitfor <ms> <js>` | `WAITFOR:<ms>\|<id>\|<js>` | poll a JS predicate every 100ms until truthy or timeout **←reads back** | wait for SPA content/navigation to settle before the next step (don't sleep) |
| *(raw)* | `TAB:<tabId>\|<line>` | pin `<line>` to the tab with UUID `<tabId>` (unknown id → `{ok:false,error:"unknown tabId"}`, never last-active) | race-free multi-tab driving: target a specific tab regardless of focus. Prefix on GOTO/EVAL/EVALASYNC/CLICK/TEXT/KEY/SCREENSHOT/WAITFOR/NETLOG |
| `newtab [url]` | `NEWTAB:<id>\|<url>` | open a foreground tab; **acks** `{ok:true,tabId}` **←reads back** | parallel context; capture the returned `tabId` to address the tab later. Bare `NEWTAB:<url>` = fire-and-forget |
| `newwindow [url]` | `NEWWINDOW:<url>` | open a new window | separate window when tabs won't do |
| `selecttab <i>` | `SELECTTAB:<i>` | activate tab by index | with no `TAB:` prefix, GOTO/EVAL/type hit the ACTIVE tab, so selecttab + action = target any tab (or use `TAB:<tabId>\|`) |
| `closetab [i]` | `CLOSETAB:<i>` | close tab by index (omit = active) | clean up; closing the active tab is safe (no crash) |
| `listtabs` | `LISTTABS:<id>` | array of `{tabId,window,index,title,url,active}` across ALL windows **←reads back** | discover what's open + each tab's UUID before selecttab/closetab or a `TAB:` prefix |
| `screenshot <path>` | `SCREENSHOT:<id>\|<path>` | PNG to `<path>`; **acks** `{ok,path,bytes}`/`{ok,error}`; captures **background** tabs **←reads back** | capture visual state or find click coordinates. Bare `SCREENSHOT:<path>` = fire-and-forget. **Never write inside the spool dir** — the watcher deletes stray files; use `/tmp` |
| `netlog start` | `NETLOG:START` | begin recording resource-load completions | before driving a flow you want the network trace of |
| `netlog stop <path>` | `NETLOG:STOP:<path>` | stop, write JSON array of loads to `<path>` | after the flow — completion records only (no headers/body; not a live interceptor) |
| `send AUDIOSTART:<port>` | `AUDIOSTART:<port>` | open the `/mic` WebSocket, arm the mic bridge | inject microphone audio into a call/page — then stream int16 mono 48 kHz to `ws://127.0.0.1:<port>/mic` |
| `send PLAYWAV:<path>` | `PLAYWAV:<path>` | push a 16-bit PCM WAV into the mic, one-shot | speak a prerecorded/generated WAV into the page (no socket) |
| `send AUDIOSTOP` | `AUDIOSTOP` | tear down the mic server, disarm + flush | done injecting audio |
| `send VIDEOSTART:<port>` | `VIDEOSTART:<port>` | open the `/cam` WebSocket, arm the fake camera | inject webcam video into a call/page — then stream `[int32 w][int32 h][I420]` frames to `ws://127.0.0.1:<port>/cam` (e.g. ffmpeg-decoded mp4) |
| `send VIDEOSTOP` | `VIDEOSTOP` | tear down the camera server, disarm + flush | done injecting video |

Rules of thumb for the agent:
- **Prefer `eval` over `click x y`** when a selector exists — coordinates are brittle, `el.click()`/setting `.value` is not. Use screenshots + coordinates only when there's no stable selector (canvas, native UI).
- **Always `waitfor` after a navigation or an action that loads content** instead of guessing a delay.
- **Target a specific tab** either by `selecttab <i>` first (an un-prefixed command hits the active tab), or — race-free — by prefixing `TAB:<tabId>|` on the command itself (get the `tabId` from `newtab`'s ack or `listtabs`). Prefer `TAB:` when multiple agents/tabs are in play.
- **Read-back commands** (`eval`/`evalAsync`/`getdom`/`http`/`waitfor`/`listtabs`, plus `newtab`/`screenshot` acks) block in the CLI and print JSON; the raw spool writes `results/<id>.json` (poll + delete it yourself if bypassing the CLI).
- **Audio and video need no launch flag** — mic and camera are both faked by default (silent mic / black camera until you inject). `send AUDIOSTART:<port>` then stream int16 PCM to `/mic`; `send VIDEOSTART:<port>` then stream `[w][h][I420]` frames to `/cam`.

## 4. Verify delivery

Prefer directory-state checks over screenshots for confirming *delivery*
(a file appears, then disappears within ~1s — consistent with at-most-once,
delete-before-dispatch semantics):

```bash
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys send "TEXT:probe-$(date +%s)"
ls ~/chrome-agent-sendkeys   # should be empty again within ~1s
```

To verify the *effect* (did the page actually receive it), use `SCREENSHOT:`
itself as the verification step rather than an OS-level screen capture —
it goes through the same `CopyFromSurface` path the feature is built on,
and avoids the terminal spike's documented gotcha where blind
window-raising (`osascript ... "first process whose unix id is $PID"`) can
grab the wrong window on a machine with multiple instances running:

```bash
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys screenshot /tmp/verify.png
# then read /tmp/verify.png
```

## Tests

- **CLI protocol (fast, no browser, no build):**
  `node --test chromesendkeys.cjs` → runs `chromesendkeys.test.cjs`, which
  spawns the CLI against a temp spool dir and asserts each verb's published
  line encoding + the stage-vs-publish semantics (incl. a regression guard
  that action verbs auto-push). Node's built-in runner, zero deps.
- **C++ trigger parser (gtest):** `sendkeys_watcher_unittest.cc` tests
  `sendkeys::internal::ParseTrigger` (modifiers, synonyms, named keys, single
  chars, case-insensitivity, invalid input). Build + run:
  `autoninja -C out/Default unit_tests && ./out/Default/unit_tests
  --gtest_filter='SendKeys*'`. (The `unit_tests` link is large; the CLI tests
  above cover the protocol layer without it.)

## 5. Clean up

- Kill the Chromium process (`PostMainMessageLoopRun()` calls `Stop()`,
  which joins the watcher thread cleanly on normal shutdown — no orphaned
  thread on a clean quit).
- Remove the spool directory and any throwaway `--user-data-dir` profile
  once done: `rm -rf ~/chrome-agent-sendkeys "$HOME/chrome-agent-profile"`.

## Security relaxations baked in (agent build)

This build relaxes several browser protections **in the core** (no flag needed):

- **CORS is OFF, but the Origin header is PRESERVED.** Cross-origin `fetch`/XHR
  from any page returns the real body (simple *and* preflighted/custom-header
  requests). We do **not** use `--disable-web-security` — that strips the Origin
  header and breaks OAuth (Teams/MSAL 400-loops). Instead Blink's same-origin
  policy stays on (Origin sent normally) and only the network CORS *checks* are
  made permissive: `services/network/cors/cors_url_loader.cc` (response check)
  and `services/network/cors/preflight_controller.cc` (preflight).
  `chrome/app/chrome_main_delegate.cc` intentionally does NOT force the switch.
- **Header Content-Security-Policy is NOT enforced** —
  `services/network/public/cpp/parsed_headers.cc` skips CSP-header parsing.
  *Meta-tag CSP is still enforced by Blink — not covered.*
- **All permission prompts auto-grant** — geolocation, notifications, camera,
  mic, clipboard, etc. never prompt
  (`components/permissions/permission_context_base.cc`).
- **Keychain-free profile (macOS)** — the OSCrypt key is read from a file
  (`$CHROMIUM_AGENT_OSCRYPT_KEY_FILE` / `~/.config/chromium-agent/oscrypt.key`)
  before the Keychain, so no "Safe Storage" prompt; seed it with another
  browser's key to decrypt a copied profile
  (`components/os_crypt/common/keychain_password_mac.mm`).

Because CORS-off keeps the Origin header and SOP intact, auth flows like
**Microsoft Teams/Office** log in normally. See `//CHROMIUM_SENDKEYS_SPEC.md` →
"Security relaxations" for full rationale, exact files, and verification. Still
an insecure browser — don't point it at untrusted sites while logged into
anything sensitive.

## Known limitations to keep in mind while testing

See `//CHROMIUM_SENDKEYS_SPEC.md`'s "Known gotchas / limitations" section in
full. The two most likely to bite during manual testing:

- `CLICK:`/`RIGHTCLICK:` are raw pixel coordinates, not element
  ids/selectors — you have to know where on the page to click. Use
  `eval "document.querySelector(sel).getBoundingClientRect()"` first to
  find coordinates if needed.
- `RIGHTCLICK:` opens a real native context menu that this feature cannot
  itself interact with or dismiss; a stray one will sit there until the
  page is clicked elsewhere or `Escape` is sent via `KEY:escape`.
- `eval`/`getdom`/`http`/`waitfor` run through `ExecuteJavaScriptForTests()`
  — labeled test-only in Chromium's own header, used deliberately anyway
  (see the spec). Result files land in `<spool>/results/*.json` and are
  deleted by the CLI once read; if a call is interrupted before reading its
  result, that file is orphaned (no TTL) — check `results/` if things seem
  to pile up.
- `netlog` only sees *completed* resource loads (url/method/mime/status/
  net_error) — no headers, no body, no in-flight requests, no
  blocking/modifying.
