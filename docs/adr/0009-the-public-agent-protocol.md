# ADR 0009 — The public agent protocol: instances, media, per-tab streams, and consent

- **Status:** **PARTLY BUILT, 2026-09-13.** The VERSION handshake (§6) and the instance registry
  (§2) are written in the engine and compile; the Go client sends VERSION in `doctor` and reads the
  registry files. NEITHER has run in a browser — the VERSION-speaking `chrome` binary was never
  built. The media source/sink model, the per-tab view/control grants, and the token-gated WS
  control plane (§1, §3, §5) are NOT built; they remain the design of record and supersede ADR
  0003's X/VNC login.
- **Date:** 2026-09-13
- **Owner:** Muthu (fork maintainer)
- **Author:** Claude Code (chrome-agent session)
- **Severity:** HIGH — it decides what other people's software is allowed to talk to, and it is the
  first design in this project with an attacker in the threat model who is not us.
- **Related:** ADR 0001 (spool + tabId), ADR 0002 (audio in/out), ADR 0003 (server + login),
  ADR 0006 (CLI vs engine), ADR 0010 (the Go client), ADR 0011 (state export/import).

---

## Context

The fork already has most of the powers a public agent browser needs, built for our own use and
reachable only through a bash script:

| Capability | Mechanism | State |
|---|---|---|
| tabs/windows, navigate, eval, DOM, screenshot | spool verbs + stable tabId UUIDs | shipped (ADR 0001) |
| audio **in** per tab (push PCM as the mic) | `AUDIOSTART` → `ws://127.0.0.1:<port>/mic` | shipped, verified (ADR 0002) |
| audio **out** per tab (tap what the tab plays) | `/tap` | shipped, verified 5/5 |
| video **in** per tab (fake camera) | `VIDEOSTART` + I420 → `/cam` | shipped |
| undetectable, real logged-in profile | fork patches | the moat |
| video **out** / screencast | — | missing |
| per-tab audio device selection | — | missing |
| instance discovery, window management | — | missing |
| any notion of who is allowed to do this | — | **missing, and that is the problem** |

Opening this to other people changes the question from "what can it do" to "what may a caller do,
and who decided". Every safety property today lives in the bash CLI — staged writes, `--confirm`,
the pacing floor. **A socket that exposes the engine deletes all of them**, because anyone can write
their own client and skip the CLI entirely.

## Decision

### 1. Two planes, on purpose

**Control stays on the spool.** A file-drop directory has no port, no auth surface, survives
restarts, is ordered, and its failures are visible (the `cli-crash` contract exists because a
swallowed crash once caused a double-post). It stays the local, zero-config path.

**Streams are WebSockets**, as `/mic`, `/cam` and `/tap` already are. The spool cannot stream and
should not learn how.

**Third parties get one WS control plane, and it is off by default**: `chromium-agent serve
--token`, bound to `127.0.0.1`, token generated per run and printed once. Not enabled, not
discoverable, not implicit. An always-on unauthenticated localhost port attached to a logged-in
browser is precisely the 2026-07-06 incident, and it is the single most likely way this project
hurts someone.

### 2. Instances are a registry, not path arithmetic

The spool is currently derived by hashing the profile path. Two real bugs lived there: a trailing
slash forked one profile into two spools, and a `basename` collision merged two profiles into one.
That is acceptable for a private tool and unacceptable as "find the browser".

The engine writes `~/.config/chromium-agent/instances/<id>.json` at startup — `{pid, profile,
spool, ws_port, headless, started_at, protocol}` — and removes it at exit. Staleness is decided by
pid liveness, the same check that caught the dead `SingletonLock` (a symlink whose target never
exists, so `-e` reported "free" while a browser was running).

### 3. Media is one symmetric noun

Not `SETMIC` / `SETSPEAKER` / `SCREENCAST` as three unrelated verbs. Per tab:

