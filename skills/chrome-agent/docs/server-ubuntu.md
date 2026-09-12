# chrome-agent on Ubuntu — what actually has to be true

Companion to [ADR 0003](../../../docs/adr/0003-chrome-agent-on-a-server.md) and to
[`scripts/server-install.sh`](../scripts/server-install.sh).

**Status: SPIKE.** The installer was written and statically checked on macOS. Nothing in this
document has been executed on a real Ubuntu box. Anything that needs one to confirm is labelled
**UNVERIFIED**. The cookie-encryption section is the exception — that one is settled from the
fork's own source, cited below.

---

## 0. What the stack needs from the OS

Read off `chromium-agent-launch.cjs` and `skills/chrome-agent/chrome-agent`:

| need | why | where it comes from |
|---|---|---|
| the fork checkout | `chromesendkeys.cjs` + `chromium-agent-launch.cjs` are read from `$CHROME_AGENT_FORK` (`chrome-agent:40-56`) | git clone |
| a built Linux binary | launcher resolves `out/Default/chrome` on any non-darwin/win32 platform (`chromium-agent-launch.cjs:53`) | build or copy — see §1 |
| `node` | runs `chromesendkeys.cjs`; dependency-free, so version is undemanding | apt |
| `python3` | the CLI shells out to it for *every* JSON encode/decode | apt |
| `sha256sum` | `_spool_key()` falls back to it when `shasum` is absent. A wrong hash means a wrong spool means **a different profile silently posting as the wrong identity** | coreutils |
| Chromium's shared libs | the binary is dynamically linked | apt, group (a) |
| Xvfb + x11vnc + noVNC + cloudflared | only for `share` / login (ADR 0003 §2–3) | apt + Cloudflare repo |

`server-install.sh` installs (a)–(d) and then prints a preflight table. It never builds
Chromium and never starts a browser.

---

## 1. Two ways to get a fork onto the box

### Path A — build on the server

```
git clone <fork> ~/chromium && cd ~/chromium
./build/install-build-deps.sh --no-prompt
gn gen out/Default --args='is_debug=false dcheck_always_on=false is_component_build=true'
autoninja -C out/Default chrome
```

The real cost, stated honestly:

- **Disk:** a Chromium checkout plus one `out/` is on the order of **100 GB**. A 40 GB VPS
  cannot do this. Budget 150 GB to be able to rebuild without pruning.
- **RAM:** the link step is the spike. 16 GB is the floor; 8 GB will OOM the linker.
  32 GB is where it stops being annoying. Swap is not a substitute — it turns hours into days.
- **Time:** a cold full build is **hours** on a typical cloud VM (4–8 vCPU). Not a coffee break.
- `install-build-deps.sh` is a much bigger apt footprint than `server-install.sh` group (a):
  (a) is the *runtime* set, `install-build-deps` is the *build* set. Do not confuse them.

Use this path when the server is the only machine, or when it is a beefy build box you'll reuse.

### Path B — build elsewhere, copy `out/Default`

```
# on the build machine
tar -C out -czf default.tgz Default
scp default.tgz server:
# on the server
git clone <fork> ~/chromium              # still needed: the two .cjs files live here
mkdir -p ~/chromium/out && tar -C ~/chromium/out -xzf default.tgz
```

**A macOS-built Chromium cannot run on Linux. Full stop.** On darwin the launcher resolves
`out/Default/Chromium.app/Contents/MacOS/Chromium` — a Mach-O binary inside an `.app` bundle,
linked against macOS frameworks. Linux loads ELF and links against glibc + the X/GTK stack.
There is no shim, no compatibility layer, no flag. The file will not even be recognised as an
executable format.

So **the copy path requires a Linux build.** Concretely, that means one of:
- a Linux build box or VM you own,
- a CI job (GitHub Actions `ubuntu-latest`, a self-hosted Linux runner) that builds the fork and
  publishes `out/Default` as an artifact,
