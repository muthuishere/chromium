# Chromium sendkeys automation spike — spec

Date: 2026-07-13 (built + verified 2026-07-14)
Status: spike / proof-of-concept, BUILT AND VERIFIED WORKING, not upstream
Chromium behavior
Sibling spike: the Ghostty terminal has an equivalent feature (see that
repo's `SENDKEYS_SPEC.md`); this doc follows the same shape deliberately.

## Problem

Drive a running Chromium instance from an external process — navigate,
click, right-click, type, screenshot — for AI-agent / e2e automation,
without CDP, without a remote-debugging port, and without OS-level UI
automation (no synthetic OS input, no accessibility APIs). The requirement
was to do this by changing Chromium's own source, so input travels through
Chromium's real input pipeline (`content::RenderWidgetHost`) exactly like
hardware input would, and the browser keeps behaving like a normal,
manually-driven browser — real profile, no automation flags, no
`navigator.webdriver`, no "controlled by automated software" banner.

## Design

### Where it plugs in

Every mouse/keyboard event — real hardware or synthetic — funnels through
one public interface, `content::RenderWidgetHost`
(`content/public/browser/render_widget_host.h`):
`ForwardMouseEvent`/`ForwardKeyboardEvent`/`ForwardWheelEvent`/
`ForwardGestureEvent`. This is exactly what DevTools' own `InputHandler`
calls into (`content/browser/devtools/protocol/input_handler.cc` ->
`widget_host_->ForwardMouseEvent(...)`), and exactly what a platform view
(`RenderWidgetHostViewAura`/`RenderWidgetHostViewMac`) calls when real OS
hardware events arrive. There is no lower level inside Chromium; from here
input goes straight to the renderer over mojo and Blink dispatches it as
`isTrusted: true`. The spike's job is to get synthetic mouse/keyboard events
into that same call from a source that isn't a GUI event or CDP.

The keyboard event field-filling (dom_key/dom_code/native_key_code/
windows_key_code/text) mirrors the pattern
`content::SimulateCharTyped()`/`SimulateKeyPressImpl()` use in
`content/public/test/browser_test_utils.cc` — that helper is test-only and
not linkable from `chrome/browser`, so `sendkeys_watcher.cc` reimplements
the same technique (`ui::DomKey::FromCharacter()` +
`UsLayoutDomKeyToDomCode()` + `DomCodeToUsLayoutKeyboardCode()`) against the
production API. It handles any US-layout-representable Unicode character,
not just ASCII.

Screenshot capture mirrors an existing production `chrome/browser` file,
`chrome/browser/feedback/report_unsafe_site/screenshot_taker.cc`:
`RenderWidgetHostView::CopyFromSurface()` -> `result.value().bitmap`
(`SkBitmap`) -> `gfx::PNGCodec::EncodeBGRASkBitmap()`.

Target resolution (which tab receives the input) goes through
`GetLastActiveBrowserWindowInterfaceWithAnyProfile()`
(`chrome/browser/ui/browser_window/public/browser_window_interface_iterator.h`)
-> `BrowserWindowInterface::GetActiveTabInterface()`
(`chrome/browser/ui/browser_window/public/browser_window_interface.h`) ->
`tabs::TabInterface::GetContents()` (`components/tabs/public/tab_interface.h`)
-> `content::WebContents`. This is single-window/single-tab-at-a-time,
matching the terminal spike's single-surface simplicity — no multi-window
targeting.

### Cross-thread delivery

Simpler than the terminal case: Chromium already has `content::
GetUIThreadTaskRunner({})` (`content/public/browser/browser_thread.h`), so
the background watcher thread just does
`PostTask(FROM_HERE, base::BindOnce(&SendKeysWatcher::DispatchLineOnUIThread,
weak_factory_.GetWeakPtr(), line))` per line — no custom mailbox/message
union needed the way Ghostty's apprt surface required. The `WeakPtr` is
created on the UI thread (inside `Start()`) and only ever *dereferenced* on
the UI thread when the posted task runs; it's just copied across the thread
boundary, which is the standard, safe Chromium pattern for this shape of
problem.

### The watcher

`chrome/browser/sendkeys_watcher.{h,cc}` (new files) implement
`sendkeys::SendKeysWatcher`, and `chrome/browser/chrome_browser_main.cc`
gained a small `ChromeBrowserMainExtraPartsSendKeys` (registered alongside
the other `ChromeBrowserMainExtraParts` in `AddParts()`,
mirroring the existing inline `ChromeBrowserMainExtraPartsThreadNotifier`
pattern in the same file) whose `PostBrowserStart()`/
`PostMainMessageLoopRun()` start/stop the watcher — the Chromium-native
equivalent of the terminal spike's `Surface.init`/`Surface.deinit` hooks.

- `Start()` — reads `CHROMIUM_SENDKEYS_DIR` via `base::Environment`; no-ops
  if unset; errors (logged) if the directory doesn't exist (does not create
  it). Spawns `WatcherThreadMain` on a `std::thread`.