- a **source** — what the page believes its microphone or camera is: a file, a WS stream, a real
  device, or silence;
- a **sink** — what leaves the tab: its audio, or its video (screencast).

Four verbs: `attach`, `detach`, `list`, `stat`. Screencast stops being a new subsystem and becomes
the `/cam` pipeline reversed, reusing plumbing that exists.

### 4. Encoding happens in the engine. This is not negotiable

```
1080p I420 raw:     3.11 MB/frame → 93 MB/s → 746 Mbps at 30fps
VP9 / H.264 stream: ~3 Mbps → 0.4% of that
```

Raw frames over a socket are impossible over a tunnel and wasteful over loopback. The engine
already owns encoders (VP8/VP9 always; H.264 once `proprietary_codecs` is enabled — the codec-enabled fork.2, not fork.1), often
hardware-backed. **A sink emits an encoded stream; no client ever handles raw frames.** This is also
what makes the client's language a developer-experience decision rather than a performance one
(ADR 0010).

### 5. A tab may be granted to a human — view and control are different rights

```
grant  {tabId, right: view|control, ttl, token}   → one URL
revoke {grantId}                                  → instant
```

- **view** — the encoded stream leaves that tab.
- **control** — mouse and keyboard events enter that tab.

Scoped to **one tabId**, never a window and never the profile. TTL mandatory. Revoke instant. The
tab carries a visible indicator for as long as a grant is live. **Enforced in the engine**, because
a check in the client is not a check.

This replaces ADR 0003's Xvfb → x11vnc → noVNC stack for login. The old design exposed a whole
desktop on Linux only; this exposes one tab, works headless, works on macOS, and the human signs in
on their phone. The flow becomes: open a fresh tab at the login page → `grant control` → human signs
in → `revoke` → `auth <domain>` confirms it took.

**State export is NOT reachable through a grant.** A remote viewer must never be able to exfiltrate
the identity it was lent (ADR 0011).

### 6. A versioned handshake, from the first public byte

`hello` returns `{protocol: N, capabilities: [...], engine_version, chromium_version}`. ADR 0006
already names the failure: a client newer than its engine discovers that as a command that hangs
forever. `doctor` currently probes capabilities by trying them under timeouts — a decent workaround
while we are the only user, and support load the moment we are not.

## Alternatives rejected

| Option | Why not |
|---|---|
| **Make the spool the public API** | No streaming, polling latency, results as files, no backpressure — and it cannot do screencast at all. |
| **Adopt CDP** | Re-introduces the detectable surface the fork exists to remove, and an open debugging port is RCE-equivalent on the profile. |
| **Always-on localhost port** | The 2026-07-06 incident, by design instead of by accident. |
| **Raw frames to the client** | 746 Mbps for 1080p30. |
| **Whole-desktop VNC (ADR 0003 as written)** | Three moving parts, Linux-only, and it lends out the entire browser to grant one login. |
| **Permissions in the client** | Anyone can write a client. The engine must own every invariant that matters. |

## Consequences

- The engine grows an auth surface and a consent model, and becomes the place safety lives. The CLI
  stops being load-bearing for security, which is the right direction anyway.
- ADR 0003 loses its X stack and its macOS refusal; both become unnecessary.
- A protocol version becomes a maintained artifact with a compatibility policy.
- Third-party clients become possible, which is the point, and also means every mistake here ships
  to people who cannot read our source.

## What would prove this

1. A tab granted `view` for 60s streams to a phone over a tunnel at a few Mbps, and the URL is dead
   the second the TTL passes — checked from off-box.
2. A `control` grant lets a human complete a real login on a **headless** instance, and
   `chrome-agent auth <domain>` then reports `signed_in: true` on that machine.
3. A grant on tab A gives no access to tab B, the window, or any other profile.
4. `hello` against a deliberately older engine reports the skew and names the verbs that will fail,
   instead of hanging.
5. Revoking mid-stream cuts the stream and removes the indicator.
