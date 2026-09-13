# ADR 0010 — The client is one Go binary, and bash retires

- **Status:** **SLICES 1-3 BUILT, 2026-09-13.** One static binary, ~3.5 MB, five targets from one
  laptop, proven on a bare Ubuntu container with no node and no python3. It now covers the whole
  read/identity/operational surface — spool protocol, instances, `doctor` (with the VERSION
  handshake), `status`, `hello`, `goto`, `eval`/`evalcsp`, `recipe`/`recipes`, `read`, `verify`,
  `auth`/`login`/`logout`, `sites`, `profile`, `ledger`, `note`/`promote`, `install`, `tabs reap`,
  and `cookies export`/`import` — with no node or python3 at runtime. Recipe execution is a
  byte-identical Go re-extraction of the registry (48 verbs, matched against node). Parity with bash
  is 14/14. Bash is frozen and retires once the engine and stream verbs have run against a live
  VERSION-era build.
- **Date:** 2026-09-13
- **Owner:** Muthu (fork maintainer)
- **Author:** Claude Code (chrome-agent session)
- **Severity:** MEDIUM — it replaces a working thing. The risk is not the language; it is losing
  what the working thing knows.
- **Related:** ADR 0006 (CLI vs engine — this closes its open question), ADR 0009 (protocol),
  ADR 0011 (state export/import), ADR 0007 (releases).

---

## Context

The CLI is **1,423 lines of bash with 51 `python3` shell-outs**. It works, and most of its length is
not logic — it is *traps*, paid for in real incidents: the HttpOnly cookie probes, the fail-closed
rule, spool keying (a trailing slash forked one profile into two spools; a `basename` collision
merged two), the stale `SingletonLock` symlink that made `-e` lie, the 44-hour LinkedIn origin
lesson, the crash-visible `cli-crash` contract that exists because a swallowed crash caused a
double-post.

It has also just been shown to be a portability problem. Running it on real Ubuntu found three bugs
in one evening, every one of them a path or environment assumption: `SKILL` hardcoded to
`~/.claude/skills/...`, the recipe registry hardcoded to one checkout, and a vendored ESM directory
with no `package.json`. Each was invisible on the machine that wrote it.

And ADR 0009 changes the job description. A client that streams encoded video, forwards input
events, maintains WS connections and manages grants is not a shell script with a `python3` helper
per JSON parse.

## Decision

**One statically-linked Go binary. Bash is deleted at parity, not kept "just in case".**

### Why Go, and why the performance worry lands elsewhere

ADR 0009 moved encoding into the engine, for arithmetic that decides this:

```
1080p I420 raw:  3.11 MB/frame → 93 MB/s → 746 Mbps at 30fps
VP9 / H.264:     ~3 Mbps → 0.4% of that
```

746 Mbps is impossible over a tunnel and pointless over loopback, so the engine encodes and **no
client ever touches a raw frame**. What is left for the client is WS I/O, JSON, process management
and file handling at single-digit Mbps — a workload no modern language loses at.

So the choice is developer experience, and Go wins it here:

| | Go | Rust |
|---|---|---|
| six targets from one machine | `GOOS`/`GOARCH`, static binaries, no toolchain zoo | per-target toolchains; `cross` is a build system to own |
| embedded assets | `go:embed`, exactly the shape the assets already have | workable, clunkier |
| this workload | ideal | ideal |
| the owner's stack | reqsume, cryptodesk, apl are Go | one maintainer, no leverage |

Rust would win if the client owned a zero-copy realtime pipeline. It does not, because we designed
that out. Choosing Rust would be paying a real cost for a bottleneck that no longer exists.

If a genuinely hot path ever appears — transcode, mix, record — it belongs in the engine (where the
frames already are) or in an `ffmpeg` subprocess. Never in a client rewrite.

### What dies with bash, and that is most of the point

`python3` as a hard runtime dependency (51 invocations, one per JSON parse), the
`shasum || sha256sum` fallback, the `timeout`-may-not-exist fallback, quoting traps (an apostrophe
in a comment once broke a `python3 -c` string and the whole script), and `$HOME`-shaped path
assumptions. A Go binary has one runtime dependency: itself.

### Assets: `go:embed`, with the installed copy still winning

Site definitions and vendored recipes compile into the binary, and
`~/.config/chrome-agent/sites/` still overrides them. That rule already exists and is proven (ADR
0004); the port inherits it rather than inventing one. `go:embed` is what makes "default available
as embedded assets" literal instead of a packaging step.

### The port is a strangler, and the selftest is the specification

**`scripts/selftest.sh` is the acceptance suite** — 25 checks that already encode the contract: exit
codes, JSON shapes, the fork guard, both spool-keying bugs, the profile-delete guards, the site
resolution order, and auth/logout/read against a synthetic site. A clean-room rewrite would lose the
traps silently; a rewrite that must go green against this suite cannot.

Three slices, each green before the next starts:

1. **protocol + instances** — spool client, instance registry, `doctor`/`hello`, exit codes.
2. **identity + sites** — `auth`, `login`, `logout`, `read`, `verify`, `sites`, `profile`,
   embedded assets, `promote`.
3. **streams + grants + state** — media source/sink, per-tab `view`/`control` grants (ADR 0009),
   `cookie export`/`import` (ADR 0011).

Bash remains the reference implementation until slice 3 passes, then is deleted in one commit. Two
implementations of a safety contract is how they drift.

### Every new verb ships in Go only

`grant`, `stream`, `cookie export/import` and the media verbs are never written in bash. The bash
CLI is frozen at its current feature set the day slice 1 starts.

## Alternatives rejected

| Option | Why not |
|---|---|
| **Keep bash, add Go only for streaming** | Two clients, two safety models, one protocol — the drift is the bug. |
| **Rust** | See above: the bottleneck it would fix was designed out of existence. |
| **Node** | Ships a runtime and a `node_modules` tree to every server; the engine already carries enough weight. |
| **Keep bash as a fallback after parity** | An untested fallback is a liability wearing a safety blanket. |

## Consequences

- One binary per platform, cross-compiled from any machine; no `python3`, no `shasum`, no `timeout`.
- The CLI stops being a portability problem, which is what the Ubuntu run proved it was.
- A rewrite risk exists and is mitigated by exactly one thing: the selftest must stay honest, and
  every trap the bash encodes in a comment must arrive in Go **as a test**, not as a comment.
- The Go client and the engine version independently (ADR 0006), so the protocol handshake in
  ADR 0009 stops being optional.

## What would prove this

1. The Go binary passes all 25 selftest checks on macOS **and** on Ubuntu, from one cross-compile.
2. `chrome-agent auth linkedin.com` returns the same verdict and the same identity as the bash CLI,
   on the same profile.
3. No `python3` on the box, and everything except the browser still works.
4. A fresh machine: download one binary, `install`, `doctor` — with no repo, no Node, no Python.