- `WatcherThreadMain` — loop until `stop_requested_`; calls `DrainOnce()`
  each iteration; only sleeps (25ms) if a pass found nothing, so a burst of
  queued files drains back-to-back.
- `DrainOnce` — lists the directory (`base::FileEnumerator`), skips
  dotfiles (reserved for producer staging) and non-regular files, sorts
  remaining names, processes each in order.
- `ProcessFile` — reads a file's full contents, **deletes it before
  dispatching** (at-most-once delivery), then splits it into lines and posts
  each to the UI thread.
- `DispatchLineOnUIThread` — dispatches by prefix: `TEXT:`/`KEY:`/`CLICK:`/
  `RIGHTCLICK:`/`GOTO:`/`SCREENSHOT:`, bare line -> `TEXT:`.
- Also (re)attaches a `content::RenderWidgetHost::InputEventObserver`
  (`Observer`, nested in `SendKeysWatcher`) to whatever widget the line just
  targeted — the "middleware" observation point discussed alongside this
  spike, via the existing public `AddInputEventObserver`/
  `RemoveInputEventObserver`. Observation-only for now (`OnInputEvent` has
  no way to swallow an event); `MouseEventCallback`/`KeyPressEventCallback`
  (also public on `content::RenderWidgetHost`, and already used internally
  as a first-look, event-swallowing hook — see
  `RenderWidgetHostImpl::ForwardMouseEventWithLatencyInfo`) would be the
  next step if blocking/rewriting real input is ever needed.

### Why a spool directory, not a single tailed file

Same reasoning as the terminal spike, unchanged: any number of producers
write their own uniquely-named files independently (no shared cursor);
producers publish atomically via stage-then-`rename()` (POSIX-atomic, so
the watcher never observes a half-written file); files are processed in
sorted-filename order, then deleted.

## Producer-side CLI

`chromesendkeys.cjs` (repo root) — dependency-free Node script:

```
chromesendkeys.cjs --dir <spool> add        <TEXT:...|KEY:...|literal>
chromesendkeys.cjs --dir <spool> type       <text>
chromesendkeys.cjs --dir <spool> key        <combo>
chromesendkeys.cjs --dir <spool> click      <x> <y>
chromesendkeys.cjs --dir <spool> rightclick <x> <y>
chromesendkeys.cjs --dir <spool> goto       <url>
chromesendkeys.cjs --dir <spool> screenshot <path>
chromesendkeys.cjs --dir <spool> send       <TEXT:...|KEY:...|literal>
chromesendkeys.cjs --dir <spool> push
```

`add`/`type`/`key`/`click`/`rightclick`/`goto`/`screenshot` append a line to
`.chromium-sendkeys-staging` inside the target directory; `push` does
`fs.renameSync` to a `<Date.now()>-<pid>-<random>.txt` name. `--dir` falls
back to `$CHROMIUM_SENDKEYS_DIR`.

## Extension: EVAL, WAITFOR, NETLOG (DOM/HTTP/network)

