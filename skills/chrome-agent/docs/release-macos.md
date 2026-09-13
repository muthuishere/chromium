# Releasing the fork on macOS

The runbook for `chrome-agent-engine-<version>+fork.<n>-darwin-arm64`. It follows ADR 0007 (what a
release is, and the five gates) and mirrors ADR 0008 (the Linux artifact that already passed them).

**The one-line summary: on macOS, building is the easy half.** A Linux artifact is a tarball and a
sha256 and it is done. A macOS artifact that anybody *downloads* is quarantined by Gatekeeper
unless it is signed with a Developer ID Application certificate, built with the hardened runtime,
notarized by Apple, and stapled. That is not a polish step you add later — it changes the app's code
identity, which changes what the Keychain will hand it, which is the fork's login story.

Everything below is either measured on this machine today (2026-09-13) or marked
**UNVERIFIED — needs a real run**. Nothing here prints, echoes, or stores a key.

---

## 0. What exists today, measured

| | |
|---|---|
| `out/Default` | `is_component_build = true` — **iteration only, never a release** |
| its `Chromium.app` | 109 MB, and **does not run outside the build tree** |
| the real browser in that config | 524 loose dylibs, 699 MB, sitting *beside* the .app in `out/Default` |
| signature on it | `Signature=adhoc`, `flags=0x20002(adhoc,linker-signed)`, `TeamIdentifier=not set`, `Sealed Resources=none` |
| bundle id | `org.chromium.Chromium` |
| `notarytool` / `stapler` | present — `/Applications/Xcode.app/Contents/Developer/usr/bin/` |
| Developer ID identity in the login keychain | **present** — `Developer ID Application: Suguna Paulraj (6PQ6534W2R)` |

Copy that component `Chromium.app` anywhere else and it dies, verbatim:

```
dyld: Library not loaded: @rpath/libc++_chrome.dylib
  tried: …/Chromium.app/Contents/Frameworks/libc++_chrome.dylib (no such file)
         …/Chromium.app/Contents/MacOS/../../../libc++_chrome.dylib (no such file)
```

That second path — `MacOS/../../..` — is the build directory. The launcher's rpath reaches back into
`out/Default`, which is why the artifact works perfectly on the machine that built it and nowhere
else. **This is the failure mode ADR 0007 exists to catch**, and it is why gate 1 is "unpack
somewhere else entirely and launch", not "run it".

---

## 1. Build: non-component, in its own directory

`out/Default` stays as it is. A browser is usually running out of it, and the fast component link is
what makes iteration bearable. Releases get their own tree.

Chromium's own signing README states the requirement independently: *"Signing requires a statically
linked build (i.e. `is_component_build = false`)"* — `chrome/installer/mac/signing/README.md`.

```sh
export PATH="$HOME/depot_tools:$PATH"
cd ~/muthu/gitworkspace/chromium

gn gen out/Release --args='is_debug=false is_component_build=false dcheck_always_on=false
  symbol_level=0 blink_symbol_level=0 is_official_build=false proprietary_codecs=true
  ffmpeg_branding="Chrome"'          # one line, no newlines

nice -n 19 autoninja -C out/Release chrome -j6
```

- `-j6` and `nice -n 19` because this is a laptop. The default job count will make the machine
  unusable and will thermally throttle its way to a *slower* build.
- `proprietary_codecs` + `ffmpeg_branding="Chrome"` gives H.264/AAC. Note the patent-licensing
  obligation that attaches to *distributing* binaries containing those decoders (ADR 0007).
- `is_official_build=false` keeps the build tractable; it also keeps the bundle id
  `org.chromium.Chromium` and the name `Chromium.app`, which is what every path in the client
  already resolves (`client/internal/paths/paths.go:42`).
- Also build `autoninja -C out/Release chrome chrome/installer/mac` if you intend to use Chromium's
  own signing driver (§4) — that target is what emits `out/Release/Chromium Packaging/sign_chrome.py`.

**Expected size — UNVERIFIED on darwin.** Linux measured `chrome` at 486 MB, a 531 MB staged tree
and a 185 MB tarball (ADR 0008). On macOS the equivalent mass is
`Chromium.app/Contents/Frameworks/Chromium Framework.framework`, and the number will be whatever the
first completed non-component build says. Until that build finishes, **the darwin `.app` size is
unmeasured** — `package-release.sh` writes the measured value into `manifest.json`, so nobody has to
guess.

---

## 2. What actually ships on macOS

**The `.app` bundle, and nothing else.** Non-component, the whole browser — the helper apps, the
paks, `icudtl.dat`, the v8 snapshots — lives inside
`Chromium.app/Contents/Frameworks/Chromium Framework.framework`. That is the entire difference from
Linux, where `chrome` needs a dozen resource files laid out beside it.

