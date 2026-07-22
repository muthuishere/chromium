---
name: chromium-sendkeys
description: >
  Operational runbook for this fork's sendkeys input-injection spike
  (see //CHROMIUM_SENDKEYS_SPEC.md) — building the modified Chromium,
  launching it with CHROMIUM_SENDKEYS_DIR set, driving it via
  chromesendkeys.cjs (type/key/click/rightclick/goto/screenshot), and
  verifying delivery without relying on CDP or OS-level UI automation.
  Trigger on: "build the sendkeys spike", "launch chromium with sendkeys",
  "inject keys/clicks into chromium", "test the chromium sendkeys watcher",
  "drive this chromium build for e2e", or any mention of
  CHROMIUM_SENDKEYS_DIR / chromesendkeys.cjs.
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

## 3a. Audio in / out — raw PCM over WebSocket (no virtual driver)

Design decision: audio is streamed as **raw PCM over local WebSockets**, NOT through a
virtual audio driver (no BlackHole). Two endpoints, bound **127.0.0.1 only** (expose via a
cloudflared tunnel if you ever need it remote — never bind 0.0.0.0 in the browser):

- `ws://127.0.0.1:<port>/mic` — **you send** PCM in → becomes the tab's microphone.
- `ws://127.0.0.1:<port>/tap` — **you receive** the tab's audio output PCM out.

Format: interleaved **int16, 48 kHz stereo** (first WS text frame may override:
`{"rate":48000,"channels":2}`). Port via `CHROMIUM_AUDIO_WS_PORT` (default 8778).

**Watcher commands** (arm/disarm; the audio bytes flow over the WS, not the spool):

```bash
# SEND a WAV as the tab mic (this one works TODAY via flags, no rebuild:
#   --use-fake-device-for-media-stream --use-file-for-fake-audio-capture=/abs/file.wav ):
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys playwav /abs/path.wav

# Live raw-PCM streaming (ships with the audio-bridge build):
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys micstream on    # open ws /mic  (send)
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys tapaudio  on    # open ws /tap  (receive)
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys micstream off
node chromesendkeys.cjs --dir ~/chrome-agent-sendkeys tapaudio  off
```

> STATUS: `playwav` (WAV-file mic injection) is available via the built-in fake-audio flags.
> `micstream`/`tapaudio` (the live WebSocket bridge) are implemented by the in-fork audio bridge
> — build with "build the audio websocket bridge". Backed by Chromium's own
> `media/audio/fake_audio_input_stream.cc` (send) and
> `content/browser/media/audio_loopback_stream_broker.cc` (receive) — no external driver.

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

This build ships with two browser protections **disabled in the core** (no
flag needed, and not shown in the UI or `chrome://version`):

- **CORS / same-origin policy is OFF** — cross-origin `fetch`/XHR from any page
  returns the real body. (`chrome/app/chrome_main_delegate.cc` forces
  `--disable-web-security` on unconditionally.)
- **Header Content-Security-Policy is NOT enforced** — pages like LinkedIn that
  send a `connect-src` CSP header no longer block cross-origin requests.
  (`services/network/public/cpp/parsed_headers.cc` skips CSP-header parsing.)
  *Meta-tag CSP is still enforced by Blink — not covered.*

See `//CHROMIUM_SENDKEYS_SPEC.md` → "Security relaxations" for the full
rationale, the exact files, and verification. Treat this as an insecure browser:
don't point it at untrusted sites while logged into anything sensitive.

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