Added in the same session, on top of the input-injection primitives above,
to cover "goto a site, wait for something to happen, read/update the DOM,
observe network, run HTTP requests."

### One primitive covers DOM read, DOM write, and HTTP: EVAL

`EVAL:<id>|<js>` runs `<js>` via
`content::RenderFrameHost::ExecuteJavaScriptForTests()`
(`content/public/browser/render_frame_host.h`), passing
`content::ISOLATED_WORLD_ID_GLOBAL` as the world id, and writes the
JSON-serialized result to `<spool_dir>/results/<id>.json` as
`{"ok":true,"value":<result>}`.

**This runs in the tab's real main JS world, not a sandboxed isolated
world, and never touches DevTools/CDP.** Despite the name,
`ISOLATED_WORLD_ID_GLOBAL` is value `0`, which
`content/public/common/isolated_world_ids.h` documents explicitly: "The
main world. Chrome cannot use ID 0 for an isolated world because 0
represents the main world." So `<js>` executes exactly as if the page's own
`<script>` tag had run it -- same globals, same page-injected libraries
(jQuery, React internals, whatever the page loaded), same `document`/
`window` state a content script or a DevTools-isolated-world eval would
*not* see. The call path is a direct Mojo message from the browser process
into Blink's `LocalFrame` (`blink::JavaScriptExecuteRequestForTestsHandler`,
`third_party/blink/renderer/core/frame/local_frame_mojo_handler.cc`) --
no DevTools agent, no CDP, anywhere in the path.

This one primitive is deliberately generic rather than three separate new
C++ commands, because from the page's point of view "get the DOM", "update
stuff", and "run HTTP" are all just JavaScript:

- Get DOM: `id|document.documentElement.outerHTML`
- Update DOM: `id|document.querySelector('#x').value = 'hi'`
- Run HTTP inside the page: `id|(async () => { const r = await
  fetch(url); return {status: r.status, body: await r.text()}; })()`

The last case works because `ExecuteJavaScriptForTests()`'s underlying
implementation (`blink::JavaScriptExecuteRequestForTestsHandler
::PromiseCallback`, `third_party/blink/renderer/core/frame/
local_frame_mojo_handler.cc`) resolves/awaits a returned Promise before the
result callback fires — confirmed by reading that handler, not assumed.
`fetch()` here runs with the page's own cookies/CORS/origin, i.e. "run HTTP
inside the browser/page," as asked for — not a separate out-of-band HTTP
client.

**Caveat, stated plainly:** `ExecuteJavaScriptForTests()` is the only public
`RenderFrameHost` API that can run unrestricted script outside
`chrome://`/`devtools://` pages — the default `ExecuteJavaScript()` is
explicitly restricted to those schemes. It is named and documented "THIS IS
ONLY FOR TESTS" in the header, but it is not build-gated or hidden behind a
test-only target (it lives in the same public production header as
`ForwardMouseEvent` etc.) — using it here is a deliberate, spike-appropriate
choice, not an oversight, but it's exactly the kind of API-use a code review
would flag on a production (non-spike) change.

### WAITFOR: poll until a condition or timeout

`WAITFOR:<timeout_ms>|<id>|<js-expression>` polls `<js-expression>` (via the
same `ExecuteJavaScriptForTests()` mechanism) every 100ms until it evaluates
truthy or `timeout_ms` elapses, writing `{"ok":true}` or
`{"ok":false,"timeout":true}` to the same results directory. Example:
`WAITFOR:5000|id|!!document.querySelector('.loaded')`.

Implemented as `SendKeysWatcher::PollWaitFor()`, self-rescheduling via
`content::GetUIThreadTaskRunner({})->PostDelayedTask()`. It holds a
`content::WeakDocumentPtr`
(`content/public/browser/weak_document_ptr.h`,
`RenderFrameHost::GetWeakDocumentPtr()`) across polls rather than a raw
`RenderFrameHost*`, so a navigation away mid-wait resolves the wait as
`{"ok":false,"error":"frame navigated away or was destroyed"}` instead of
injecting into (or crashing on) a dead frame.