Never ship the out dir. `out/Release` on Linux came to 9.5 GB of test binaries and object files
against a 531 MB artifact; macOS is no different.

```sh
bash skills/chrome-agent/scripts/package-release.sh              # stage, tar.gz, manifest, SHA256SUMS
bash skills/chrome-agent/scripts/package-release.sh --zip        # also a ditto zip, for notarytool
```

The script **refuses to package a component build** — it reads `args.gn` and stops — and it checks
that `Chromium Framework` is a real binary and not a stub, because those are the two ways this
artifact has already failed.

Two things it stages beyond the bundle, 17 KB total: `chromesendkeys.cjs` and
`chromium-agent-launch.cjs`, plus an `out/Default` symlink back to the artifact root. That makes one
env var (`CHROME_AGENT_FORK=<unpacked>`) resolve everything the Go client needs on a box with **no
repo checkout** — which is exactly what ADR 0007's "what would prove this" item 3 asks for.

Locales default to en-US only. On Linux the full set was 123 MB of a 531 MB tree; the macOS
equivalent is the `.lproj` directories inside the framework's `Resources`, and `--all-locales` keeps
them.

---

## 3. Gatekeeper: what happens with no signature

This is the question that decides whether a macOS release is distributable at all.

An adhoc-signed `.app` (what `autoninja` produces — measured above) behaves like this:

| how it arrives | what happens |
|---|---|
| built here, run here | fine. No quarantine attribute is ever set. |
| copied over SSH / `scp` / a USB stick | usually fine — no quarantine attribute. |
| **downloaded** (browser, Mail, Slack, a GitHub Release) | the downloader sets `com.apple.quarantine` on it. Gatekeeper then evaluates it, finds no Developer ID and no notarization ticket, and **refuses to launch it**. On recent macOS the dialog says the app *"is damaged and can't be opened"* — which is the message for "unsigned and quarantined", not for a corrupt file, and it sends every user straight to a support thread. |

Aggravating details:

- **Right-click → Open does not reliably rescue it any more.** That escape hatch was for
  *signed-but-not-notarized* apps. For an adhoc/unsigned bundle from a download, recent macOS
  versions give no Open button at all.
- **`xattr -dr com.apple.quarantine Chromium.app` works**, and it is a workaround *for the person
  who built it*, not a distribution strategy — ADR 0007 rejects "tell users to right-click-open" for
  exactly this reason: it teaches users to bypass Gatekeeper, and it works once per user per
  download.
- **App Translocation.** A quarantined app launched from `~/Downloads` is copied to a random
  read-only path first. Anything that resolves paths relative to the bundle — which is precisely how
  this artifact finds `chromesendkeys.cjs` and the profile — sees a path that is not where the user
  put it.

So: **for anything that will be downloaded, signing + notarization is not optional.** For a tarball
you `scp` to a second Mac to satisfy gate 1, an unsigned build is enough to *test* — and ADR 0007's
proof item 2 explicitly asks for the stronger thing: "launches from `~/Downloads` on a second Mac
with no quarantine prompt."

---

## 4. Signing and notarizing

### 4.1 The certificate — and how `applecert` fits

Notarization requires a **Developer ID Application** certificate. Not "Apple Development", not
"Apple Distribution" (that one is for the App Store), and **not** a self-signed certificate —
Chromium's README is explicit that a self-signed identity *"is incompatible with the library
validation signing option that Chrome uses."*

The owner's `applecert` tool already mints exactly this one:

```sh
applecert generate developer-id     # -> DEVELOPER_ID_APPLICATION  (applecert:152)
applecert status                    # config + what exists
applecert list
```

Measured today, `security find-identity -v -p codesigning` already lists
**`Developer ID Application: Suguna Paulraj (6PQ6534W2R)`** in the login keychain, alongside an Apple
Development and an Apple Distribution identity. So the certificate half of this is **already
solved** — the team id `6PQ6534W2R` is the one a notarization submission would be attributed to.

`applecert` keeps its private keys in `~/.config/apple/keys/` and the App Store Connect `.p8` where
its config points. Account details (issuer id, key id, team id, cert inventory) are in the prod vault
at `infra-workspace/infra/vault/production/wiki/appleaccount.md`. **None of it is read, printed, or
copied by anything in this runbook.** `codesign` takes the identity by *name*; the key never leaves
the keychain.

### 4.2 Hardened runtime and the entitlements a Chromium fork needs

Notarization requires the hardened runtime (`codesign --options runtime`). The hardened runtime then
*removes* capabilities the browser needs back by entitlement. Chromium ships the exact set — these
are files in this checkout, not guesses:

| bundle part | entitlements file | signing options |
|---|---|---|
| the main app | `chrome/app/app-entitlements.plist` | `restrict,library,runtime,kill` (`FULL_HARDENED_RUNTIME_OPTIONS`) |
| Helper (Renderer) | `chrome/app/helper-renderer-entitlements.plist` | `restrict,runtime,kill` — **library validation deliberately omitted** |
| Helper (GPU) | `chrome/app/helper-gpu-entitlements.plist` | `restrict,runtime,kill` — same omission |
| the framework, crashpad, swiftshader, every other nested binary | none | `FULL_HARDENED_RUNTIME_OPTIONS` |

Sources: `chrome/installer/mac/signing/parts.py:43-122` for the mapping,
`chrome/installer/mac/signing/model.py:199-214` for what the option names mean.

What is actually in them:

- **`app-entitlements.plist`** — `com.apple.security.device.audio-input`, `.camera`, `.bluetooth`,
  `.usb`, `.print`, `personal-information.location`, `.photos-library`. The audio-input one is not
  decorative for this fork: ADR 0002's Teams audio path and the `/tap` bridge are microphone work,
  and without that entitlement a hardened-runtime build gets silence.
- **`helper-renderer-entitlements.plist` / `helper-gpu-entitlements.plist`** — a single key,
  `com.apple.security.cs.allow-jit`. V8 is a JIT; the hardened runtime blocks
  write-then-execute memory without it, and the renderer simply will not start.
- **Library validation is off for the helpers on purpose.** `parts.py:73-90` says it in a comment:
  the helpers load plugins/libraries not signed by the same team. Turning it on for them breaks the
  browser; leaving it off for the *main* app would weaken the whole thing.

### 4.3 Do it with Chromium's own driver, not a hand-rolled `codesign` loop

A Chromium bundle is dozens of nested signable items and the order matters (inside-out: helpers and
dylibs first, framework next, outer app last). Hand-rolling that is how a bundle ends up passing
`codesign -v` and failing notarization. Use what the tree already has:

```sh
autoninja -C out/Release chrome chrome/installer/mac
./out/Release/Chromium\ Packaging/sign_chrome.py \
    --input out/Release --output out/Release/signed \
    --identity 'Developer ID Application: Suguna Paulraj (6PQ6534W2R)' \
    --disable-packaging
```

(`--disable-packaging` skips DMG/PKG creation; we ship a tarball/zip. `--development` exists but
injects `com.apple.security.get-task-allow` and skips real signing checks — **never for a release**.)

**UNVERIFIED — needs a real run.** `sign_chrome.py` has never been run against this fork. The fork
patch touches 261 files (+25,903 / -257 as measured today, well past ADR 0007's "56 files / 298 KB"
figure) and adds new binaries under `media/` and `chrome/browser/`; if any of them land as separate
nested Mach-O files rather than inside the framework, `parts.py`'s inventory needs an entry or the
outer signature will not seal them. The first real run is what tells us.

### 4.4 Notarize, then staple

```sh
# One-time, interactive, on the owner's machine. Stores the App Store Connect credential IN THE
# KEYCHAIN under a profile name; nothing is written to a file and nothing is echoed.
xcrun notarytool store-credentials "chrome-agent-notary" \
    --key ~/.config/apple/keys/<AuthKey>.p8 --key-id <KEY_ID> --issuer <ISSUER_ID>

# Submit. notarytool takes a zip/dmg/pkg — and only ditto's zip preserves an .app's symlinks and
# signature. `zip -r` produces an archive notarization rejects.
ditto -c -k --keepParent --sequesterRsrc out/Release/signed/Chromium.app /tmp/Chromium.zip
xcrun notarytool submit /tmp/Chromium.zip --keychain-profile "chrome-agent-notary" --wait

# On failure, the log is the only thing that tells you which nested binary is unsigned:
xcrun notarytool log <submission-id> --keychain-profile "chrome-agent-notary"

# Staple the ticket INTO the bundle, so it validates with no network on the user's machine.
xcrun stapler staple out/Release/signed/Chromium.app
xcrun stapler validate out/Release/signed/Chromium.app
```

Chromium's driver automates the same three calls — `notarytool submit --no-wait --output-format
plist` (`signing/notarize.py:51-66`), poll, then `stapler staple --verbose`
(`signing/notarize.py:182`) — and takes its auth through repeatable `--notary-arg` flags
(`notarize.py:31-49`), e.g. `--notary-arg=--keychain-profile --notary-arg=chrome-agent-notary`.

**Package only after stapling.** The ticket lives in the bundle; a tarball made before stapling
ships an un-stapled app. So the order is: build → sign → notarize → staple → `package-release.sh` →
`verify-release.sh`.

### 4.5 Prove it, locally, before anyone downloads it

```sh
codesign --verify --deep --strict --verbose=4 Chromium.app     # structural
codesign -dv --verbose=4 Chromium.app                          # expect TeamIdentifier=6PQ6534W2R,
                                                               # flags=…(runtime), Signature=…Developer ID
