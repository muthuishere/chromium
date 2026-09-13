# ADR 0008 — Producing the Linux build of the fork

- **Status:** **IN PROGRESS — first build running 2026-09-13.** Stage 1 (depot_tools + a checkout
  pinned to the fork's upstream base + build deps) is syncing on `deemwar-db1`. No Linux binary
  exists yet; every size number in this ADR is marked as an expectation until one does.
- **Date:** 2026-09-13
- **Owner:** Muthu (fork maintainer)
- **Author:** Claude Code (chrome-agent session)
- **Severity:** HIGH — ADR 0003 (chrome-agent on a server) is blocked on exactly this artifact.
- **Related:** ADR 0003 (server), ADR 0007 (releases across OS), `sync-upstream.sh`.

---

## Context

Everything in ADR 0003 — headless work on a server, a human login through a time-boxed share,
`browser:` as an identity that lives somewhere other than one laptop — needs a Linux Chromium with
our patches. There is none, and the reason it has never been produced is that the obvious routes
are all wrong:

- **A macOS build cannot be copied to Linux.** darwin produces a Mach-O inside `Chromium.app/`;
  Linux loads ELF and the launcher looks for a bare `out/Default/chrome`
  (`chromium-agent-launch.cjs:41-56`). This is not a packaging detail, it is a different binary
  format.
- **Shipping the source is 43 GB** (the working tree here, ~30 GB without `out/`). Uploading that
  to a build box over a home connection is hours of transfer to solve a problem that is not a
  transfer problem.
- **`fetch chromium` on the box gives upstream, not us.** Our patches have to get there somehow.

### The fact that makes this easy

**The entire fork is a 298 KB patch**: 56 files, +5,833 / -257 lines, over upstream Chromium commit
`a9b5091aa0fa2f7aa769c1c6b2ceea8038e891dc` (2026-07-13, Chromium 152.0.7948.0). That commit is the
shallow root of this checkout, and `sync-upstream.sh` already treats the fork as a commit stack
replayed onto one upstream base.

So the build host does not need our repository. It needs Google's Chromium at one revision, plus
298 KB.

## Decision

**Build on Linux from upstream-at-a-pinned-revision plus the fork patch.** Six steps, all niced:

```
1. depot_tools                     git clone (shallow)
2. .gclient pinned to the base     url = …/chromium/src.git@a9b5091aa0…, target_os = ["linux"]
3. gclient sync --no-history       the long download, from Google's servers
4. install-build-deps.sh           apt, --no-prompt --no-arm --no-nacl --no-chromeos-fonts
5. git apply agent-fork.patch      the 298 KB that IS the fork
6. gn gen out/Release + autoninja  non-component (ADR 0007), nice -n 19, capped jobs
```

The patch travels by `scp`, not by giving a build box credentials to a private repo. Nothing on the
build host needs a GitHub key, and the host never holds the whole fork history.

### Build host: the trade-off, stated plainly

The first build is running on **`deemwar-db1`** — the production **PostgreSQL 16** host. That is a
deliberate and uncomfortable choice, so it is written down rather than assumed:

- **Why it was picked:** it is by far the biggest machine available — 12 cores, 125 GB RAM, 504 GB
  free, load 1.0 at the time, Ubuntu 24.04 x86_64, which is also the target platform.
- **The risk:** a Chromium build is hours of saturated CPU and heavy I/O on the box that serves
  production data.
- **The mitigations, non-negotiable:** every long step runs under `nice -n 19 ionice -c3`, the
  compile is capped below the core count (8 of 12), and the build lives in `/root/cabuild` — nothing
  is installed into a path Postgres uses. `install-build-deps.sh` does add a large apt footprint to
  a prod box; that is the real cost and the main argument against repeating this.
- **This should not become the pattern.** A dedicated build box, or CI on `ubuntu-latest`, is the
  right long-term home. Doing it once to unblock ADR 0003 is defensible; doing it monthly is not.

### What the artifact contains

A Linux Chromium release build is **not one file**, even non-component. The launcher wants
`out/Default/chrome`, and `chrome` needs its resources beside it:

```
chrome                    the binary
chrome_100_percent.pak  chrome_200_percent.pak  resources.pak
icudtl.dat              snapshot_blob.bin  v8_context_snapshot.bin
locales/*.pak           chrome_crashpad_handler
libEGL.so libGLESv2.so libvk_swiftshader.so  vk_swiftshader_icd.json   (GPU/software GL)
```

The release tarball is that set, and only that set — never the whole `out/Release` (which carries
every test binary and object file). **Size is unknown until it links**; it will be reported here,
measured, not estimated.

### Verification, on a machine that did not build it

ADR 0007's five gates apply, with two Linux specifics:

- **ELF check first.** `file out/Default/chrome` must say `ELF 64-bit LSB executable, x86-64`. It is
  the cheapest possible guard against the single most likely mistake — someone copying a macOS
  build across — and `server-install.sh`'s preflight already does exactly this check.
- **`ldd` must resolve everything** on a *clean* Ubuntu, not on the build host, which has every
  build dependency installed and will hide a missing runtime library.
- Headless is the default on a server, so gate 5 (`chrome-agent read`) runs headless; the headful
  path needs Xvfb and belongs to ADR 0003's share.

## Alternatives rejected

| Option | Why not |
|---|---|
| **Copy the macOS build** | Mach-O vs ELF. Not a packaging problem. |
| **rsync the 30 GB source** | Hours of upload to avoid sending 298 KB. |
| **Clone the fork on the build box** | Needs a GitHub key on a production host, and pulls the whole history for a patch we can hand it directly. |
| **Build in Docker on the Mac** | An x86_64 Chromium build under emulation on arm64 — hours becomes days. Docker *did* earn its keep for testing the CLI and installer on real Ubuntu; it is the wrong tool for the compile. |
| **Cross-compile darwin → linux** | GN supports a sysroot cross-build in principle; it has never been tried for this fork, and a release path nobody has walked is not a release path. |
| **Build on `deemwar-app1/2`** | Smaller, and they serve live traffic (messenger, the crypto desk). The DB box had the headroom; neither app box does. |

## Consequences

- ADR 0003 becomes testable end to end for the first time: a real Linux fork means Xvfb, the share,
  and a human login through noVNC can actually be tried.
- A production database host spends hours at high load and gains a large apt footprint. Accepted
  once, deliberately, with mitigations; re-examined before it happens again.
- The release path now depends on one pinned upstream revision. When `sync-upstream.sh` moves the
  base, the Linux build must be redone — that is the point of recording `upstream_base` in the
  manifest (ADR 0007).

## What would prove this

1. `file out/Release/chrome` reports an x86-64 ELF, and the tarball unpacks and runs **on a
   different Ubuntu machine** than the one that built it.
2. `chrome-agent up --headless` on that machine reports `undetected`, and `chrome-agent read
   news.ycombinator.com` returns items.
3. `chrome-agent-selftest.cjs` passes against the Linux artifact.
4. The measured artifact size is recorded here, replacing the word "unknown".