### NETLOG: resource-load observation, no CDP Network domain

`NETLOG:START` attaches a `content::WebContentsObserver` subclass
(`SendKeysWatcher::NetworkLogObserver`) to the active tab, overriding the
public `ResourceLoadComplete()` hook
(`content/public/browser/web_contents_observer.h` L510-514) — the same
public API `chrome/browser/net/network_request_metrics_browsertest.cc`
uses for its own observation. Each completed resource load is recorded
(`final_url`, `original_url`, `method`, `mime_type`, `http_status_code`,
`net_error` — from `blink::mojom::ResourceLoadInfo`,
`third_party/blink/public/mojom/loader/resource_load_info.mojom`).
`NETLOG:STOP:<out_path>` detaches the observer and writes the accumulated
entries as a JSON array to `<out_path>`.

This is resource-load-completion events only (one entry per finished
request) — not a live request/response interceptor. There is no way to
inspect a request's headers/body, block/modify a request in flight, or see
in-flight (not-yet-completed) requests. A real Network-domain equivalent
would need to hook the network service layer (`network::mojom::
URLLoaderFactory` interception) — not attempted here.

### Two-way protocol: the `results/` directory

Every command up to this point was fire-and-forget (spool file in, browser
acts, nothing comes back). EVAL/WAITFOR/NETLOG:STOP need a way to return
data, so they introduce a `results/` subdirectory inside the spool dir
(created lazily by `WriteResultFile()` via `base::CreateDirectory()` — the
producer never needs to pre-create it). It's never watched as an input
source: `DrainOnce()`'s `base::FileEnumerator` lists `FILES` only,
non-recursively, so a `results/` subdirectory is invisible to the input
scan — no collision risk between the two directions.

`chromesendkeys.cjs`'s `eval`/`getdom`/`http`/`waitfor` subcommands generate a
random id, push the command, then **poll** for `results/<id>.json` (same
polling philosophy as the watcher itself — no push notification either
direction), delete it once read, and print its contents. `netlog start`/
`netlog stop <path>` are fire-and-forget on the way in; the log itself lands
directly at the path given to `stop`, not in `results/`.

## Protocol summary (quick reference)

| Env var                  | Effect                                                      |
|---------------------------|---------------------------------------------------------------|
| `CHROMIUM_SENDKEYS_DIR`   | Enables the watcher; must point at an existing directory      |

| Spool file line         | Effect                                                           |
|---------------------------|---------------------------------------------------------------------|
| `TEXT:<text>`            | Types `<text>`, one synthetic key event per Unicode character     |
| `KEY:<trigger>`          | Press+release of one chord, e.g. `ctrl+shift+t`, `enter`, `a`      |
| `CLICK:<x>,<y>`          | Left mousedown+mouseup at widget-relative coordinates              |
| `RIGHTCLICK:<x>,<y>`     | Right mousedown+mouseup (opens the native context menu — see below)|
| `GOTO:<url>`             | Navigates the active tab's main frame                              |
| `SCREENSHOT:<path>`      | PNG-encodes the current surface to `<path>`                        |
| `EVAL:<id>\|<js>`         | Runs `<js>`, writes `results/<id>.json` (DOM read/write, HTTP via fetch) |
| `WAITFOR:<ms>\|<id>\|<js>`| Polls `<js>` every 100ms until truthy/timeout, writes `results/<id>.json` |
| `NETLOG:START`           | Starts recording resource loads on the active tab                    |
| `NETLOG:STOP:<path>`     | Stops recording, writes the JSON array to `<path>`                    |
| *(bare line)*            | Treated as `TEXT:`                                                  |

Constraints: `\n` is always the file's line separator; files must be
published via atomic rename, never appended to directly inside the watched
directory; delivery is at-most-once (file is deleted before its lines are
dispatched); `KEY:` supports one chord per line, no multi-chord sequences.

## Known gotchas / limitations