spctl -a -vvv -t exec Chromium.app                             # expect: accepted, source=Notarized Developer ID
xcrun stapler validate Chromium.app                            # expect: The validate action worked!
```

And the honest end-to-end check, which is the one ADR 0007 actually asks for: put the artifact
somewhere it must be **downloaded** from, download it on a **second Mac**, and launch it. A
`scp` does not set the quarantine attribute, so a copy that works proves nothing about a download.

---

## 5. Verify — the five gates

```sh
bash skills/chrome-agent/scripts/verify-release.sh \
     dist/chrome-agent-engine-152.0.7948.0+fork.1-darwin-arm64.tar.gz
```

It unpacks into a fresh `mktemp -d` **outside the build tree**, launches the artifact there with a
throwaway profile (so it can never touch a live session another agent is driving), and asks
everything through the Go client — no node, no python3. Gates: relocation, `navigator.webdriver ===
false`, the spool protocol (eval / evalAsync / tabId registry / screenshot ack), `doctor` reporting
`ready: true`, and a real read of Hacker News.

**There is no SKIP.** A gate it cannot run is a FAIL with a named reason and a non-zero exit, because
the failure this whole ADR is about is silent.

`VERIFY_JSON=gates.json bash …/verify-release.sh <artifact>` writes the gate block in the manifest's
shape, so the `UNVERIFIED` lines `package-release.sh` wrote get replaced by something a run actually
produced — never by hand.

What it does **not** cover, and what you must therefore check by hand from §4.5: signature, team id,
hardened runtime, notarization ticket, and the download-and-launch test on a second Mac.

---

## 6. What breaks

- **The Keychain forgets you after every re-sign.** The fork patches
  `components/os_crypt/common/keychain_password_mac.mm`, and OSCrypt's "Chromium Safe Storage"
  keychain item is ACL'd to a *code identity*. Signing with a Developer ID changes that identity, so
  the first launch of a signed build re-prompts for keychain access — and a profile encrypted under
  the old identity will not decrypt its cookies until it is granted. The fork already has the escape
  hatch: `$CHROMIUM_SAFE_STORAGE_KEY` makes OSCrypt derive the same AES key with no keychain involved
  at all. For a distributed build, that env path is the story, not the keychain.
- **Library validation vs. the agent layer.** The main app signs with `library` (library
  validation), which means it will load only libraries signed by the same team. Anything the agent
  layer might `dlopen` from outside the bundle would be refused. Nothing in the fork does that today
  — the audio/video bridges are compiled in — but it is the constraint to check first if a signed
  build behaves differently from an unsigned one.
- **`--no-sandbox` is fine for the verify gates and wrong for a release.** The gate script passes it
  to a throwaway profile in a temp dir. It should not appear in anything a user runs.
- **`--headless=new` on macOS is not headful.** Gate 5 passes headless; the headful window path is a
  different code path (and the reason `--headful` exists on the verify script). A release claim of
  "works" should say which.
- **tar.gz vs. the notarization zip.** The distributable is the tarball; the notarization submission
  is a `ditto -c -k --keepParent` zip. They are not interchangeable, and `zip -r` is not a substitute
  for `ditto` for either one.
- **A re-based fork invalidates everything.** When `sync-upstream.sh` moves the base, the build, the
  signature, and the notarization ticket are all stale. That is why `upstream_base` and
  `fork_patch_sha256` are in the manifest.
- **`is_official_build=true` would rename the app.** It also turns on a much heavier link. If it is
  ever adopted, `client/internal/paths/paths.go:42` and every `Chromium.app` string in the packaging
  path need to move with it.

---

## 7. Status, honestly

| step | state |
|---|---|
| non-component `out/Release` build on darwin-arm64 | **not finished.** Started 2026-09-13 at `nice 19 -j6`; starved by a load average of ~125 from unrelated work, then killed by a machine restart at ~3,900 of the tens of thousands of objects `chrome` needs. Restarted incrementally the same day. `.app` size **unmeasured**; relocation of a real non-component bundle **UNVERIFIED — needs a real run** |
| `package-release.sh` / `verify-release.sh` | written, `bash -n` and shellcheck clean |
| Developer ID Application certificate | **present** on this machine (`6PQ6534W2R`) |
| `sign_chrome.py` against this fork | **UNVERIFIED — needs a real run** |
| notarytool credential profile | **UNVERIFIED — needs a real run** (none confirmed to exist) |
| notarization + stapling of a fork build | **UNVERIFIED — needs a real run** |
| download-and-launch on a second Mac | **not done** — ADR 0007's proof item 2 |
