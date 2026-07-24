---
name: chromium-sendkeys
description: >
  Operational runbook for this fork's sendkeys input-injection spike
  (see //CHROMIUM_SENDKEYS_SPEC.md) — building the modified Chromium,
  launching it with CHROMIUM_SENDKEYS_DIR set, and driving it via
  chromesendkeys.cjs. Full command surface (see the §3b catalog): input
  (type/key/click/rightclick), navigation + tabs (goto/newtab/newwindow/
  selecttab/closetab/listtabs), scripting (eval/getdom/http/waitfor),
  observation (screenshot/netlog), and microphone audio injection
  (AUDIOSTART/PLAYWAV/AUDIOSTOP — mic faked by default, real camera kept).
  All without CDP or OS-level UI automation. Trigger on: "build the sendkeys
  spike", "launch chromium with sendkeys", "inject keys/clicks into chromium",
  "drive this chromium build for e2e", "inject microphone audio", or any
  mention of CHROMIUM_SENDKEYS_DIR / chromesendkeys.cjs.
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
# GOTO/EVAL/etc. always target the ACTIVE tab, so selecttab + goto = full control.
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys newtab "https://example.com"  # new foreground tab
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys newwindow "https://x.com"     # new window
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys selecttab 2                    # activate tab by index
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys closetab 2                     # close tab by index (omit = active)
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys listtabs   # -> {"ok":true,"value":[{index,title,url,active}]}

# Network log (resource-load-completion only, not a live interceptor):
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys netlog start
# ... drive the page ...
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys netlog stop /tmp/netlog.json
```

Or set `--dir` once via `export CHROMIUM_SENDKEYS_DIR=~/chrome-agent-sendkeys`
and drop `--dir` from every call.

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

Video server (`VIDEOSTART:<port>`, its own port): *(not built yet — audio slice shipped first)*
- `ws://127.0.0.1:<port>/cam`  — **you send** frames in → becomes the tab's camera.
- `ws://127.0.0.1:<port>/vtap` — **you receive** the tab's rendered video frames out.

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
the bridge** — while the **real camera is untouched** (that needs `--use-fake-device-for-media-stream`,
which the fork does NOT set). The fork also forces the audio service **in-process**
(`--disable-features=AudioServiceOutOfProcess`) so the browser-process bridge and the fake capture
stream share one process. You pass none of this — just launch the fork normally (`chromeagent`) and
issue the spool commands below. Until you arm/stream, the mic is **silent** (not beeping).

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
> `fake_audio_input_stream.cc`; `net/server/web_socket_encoder.cc` accepts binary frames; the
> mic-only default (`kUseFakeAudioInputOnly`, `media_switches` + `audio_manager_base.cc`) keeps the
> real camera. STILL TO BUILD (same pattern, trigger "build the tap and video bridges"): `/tap`
> (receive tab audio via `audio_loopback_stream_broker` + a binary-ENCODE addition to
> `net/server/web_socket`) and the VIDEO server (`/cam` send + `/vtap` via `CopyFromSurface`). For a
> fake CAMERA today (launch-flag only, replaces the real webcam):
> `--use-fake-device-for-media-stream --use-file-for-fake-video-capture=/abs/v.y4m` — frames must be
> valid I420/Y4M at a supported size or nothing renders.

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

The complete surface (17 commands). `verb` = the `chromesendkeys.cjs` subcommand;
`SPOOL:` = the raw line it writes (also usable via `send <line>` or stage-then-rename).
Commands with a `results/<id>.json` return value are marked **←reads back**.

| verb (CLI) | raw spool line | what it does | WHEN an agent uses it |
|---|---|---|---|
| `goto <url>` | `GOTO:<url>` | navigate the active tab's main frame | first step of almost any task — get to the page |
| `type <text>` | `TEXT:<text>` | type text, one synthetic key event per char, into the focused element | fill a field the caret is already in; a bare line also = TEXT |
| `key <chord>` | `KEY:<chord>` | one key chord: `enter`, `ctrl+a`, `cmd+shift+t`, `tab`… | submit a form, trigger a shortcut, move focus — one chord per line |
| `click <x> <y>` | `CLICK:<x>,<y>` | left mousedown+up at widget-relative px | click something you located by coordinates (from a screenshot/eval rect) |
| `rightclick <x> <y>` | `RIGHTCLICK:<x>,<y>` | right click → opens the **native** context menu | only when you actually need the OS context menu (it's not a DOM menu) |
| `eval <js>` | `EVAL:<id>\|<js>` | run JS in the page's main frame; JSON result **←reads back** | the workhorse: read/mutate DOM, click by selector (`el.click()`), or `await fetch()` in-page. Prefer this over blind x/y clicks |
| `getdom` | `EVAL:<id>\|documentElement.outerHTML` | dump the DOM **←reads back** | inspect page structure before deciding what to click/type |
| `http <url>` | `EVAL:<id>\|await fetch(...)` | HTTP request **from inside the page** (its cookies/origin) **←reads back** | call an API as the logged-in page — no separate auth |
| `waitfor <ms> <js>` | `WAITFOR:<ms>\|<id>\|<js>` | poll a JS predicate every 100ms until truthy or timeout **←reads back** | wait for SPA content/navigation to settle before the next step (don't sleep) |
| `newtab [url]` | `NEWTAB:<url>` | open a foreground tab | parallel context, or open a link without losing the current tab |
| `newwindow [url]` | `NEWWINDOW:<url>` | open a new window | separate window when tabs won't do |
| `selecttab <i>` | `SELECTTAB:<i>` | activate tab by index | **GOTO/EVAL/type always hit the ACTIVE tab**, so selecttab + action = target any tab |
| `closetab [i]` | `CLOSETAB:<i>` | close tab by index (omit = active) | clean up; closing the active tab is safe (no crash) |
| `listtabs` | `LISTTABS:<id>` | array of `{index,title,url,active}` **←reads back** | discover what's open before selecttab/closetab |
| `screenshot <path>` | `SCREENSHOT:<path>` | PNG of the current surface to `<path>` | capture visual state to reason over, or to find click coordinates |
| `netlog start` | `NETLOG:START` | begin recording resource-load completions | before driving a flow you want the network trace of |
| `netlog stop <path>` | `NETLOG:STOP:<path>` | stop, write JSON array of loads to `<path>` | after the flow — completion records only (no headers/body; not a live interceptor) |
| `send AUDIOSTART:<port>` | `AUDIOSTART:<port>` | open the `/mic` WebSocket, arm the mic bridge | inject microphone audio into a call/page — then stream int16 mono 48 kHz to `ws://127.0.0.1:<port>/mic` |
| `send PLAYWAV:<path>` | `PLAYWAV:<path>` | push a 16-bit PCM WAV into the mic, one-shot | speak a prerecorded/generated WAV into the page (no socket) |
| `send AUDIOSTOP` | `AUDIOSTOP` | tear down the mic server, disarm + flush | done injecting audio |

Rules of thumb for the agent:
- **Prefer `eval` over `click x y`** when a selector exists — coordinates are brittle, `el.click()`/setting `.value` is not. Use screenshots + coordinates only when there's no stable selector (canvas, native UI).
- **Always `waitfor` after a navigation or an action that loads content** instead of guessing a delay.
- **`selecttab` before acting on a non-active tab** — every action command targets the active tab only.
- **Read-back commands** (`eval`/`getdom`/`http`/`waitfor`/`listtabs`) block in the CLI and print JSON; the raw spool writes `results/<id>.json` (poll + delete it yourself if bypassing the CLI).
- **Audio needs no launch flag** (mic is faked by default, camera stays real) — just `send AUDIOSTART:<port>` and stream.

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