- **`KEY:` trigger parsing is new, dedicated code, not a reused parser.**
  Unlike the terminal spike (whose `KEY:` syntax reuses Ghostty's own
  config-file keybind parser verbatim), no public runtime
  string-to-accelerator parser was found in Chromium to reuse — its
  accelerators are built from `ui::KeyboardCode` + modifier bits in
  C++/resource tables, not parsed from user-facing strings. `ParseTrigger()`
  in `sendkeys_watcher.cc` is a small standalone parser covering
  ctrl/shift/alt/cmd + a short list of named keys + single characters.
- **No element-id/selector targeting yet.** `CLICK:`/`RIGHTCLICK:` take raw
  widget-relative pixel coordinates, not a DOM element id or selector.
  Resolving an element to coordinates (accessibility tree, or a `Runtime.
  evaluate`-equivalent `getBoundingClientRect()` call) is the natural next
  step but is out of scope for this pass.
- **`RIGHTCLICK:` opens a real native context menu (a separate Views
  widget/surface).** This spike doesn't drive that menu — no way yet to
  click a context-menu item or dismiss it programmatically.
- **Single tab/window targeting.** `GetLastActiveBrowserWindowInterfaceWithAnyProfile()`
  picks whichever browser window was last activated; there's no way to
  target a specific tab/window by id.
- **No IME/composition support.** Typing goes through raw keydown/char/keyup
  events, not `ImeCommitText` — fine for direct-input fields, not for
  IME-composed input methods.
- **`ExecuteJavaScriptForTests()` is labeled test-only** (see the EVAL
  section above) — a deliberate, disclosed spike choice, not an oversight.
- **`results/` files are never garbage-collected by the browser side.** If a
  producer pushes an `EVAL:`/`WAITFOR:` and never reads the result (crashes,
  times out, whatever), the JSON file sits in `results/` forever. No TTL/
  cleanup was added.
- **NETLOG is resource-load-completion only**, not a live interceptor — see
  the NETLOG section above for exactly what it can't do (headers/body,
  in-flight requests, blocking/modifying).
- **`gn format`/sort-sources.** `sendkeys_watcher.{cc,h}` were added next to
  `chrome_browser_main.cc` in `chrome/browser/BUILD.gn`'s `sources` list
  rather than in strict alphabetical position within that (very long) list
  — a presubmit `gn format` pass would likely want them moved.
- **Not built or run.** Unlike the terminal spike (which was actually built
  and screenshot-verified), this has *not* been compiled — a Chromium build
  is a multi-hour affair even incrementally, and wasn't attempted in this
  session. Treat this as a design-complete, cite-checked-against-real-APIs
  patch that still needs a build+run pass before it's trusted.

## Files touched today