- cross-compiling from the Mac to Linux — Chromium supports Linux targets via a sysroot, but
  this is a non-trivial GN configuration and has **never been attempted for this fork**
  (**UNVERIFIED — needs a real run**). Do not plan around it.

The preflight in `server-install.sh` has an explicit ELF check on the binary precisely because
"I copied out/Default from my Mac" is the most likely first mistake.

Also match the ABI: a binary built on Ubuntu 24.04 may not run on 22.04 (newer glibc symbols).
Build on the same release as the server, or older.

---

## 2. Headless vs headful-on-Xvfb

The launcher supports both with the same profile, spool, and agent protocol
(`chromium-agent-launch.cjs:86-119`):

```
CHROMIUM_AGENT_HEADLESS=1 node chromium-agent-launch.cjs      # --headless=new, no window
node chromium-agent-launch.cjs                                # headful, needs a display
```

**Work runs headless.** No display, no X stack in the process, nothing exposed. This is the
normal mode on a server and it removes the cron/ssh launch failure class entirely.

**Login runs headful on Xvfb.** Per ADR 0003 §2, headless Chromium is the wrong tool for a
login page: sites fingerprint it differently, interstitials ("verify it's you", 2FA, device
checks) are built for a human at a window, and a human cannot *see* headless. So:

```
Xvfb :99 -screen 0 1440x900x24 &
DISPLAY=:99 node chromium-agent-launch.cjs https://example.com/login
x11vnc -display :99 -localhost -rfbport 5900 -nopw -once   # 127.0.0.1 ONLY
websockify --web=/usr/share/novnc 6080 127.0.0.1:5900
cloudflared tunnel --url http://127.0.0.1:6080
```

That is the shape `chrome-agent share start` should automate. Read ADR 0003 §3 before wiring
it: **the TTL, the 127.0.0.1 bind, and the teardown are the security properties**, not
decorations. The 2026-07-06 incident (unauthenticated browser-bridge on a public tunnel, as
root, for days) is why.

`-nopw` above is only acceptable because the VNC port is bound to loopback and the only route
in is a tunnel the operator just created and will destroy. The moment that bind address
changes, this line becomes the vulnerability. **UNVERIFIED — needs a real Ubuntu run** that the
fork renders correctly on Xvfb without a GPU; expect to need `--disable-gpu` or
`--use-gl=swiftshader` (**UNVERIFIED**).

---

## 3. Carrying a profile across OSes — and what it costs you

This is the crux, so here is the answer up front:

> **Encrypted cookies copied from a macOS profile to a Linux box do not survive. They are
> silently dropped at load. The session is gone; you will be logged out of every site whose
> auth lives in a cookie.**

Not "may not". Not "unless you configure it". Here is the mechanism, from the fork's own
source in this repo.

### The mechanism

Chromium's cookie store encrypts each cookie value with an OS-provided key, and **prefixes the
ciphertext with a tag naming the key provider**. Decryption picks the key by matching that
tag prefix (`components/os_crypt/async/common/encryptor.cc:253-260`).

| platform | provider | tag | key |
|---|---|---|---|
| macOS | `KeychainKeyProvider` | **`v10`** | PBKDF2-HMAC-SHA1(**1003** iterations, salt `saltysalt`) over a random password stored in the Keychain as *Chrome Safe Storage* — `components/os_crypt/async/browser/keychain_key_provider.mm:28-66` |
| Linux, with a keyring | `FreedesktopSecretKeyProvider` | **`v11`** | PBKDF2 over a secret from gnome-keyring / kwallet via the Secret Service — `.../freedesktop_secret_key_provider.cc:45-49` |
| Linux, no keyring | `PosixKeyProvider` | **`v10`** | a **hardcoded constant**: PBKDF2-HMAC-SHA1(1 iteration, key `"peanuts"`, salt `saltysalt`) — `.../posix_key_provider.cc:17-23` |

