# ADR 0003 — chrome-agent on a server: headless fork, human login over a time-boxed share

- **Status:** **HALF BUILT AND HALF PROVEN, 2026-09-12.** The CLI side exists: `up --headless`
  (proven end to end on a throwaway profile), `login` refuses under headless and points here,
  `doctor`, and `chrome-agent share` wired to `scripts/share.sh`. **The tunnel half is proven on
  macOS** with a dummy http.server: tunnel up, fetched from off-tunnel, torn down, URL then 530,
  no process and no listener left — and the TTL enforced unattended after the starting shell
  exited. **The X half is entirely unproven** — Xvfb → x11vnc → noVNC, the fork headful on a
  virtual display, and a human login through it have never run, because nothing in this project has
  ever run on Linux. §4 was CORRECTED before anything was built on it: the profile-carry escape
  hatch it implied does not exist across operating systems.
- **Date:** 2026-09-12
- **Owner:** Muthu (fork maintainer)
- **Author:** Claude Code (chrome-agent session)
- **Severity:** HIGH — this is what makes `browser:` an identity that can live anywhere, instead
  of one that lives on one laptop.
- **Related:** ADR 0004 (site definitions as assets), ADR 0005 (profile + session lifecycle),
  ADR 0006 (the CLI/browser split), apl ADR-0007 (`login` as a capability), apl ADR-0008 (manual
  login only).

---

## Context

chrome-agent drives a patched Chromium. That fork is the reason the whole thing works —
`navigator.webdriver === false`, a real profile, no CDP — and it is also the reason the thing has
never left one Mac. `CHROME_AGENT_FORK` (2026-09-12) made the path configurable, which is
necessary and nowhere near sufficient: a server still has no display, and **every site worth
driving requires a login that only a human can complete.**

That is the actual problem. Not headless rendering — headless already works. The problem is:

> A profile on a server is signed out. Signing it in requires a human at a browser window.
> The server has no window.

Three constraints make this harder than it sounds:

1. **No credential may reach an agent.** apl ADR-0008 is absolute: the human types, always. So
   "store the password in the server's env and automate the form" is not a degraded option, it is
   a prohibited one. It is also the fastest way to get an account restricted.
2. **A session dies.** Cookies expire, a site forces re-auth, a security check fires. So this is
   not a one-time provisioning step — it is a recurring operation that must be cheap and safe at
   3am from a phone.
3. **Whatever exposes that window is a remote hand on a logged-in browser.** It is the highest-
   value thing in the system. A permanently exposed one is a breach waiting for a crawler — and we
   have already done exactly that once: a stale, unauthenticated browser-bridge on a public tunnel
   ran for days as root (retired 2026-07-06). That incident is the reason this ADR exists in this
   shape.

## Decision

**A server runs the fork headless for work, and grows a temporary, authenticated, time-boxed
window for login.** Four parts:

### 1. Headless is the default on a server, headful stays the default on a desktop

The launcher already supports `CHROMIUM_AGENT_HEADLESS=1`. The CLI gains `--headless` / a config
default so a server never tries to open a window it cannot open.

This also fixes the ugliest operational failure recorded so far: launched from cron or ssh on
macOS, Chromium dies with `bootstrap_look_up … No rendezvous client`. `up` already names that and
points at headless; on a server it simply never happens.

### 2. Login happens in a real window on a virtual display

On Linux: `Xvfb` gives the fork a display, so it runs **headful** — a real window, a real UI, real
2FA, real "verify it's you" interstitials. Headless Chromium is the wrong tool for a login page;
too many sites treat it differently, and a human cannot see it.

So a server has two modes for the same profile:
- **work**: headless, no display, no exposure;
- **login**: headful on Xvfb, exposed for as long as it takes and no longer.

### 3. The share is a verb, and it is off by default

```
chrome-agent share start [--ttl 15m]     # -> one URL, a one-time token, an expiry
chrome-agent share status
chrome-agent share stop
```