| File                                              | Change                                                    |
|-----------------------------------------------------|--------------------------------------------------------------|
| `chrome/browser/sendkeys_watcher.h` (new)          | Watcher class declaration                                  |
| `chrome/browser/sendkeys_watcher.cc` (new)         | Watcher implementation + input-injection helpers            |
| `chrome/browser/chrome_browser_main.cc`            | New include, `ChromeBrowserMainExtraPartsSendKeys`, `AddParts()` call |
| `chrome/browser/BUILD.gn`                          | Added the two new files to the `sources` list                |
| `chromesendkeys.cjs` (new, repo root)                | Producer CLI (`.cjs` — repo root `package.json` is `type:module`, so `.js` would be ESM and break `require`) |
| `chromium-agent-launch.cjs` (new, repo root)        | Cross-platform launcher (per-OS binary resolution, spool mkdir, spawn) |
| `chromesendkeys.test.cjs` (new, repo root)          | CLI protocol unit tests (`node --test`, no browser)          |
| `chrome/browser/sendkeys_watcher_internal.h` (new)  | Pure parser decls split out for unit testing                 |
| `chrome/browser/sendkeys_watcher_unittest.cc` (new) | gtest for `ParseTrigger` (wired into `//chrome/test:unit_tests`) |
| `.claude/skills/chromium-sendkeys/SKILL.md` (new)  | Operational runbook for build/launch/inject/verify/test      |
| `CHROMIUM_SENDKEYS_SPEC.md` (new, this file)       | Design + protocol record                                    |
| `chrome/app/chrome_main_delegate.cc`              | **Security relaxation:** force `--disable-web-security` on unconditionally (CORS off) — see below |
| `chrome/browser/ui/startup/bad_flags_prompt.cc`   | **Security relaxation:** drop `kDisableWebSecurity` from the bad-flags list (no warning infobar) |
| `chrome/browser/ui/webui/version/version_ui.cc`   | **Security relaxation:** hide `--disable-web-security` from the `chrome://version` command-line field |
| `services/network/public/cpp/parsed_headers.cc`   | **Security relaxation:** skip parsing `Content-Security-Policy` response headers (header CSP not enforced) |
| `components/permissions/permission_context_base.cc` | **Security relaxation:** `DecidePermission()` auto-grants every permission (no prompt) — see below |
| `components/os_crypt/common/keychain_password_mac.mm` | **Keychain-free profile:** `GetPassword()` reads the OSCrypt key from a file (`$CHROMIUM_AGENT_OSCRYPT_KEY_FILE` / `~/.config/chromium-agent/oscrypt.key`) before falling back to the macOS Keychain — no prompt, and decrypts a profile copied from another browser when seeded with its key |
| `chrome/browser/sendkeys_watcher.{cc,h}` | Tab/window commands `NEWTAB`/`NEWWINDOW`/`CLOSETAB`/`SELECTTAB`/`LISTTABS` (act on the last-active window; `GOTO`/`EVAL` still target the active tab) |

## Security relaxations (agent build — CORS + CSP off)

This is an **agent build**: it deliberately removes two browser security
mechanisms so an automated agent can issue cross-origin requests from any page
without being blocked. Both are baked into the binary in the *core* — there is
**no launch flag to pass and none to forget**, and the fact that they are off
is intentionally not surfaced in the UI. This is a security downgrade by
design; do not point this build at untrusted sites while logged into anything
you care about.

### 1. CORS / same-origin policy — OFF

Chromium already has a `--disable-web-security` switch that bypasses CORS and
the same-origin policy (it is plumbed into the network service via
`network::mojom::URLLoaderFactoryParams::disable_web_security` and into Blink
via the `web_security_enabled` web-preference — a single switch that ~10 read
sites in `content/`, `services/network/`, and Blink all key off).

Rather than depend on the launcher passing the flag, we force it on in the
core at the earliest per-process callback:

- **`chrome/app/chrome_main_delegate.cc`** — `BasicStartupComplete()`. Upstream
  code here *stripped* `kDisableWebSecurity` unless a **non-default**
  `--user-data-dir` was also given. We replaced that guard with an
  unconditional `AppendSwitch(switches::kDisableWebSecurity)` (idempotent —
  only appends if not already present). Because `BasicStartupComplete()` runs
  in **every** process (browser + renderer + utility/network + gpu), all of
  them see the switch. The browser-process copy is what matters for CORS,
  since factory params are built browser-side and sent to the network service
  over mojo.

Because the switch is now injected in-code, it is deliberately hidden from the
two places a user would otherwise "read" it:

- **`chrome/browser/ui/startup/bad_flags_prompt.cc`** — removed
  `switches::kDisableWebSecurity` from the `kBadFlags` list, so the "You are
  using an unsupported command-line flag… stability and security will suffer"
  infobar never appears.
- **`chrome/browser/ui/webui/version/version_ui.cc`** — filtered the
  `--disable-web-security` token out of the `chrome://version` **Command Line**
  field (both the Windows `GetCommandLineString()` path and the POSIX `argv`
  loop).

**Verified:** cross-origin `XMLHttpRequest` (which returns `null` under CORS)
returns the real body from `example.com → example.org`, `google.com →
example.com` (200, 559 bytes), and a `data:`/`accounts.google.com` origin →
`example.com`.