Both macOS and the Linux fallback use the tag **`v10`**. That collision is the whole problem.
`chrome/browser/browser_process_impl.cc:1570-1604` registers `PosixKeyProvider` on every
non-Mac POSIX build — *"On Linux, it is used as a fallback."* So when Linux Chromium opens a
carried macOS `Cookies` database:

1. it reads a value tagged `v10`;
2. the tag matches `PosixKeyProvider`, so it decrypts with the hardcoded **`peanuts`** key —
   not the Mac's Keychain-derived key, which is a random secret that never left the Mac;
3. AES-128-CBC decryption fails the padding check → `std::nullopt`;
4. even in the ~1-in-256 case where garbage passes padding, the cookie store checks that the
   plaintext begins with `SHA256(domain)` and rejects it anyway
   (`net/extras/sqlite/sqlite_persistent_cookie_store.cc:986-998`);
5. the row is counted as `CookieLoadProblem::kDecryptFailed` and **`continue`d past** — no
   error to the user, no log in the UI. The cookie simply is not there.

The failure is *silent*. You get a browser that starts fine, looks fine, and is logged out.

### What survives and what does not

| profile data | survives a macOS → Linux copy? |
|---|---|
| **encrypted cookie values** (every session/auth cookie) | **No.** Dropped per the chain above. |
| **saved passwords** (`Login Data`) | **No.** Same OSCrypt key, same failure. |
| **payment/autofill secrets** | **No.** Same. |
| cookie *metadata* (host, name, path, expiry) | Yes — the rows exist, only the value is unreadable. Worse than useless: `auth <domain>` that only counts rows will report a healthy jar for a logged-out browser. |
| `Local Storage`, `Session Storage`, IndexedDB (LevelDB) | **Yes** — not OSCrypt-encrypted. A site that keeps its token in localStorage *may* still be logged in. That is a minority of sites and never the ones with real auth. |
| History, Bookmarks, Preferences, extensions, `Local State` | Yes, plaintext. |

So ADR 0003 §4 is **directionally right and one clause too optimistic**. It says the copy
"loses exactly the cookies that matter *unless the fork is run with the same storage backend*".
There is no way to run the macOS Keychain backend on Linux — the escape hatch it implies does
not exist for a Mac→Linux carry. (The clause *is* achievable for **Linux → Linux**: pin both
ends to `--password-store=basic` so both use `PosixKeyProvider`'s fixed `peanuts` key, and the
encrypted cookies then decrypt on the target. That is a real, supported carry path. It also
means the cookie jar is encrypted with a key that is a public constant in the source tree —
treat the tarball as a plaintext credential in transit and at rest.)

The ADR's operational rule stands and should be treated as mandatory:
**a carried profile is re-checked with `chrome-agent auth <domain>` per site on arrival, never
assumed** — and `auth` must probe the live site, not count cookie rows.

**Practical consequence:** "log in on the Mac, copy to the server" **does not work** as a way
to move sessions. It works only for moving *settings*. Moving a session to a Linux server means
either logging in on the server through the `share` window (ADR 0003 §3), or carrying from
another Linux box with matching `--password-store` settings.

*Source: this repository's own Chromium tree, files and line numbers cited above.*
**UNVERIFIED — needs a real Ubuntu run**: the exact behaviour of *this fork's* build, in case
a patch alters OSCrypt provider registration. The upstream code above is unpatched as far as
this spike checked.

---

## 4. systemd `--user` unit for the long-running headless browser

Same pattern as the prod box already uses for `messenger` and the browser-bridge services:
`systemd --user` under an unprivileged account, with lingering so it starts at boot without a
login session.

```bash
sudo loginctl enable-linger "$USER"      # once per host
mkdir -p ~/.config/systemd/user
```

`~/.config/systemd/user/chrome-agent.service`:

```ini
[Unit]
Description=chrome-agent headless Chromium fork
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=CHROME_AGENT_FORK=%h/chromium
Environment=CHROMIUM_AGENT_HEADLESS=1
Environment=CHROMIUM_AGENT_PROFILE=%h/chrome-agent-profile
Environment=CHROMIUM_SENDKEYS_DIR=%h/chrome-agent-sendkeys
ExecStart=/usr/bin/node %h/chromium/chromium-agent-launch.cjs
Restart=on-failure
RestartSec=5
# The profile is the credential. Keep it off every other account on the box.
UMask=0077

[Install]
WantedBy=default.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now chrome-agent
systemctl --user status chrome-agent
journalctl --user -u chrome-agent -f
```

Notes that matter:

- **`CHROMIUM_SENDKEYS_DIR` must be set to the spool the CLI will compute**, or the CLI talks to
  a spool no browser is watching. For the default profile `~/chrome-agent-profile` the CLI uses
  `~/chrome-agent-sendkeys`; any other profile path gets `~/chrome-agent-sendkeys-<hash12>`
  (`chrome-agent:82-88`). Get this from `chrome-agent spool`, do not guess it.
- **No `DISPLAY`.** If one leaks into the unit environment the headful path may be attempted.
- Do **not** put the share stack in this unit. Per ADR 0003 the share is a verb with a TTL, not
  a service. A VNC server that starts at boot is the 2026-07-06 incident again.
- **UNVERIFIED — needs a real Ubuntu run:** whether the fork's sendkeys watcher survives
  `Restart=on-failure` cleanly, and whether a restart orphans the spool's `results/` files.

---

## 5. What breaks first

In the order a new operator will actually hit them.

1. **There is no Linux binary.** The single most likely start: someone copies `out/Default`
   from the Mac and the launcher either can't find `out/Default/chrome` (the Mac has
   `Chromium.app/`, not `chrome`) or finds a Mach-O it cannot exec. The preflight's ELF check
   catches this; the fix is a Linux build, not a flag. §1.
2. **The build doesn't fit.** Path A on a default cloud VPS dies on disk or OOMs the linker.
   Both failures arrive hours in. Size the box before you start: ~100 GB, ≥16 GB RAM.
3. **The carried profile is logged out and nothing says so.** The browser starts, the cookie
   rows are present, and every site is signed out because the values won't decrypt. This one
   wastes the most time because it looks like a site problem, not a platform problem. §3.

After those three, in rough order:

4. **Missing shared libraries.** The binary exits immediately with a `libfoo.so.N: cannot open
   shared object file`. Diagnose with `ldd out/Default/chrome | grep 'not found'` — the
   preflight runs exactly this. Re-run group (a); if it's still short, the package was renamed
   in your release (the `t64` transition on 24.04) — the installer handles the common ones.
5. **Headful without a display.** Any launch without `CHROMIUM_AGENT_HEADLESS=1` and without
   `DISPLAY` pointing at a live Xvfb fails. Confirm the display with
   `DISPLAY=:99 xdpyinfo >/dev/null` before blaming Chromium.
6. **GPU/sandbox on a bare VM.** Expect to need `--disable-gpu`, possibly
   `--use-gl=swiftshader`; in a container, the setuid sandbox is usually unavailable.
   **UNVERIFIED — needs a real Ubuntu run.** Resist `--no-sandbox`: this browser holds live
   logged-in sessions, which is exactly the thing the sandbox protects.
7. **Spool mismatch.** The CLI computes the spool from the profile path; the systemd unit
   hardcodes one. A trailing slash difference is enough to fork one profile into two spools and
   two contending Chromium instances. Use `chrome-agent spool` as the source of truth. §4.
8. **`share` left running.** A killed shell that leaves a tunnel up is a logged-in browser on
   the public internet. ADR 0003 requires the TTL and the boot/watchdog reconciler for exactly
   this; until `share` is built, whoever runs the manual commands in §2 owns the teardown.