`share start` brings up the virtual display, a VNC server bound to **127.0.0.1 only**, a
browser-facing client on localhost, and a Cloudflare tunnel pointed at that local port. It prints
one URL. The design rules, all of which come from the 2026-07-06 incident:

- **Never bound to a public interface.** Only the tunnel reaches it, and only while it runs.
- **A TTL is mandatory**, default 15 minutes, enforced by a timer that kills the tunnel, the VNC
  server and the display — not by the operator remembering.
- **A token in the URL**, single use where the client supports it. A quick-tunnel hostname is
  unguessable but it is not a secret; it is in `cloudflared` logs and in the operator's shell
  history, so it is not the only control.
- **Every start and stop is logged** to the action ledger with the profile and the reason.
- **`stop` is idempotent and always safe to call**, including from a phone, including twice.
- **State is a file, not a shell** — a session that dies must not leave a tunnel up. Boot and
  watchdog both reconcile: a share whose expiry has passed is torn down.

**Two bugs the TTL test caught, both of which silently disabled the security property** (2026-09-12):
a `nohup … &` timer **died with its parent shell**, so the TTL stopped being enforced in exactly the
case it exists for — fixed by starting the timer in its own session and having it ignore HUP/INT/TERM.
And cancelling that timer with `pkill -f <marker>` killed the wrong process: it matched an unrelated
shell that merely mentioned the marker, and when the timer itself called `stop` the pattern kill was
suicide mid-teardown — the tunnel stayed up and nothing was logged. Fixed by cancelling the process
group, skipping when the caller is inside it, and writing state and ledger *before* the kill.
Neither bug is visible in a happy-path test; both were found by letting the TTL actually fire.

`share` is not a remote-control feature. It is a login window with a lock on it. It should feel
like handing someone a key, watching them use it, and taking it back — which is exactly how the
chrome-agent skill already describes the browser-bridge remote tunnel.

### 4. A profile can also be carried — but not across operating systems

The fast path looks obvious: log in on a desktop, `tar` the profile, ship it to the server. It
needs no tunnel and would be the right answer for provisioning a machine. **It does not work from
macOS to Linux, and it fails silently**, which is worse than failing.

Read out of this fork's own source on 2026-09-12:

- Cookie ciphertext carries a provider tag, and decryption picks the key by matching that tag
  (`components/os_crypt/async/common/encryptor.cc:253-260`).
- macOS tags **`v10`** and derives the key with PBKDF2-HMAC-SHA1 (1003 iterations, salt
  `saltysalt`) over the random Keychain password stored as "Chrome Safe Storage"
  (`keychain_key_provider.mm:28-66`).
- Linux's POSIX fallback provider **also tags `v10`**, with a key derived from the hardcoded
  constant `peanuts` (`posix_key_provider.cc:17-23`), registered on every non-Mac POSIX build
  under the keyring provider (`browser_process_impl.cc:1590-1596`).

The tags collide. Linux decrypts the Mac's `v10` blobs with the wrong key, padding fails, and the
cookie store additionally rejects any plaintext that does not begin with `SHA256(domain)`
(`net/extras/sqlite/sqlite_persistent_cookie_store.cc:986-998`). The row is skipped as
`kDecryptFailed`. **No error is surfaced anywhere.**

So a carried profile arrives on Linux with its history, bookmarks, preferences and Local
Storage/IndexedDB intact — and every session cookie gone. A handful of sites that keep their
session in local storage still work, which is exactly enough to make it look like "some sites are
being weird" rather than "the credential did not survive the trip". The cookie *rows* are still
there, so any check that counts rows will green-light a logged-out browser.

Rules that follow:

- **Carrying is Linux → Linux only.** Both ends pinned to the same backend (`--password-store=basic`
  makes both use the `peanuts` constant) — and then be honest that the jar is encrypted with a
  public constant, so the tarball **is** a plaintext credential and must be treated like one.