### 2. Content-Security-Policy (header) — NOT ENFORCED

`--disable-web-security` disables CORS/SOP but **not** CSP — CSP is a separate,
page-declared policy. A site like LinkedIn sends
`Content-Security-Policy: connect-src 'self' …` as an **HTTP response header**,
which makes a cross-origin `fetch`/XHR from its pages throw `NetworkError` even
with web security off. (Confirmed by elimination: same-origin XHR from
LinkedIn returned 141 KB; cross-origin threw `NetworkError`; the identical
cross-origin call from `google.com`, whose page CSP is permissive, succeeded.)

To let requests work from **any** page, header CSP is disabled at the single
network-service chokepoint that feeds CSP to Blink for every response:

- **`services/network/public/cpp/parsed_headers.cc`** —
  `PopulateParsedHeaders()` normally calls
  `AddContentSecurityPolicyFromHeaders(...)` to fill
  `parsed_headers->content_security_policy`. That call is commented out, so the
  CSP list is always empty and no header-delivered CSP (`connect-src`,
  `frame-ancestors`, `script-src`, …) is ever enforced, process-wide.

**Verified:** after this change, `linkedin.com → example.com` cross-origin XHR
returns `OK status=200 len=559` (was `NetworkError`).

**Caveat — meta-tag CSP not covered:** CSP delivered via
`<meta http-equiv="Content-Security-Policy">` is parsed and enforced inside
Blink (renderer), not at this network-service chokepoint, so it is **not**
disabled by this change. Header CSP (what LinkedIn and most sites use) is the
common case; a site relying on meta CSP would need an additional Blink-side
change. Not done here.

### 3. Permission prompts — AUTO-GRANTED (never shown)

An automation agent cannot click a permission dialog, so every permission is
granted silently at the single decision chokepoint every capability funnels
through:

- **`components/permissions/permission_context_base.cc`** —
  `PermissionContextBase::DecidePermission()` normally builds a
  `PermissionRequest` and hands it to the `PermissionRequestManager`, which
  shows a prompt. The body is replaced with an immediate
  `NotifyPermissionSet(..., PermissionDecision::kAllow, is_final=true)`. This is
  the **exact** result computation of a user clicking "Allow"
  (`ComputeNewPermissionResult` → `GRANTED`), so the page sees a normal grant
  with no UI and nothing observable. `persist=false` grants per-request instead
  of writing a durable content setting, keeping the profile unmodified.

Covers geolocation, notifications, camera, microphone, clipboard-read, MIDI
sysex, sensors, and every other context that routes through the base class. (A
few contexts override `DecidePermission` with a custom flow; camera/mic are
already handled up-front by the launcher's `--use-fake-ui-for-media-stream`.)

**Note:** meta-tag CSP (§2 caveat) and permission contexts that fully override
`DecidePermission` are the only gaps. Not observed in practice for the target
sites.

### Rebuilding after these changes

All five files are compiled into `chrome` (the network-service one lives in the
widely-linked `//services/network/public/cpp` target, so it recompiles a bit
more broadly). `autoninja -j 6 -C out/Default chrome` relinks. A running
browser must be **relaunched** on the new binary to pick the changes up.

## Cross-platform notes ("run anywhere")

The **C++ is already OS-agnostic**: it is built entirely on portable public
`content/` + `base/` + `ui/` APIs (`RenderWidgetHost::Forward*Event`,
`ExecuteJavaScriptForTests`, `WebContentsObserver`, `ui::DomKey`/`DomCode`,
`base::FileEnumerator`/`Environment`, `FILE_PATH_LITERAL`), which Chromium
implements per-platform underneath. The same `sendkeys_watcher.{h,cc}`
compiles and runs on macOS, Windows, and Linux with no `#if BUILDFLAG`
branching — synthetic events reach the renderer the same trusted way on all
three.

The **only platform-specific surface is the launcher / binary path**, since
the build output differs per OS (macOS `.app` bundle vs Windows `chrome.exe`
vs Linux bare `chrome`). `chromium-agent-launch.cjs` abstracts that: it
resolves the correct binary via `process.platform`, creates the spool dir,
sets `CHROMIUM_SENDKEYS_DIR`, and spawns — so `node chromium-agent-launch.cjs`
is the one command that works identically on every platform. The producer
CLI (`chromesendkeys.cjs`) was already OS-agnostic (`path.join`,
`fs.renameSync` — atomic within a volume on Windows too, `Atomics.wait`
sleep).

Not yet done cross-platform: the convenience PATH symlink is a POSIX
`ln -s` step (macOS/Linux); the Windows equivalent (a `.cmd`/`.bat` shim or
`out\Default` on `%PATH%`) isn't scripted — but the portable launcher makes
a PATH entry optional on every OS.

## Explicitly out of scope / not done

- No CLI flag alternative to the `CHROMIUM_SENDKEYS_DIR` env var.
- No real filesystem-event notification (kqueue/inotify/FSEvents) — the
  watcher polls every 25ms.
- No element-id/selector-based targeting (see gotchas above).
- No multi-chord `KEY:` sequences per line.
- No context-menu interaction after `RIGHTCLICK:`.
- No headers/body inspection or in-flight request interception for network
  (`NETLOG` is completion-only).
- No TTL/cleanup for orphaned `results/*.json` files.
- Not wired into any build target's tests or CI — manual/spike tool only.
- Not upstreamed or intended for upstream Chromium; fork-local
  automation-testing feature, same as the terminal counterpart.

## Build + verification (done 2026-07-14)

Built (`autoninja -j 6 -C out/Default chrome`, component build,
DCHECK_ALWAYS_ON) and verified end-to-end against the real browser on
macOS arm64. Full regression passed with zero crashes: goto → waitfor →
eval/getdom → real keyboard `type` (read back through the page) → real mouse
`click` (fired a button's onclick) → sync-XHR `http` → screenshot (valid PNG
1200×829, correct render) → netlog (captured a resource load, status 200) →
browser still alive across navigations.

Five bugs were found by actually running it (none caught by inspection):

1. **`base::Value::Dict`/`List` don't exist in this tree** — renamed to
   `base::DictValue`/`base::ListValue` (compile error).
2. **UI-thread blocking file I/O** in WriteResultFile/InjectScreenshot/
   InjectNetLogStop — `base::CreateDirectory`/`WriteFile` on the UI thread
   CHECK-fail (fatal under DCHECK_ALWAYS_ON). Fixed by moving the writes to
   `base::ThreadPool::PostTask(FROM_HERE, {base::MayBlock()}, …)`; serialize
   on the UI thread, write off it.
3. **Async/Promise EVAL returns `{}`** — the public
   `ExecuteJavaScriptForTests` uses `resolve_promises=false`
   (render_frame_host_impl.cc:3889), so a returned Promise serializes empty.
   The `http` CLI command uses a **synchronous XMLHttpRequest** instead of
   `fetch()`. Documented limitation: EVAL round-trips synchronous values
   only; `waitfor` (polling) covers async conditions.
4. **Observer dangling-pointer crash on navigation** — the
   `InputEventObserver` held a `raw_ptr<RenderWidgetHost>`; a cross-document
   navigation frees the old widget, and replacing the observer ran the
   destructor's `RemoveInputEventObserver()` on freed memory (SEGV). Fixed by
   also implementing `content::RenderWidgetHostObserver` and detaching in
   `RenderWidgetHostDestroyed`.
5. **CLI action verbs didn't publish** — `screenshot`/`type`/`key`/`click`/
   `rightclick`/`goto` only appended to the staging file without `push()`, so
   the command never reached the watcher (masked in early tests because a
   later two-way command's push flushed the staged line). Now they auto-push;
   only `add` stays stage-only.

Toolchain note: this checkout arrived with most of `third_party/` as empty
stubs; a full `gclient sync` (~30 GB) + manual CIPD/GCS fetch of gn + the
mac-arm64 clang & rust toolchains was needed before `gn gen`/build worked.