- **Mac → Linux: do not carry. Log in through the share.** That is the entire reason §3 exists.
- **Always re-check per site on arrival** with `chrome-agent auth <domain>`. Never assume. This is
  mandatory, not advisory — it is the only thing that catches the silent case.

### macOS: `share start` refuses

A share exists because a server has no screen. A Mac has one, so exposing a logged-in browser over
a tunnel to save a glance at your own display is all of the risk and none of the benefit — and a
degraded mac mode is precisely the one that would get used casually and then go stale on a tunnel.
`share.sh selftest` keeps the tunnel half testable on a Mac with a dummy server, never the real
browser.

### The token is 32 bits, and that is a deliberate limit

noVNC's `?password=` is the VNC password, so the effective secret is 8 hex characters — adequate for
a 15-minute window behind an unguessable hostname, and *not* adequate for anything longer. If this
graduates from a spike, Cloudflare Access in front of it is the fix, not a longer VNC password.

## Alternatives rejected

| Option | Why not |
|---|---|
| **Automate the login form with stored credentials** | Prohibited (apl ADR-0008) and the fastest route to a restricted account. A secret in an agent's reach is a secret leaked. |
| **Chrome DevTools Protocol / remote debugging port** | Re-introduces the detectable surface the fork exists to remove, and an open debugging port is a full RCE-equivalent on the profile. |
| **A permanent VNC/noVNC endpoint behind a login page** | This is the 2026-07-06 failure with a nicer hat. Permanent exposure of a logged-in browser is not an operational convenience, it is the attack. |
| **Ship cookies over the wire per site** | A cookie jar in transit and at rest, per site, forever — all the risk of a credential with none of the revocability. |
| **Headless login with a code typed into the CLI** | Some sites do work this way. Most do not, and the ones that do change without warning. It would make the happy path fragile and the unhappy path invisible. |

## Consequences

- A `browser:<label>` identity becomes provisionable on a box the owner does not sit at, which is
  what makes it sellable at all (apl can sell `github:` and `whatsapp:` today; this is the gap).
- The server grows two dependencies it did not have: an X stack (`Xvfb`, a VNC server, a web VNC
  client) and `cloudflared`. All are install-time, none run unless `share` is called.
- There is a new, small, extremely sensitive surface. It must be reviewed as such — the TTL, the
  bind address, and the teardown are the security properties, not the feature list.
- Cron/watchdog work on a server stops being a special case: headless is the normal mode.

## What would prove this

1. The fork **builds and runs on Ubuntu** and `chrome-agent up` reports `undetected`. Until then,
   every claim here is macOS-only. (Nothing about the patch is macOS-specific; nothing about that
   sentence is evidence.)
2. `share start` yields a URL that shows the real browser window, a human logs into one site
   through it, and `chrome-agent auth <domain>` **on the server** reports `signed_in: true` with
   the right identity.
3. `share stop` — and the TTL, unattended — leaves no listening port, no tunnel process, and no
   reachable URL. Checked from off-box, not from the box.
4. A killed session leaves no share running: kill the shell mid-share and confirm the reconciler
   tears it down.

## Open questions

- **Which VNC client?** noVNC is the obvious one and adds a web server. A native VNC client over
  the tunnel avoids that but makes "open this on your phone" worse. Leaning noVNC for the phone
  case, which is the case that matters at 3am.
- **Cloudflare quick tunnel or a named tunnel on `deemwar.com`?** A quick tunnel needs no DNS and
  dies with the process (good); a named tunnel gives a stable URL (bad — a stable URL for this is
  a liability) but survives restarts and can sit behind Cloudflare Access with a real identity
  check (very good). Probably: named tunnel + Access for the owner's own boxes, quick tunnel for
  one-off machines.
- **Is `auth` enough to drive re-login automatically?** A watchdog that notices `signed_in: false`
  could raise a share and message the owner a URL. That is the genuinely useful version of this,
  and also the version where a bug means an unattended public browser. Not in the first cut.
