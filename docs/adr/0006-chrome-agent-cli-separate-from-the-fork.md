# ADR 0006 — A distributable `chrome-agent` CLI, separate from the fork it drives

- **Status:** **PROPOSED — this is the one to argue about before building.**
- **Date:** 2026-09-12
- **Owner:** Muthu (fork maintainer)
- **Author:** Claude Code (chrome-agent session)
- **Severity:** HIGH — it decides what ships, what versions against what, and whether `browser:`
  is a product or a workstation setup.
- **Related:** ADR 0003 (server), ADR 0004 (site assets), ADR 0005 (lifecycle), the embed-boundary
  decision (push chrome-agent mechanics into the fork), apl ADR-0009.

---

## Context

Today one 500-line bash script lives inside a 1.4 GB Chromium checkout and does five different
jobs: it locates and launches the browser, speaks the spool protocol, holds per-site knowledge,
implements social verbs, and manages profiles and sessions. `CHROME_AGENT_FORK` made its *path*
configurable; it did not make it a *thing you can install*.

There is also a decision already on the books pulling the other way: **push chrome-agent mechanics
into the fork** (the tabId registry, eval-await, the primitives), leaving DOM recipes and the
profile in the client. That decision is right and this ADR must not contradict it. The question
here is not "fork or client" — it is **where the seam is**, and what ships on each side of it.

Three forces:

1. **They version differently.** A site changes weekly. The fork changes when Chromium is rebased
   — a heavyweight, occasional, painful event. Binding "LinkedIn moved a button" to "rebuild
   Chromium" is the wrong coupling, and it is the coupling we have.
2. **They install differently.** The CLI is a script with `node` and `python3`; it could be an npm
   package installed in seconds on any machine. The fork is a build. On a server the fork may be a
   downloaded artifact, but it is never `npm i`.
3. **The fork is the moat.** Undetectability is the product. The CLI without the fork is a worse
   Playwright; the fork without the CLI is a browser nobody drives.

## Decision

**Split into three artifacts with one protocol between them.**

```
chrome-agent (CLI)  ──spool protocol──>  chromium fork (engine)
        │
        └── sites/*.json  (assets, installed to ~/.config/chrome-agent/sites — ADR 0004)
```

- **The engine** is the fork: the spool watcher, tabId registry, `EVALASYNC`, screenshots, the
  audio taps, the undetectability patches. Everything mechanical keeps moving *into* it, per the
  embed-boundary decision. Its public surface is the spool protocol and the launcher.
- **The CLI** is what a human or an agent types. It owns discovery of the engine
  (`CHROME_AGENT_FORK`), the profile/session lifecycle (ADR 0005), verbs, exit codes, the ledger,
  and the pacing floor. It is installable on its own and contains no Chromium.
- **The site assets** ride with the CLI and install to a user-editable path (ADR 0004), because
  they change fastest of all.

**The protocol — not the repo layout — is the compatibility boundary.** It needs a version the CLI
can ask for: `chrome-agent doctor` should be able to say "this CLI needs spool protocol ≥ N, your
fork speaks N-2, these verbs will fail" instead of a command that hangs forever. Today a CLI that
is newer than its fork discovers it as a timeout, which is the worst possible error.

### What that means concretely

- The CLI keeps living in this repo for now (`skills/chrome-agent/`), and is *packaged* from here.
  Moving the source out is a separate, later decision, and it is not required to ship.
- Publishing target is an npm package (`@deemwarhq/chrome-agent`) exposing `chrome-agent` — the
  same shape apl already uses for `wacli` and friends, so apl's adapters need no new concept.
- The fork ships as a built artifact per platform, fetched or built once; the CLI errors clearly
  when it is absent (it already does) and, with this ADR, when it is *too old*.

## The discussion this ADR exists for

**Should the CLI stay bash?** It is bash because it started as five lines around `node`. It is now
the thing that decides whether a write publishes. Arguments to move it to Node: the recipes are
already JS, `recipes-list.mjs` and `recipe-run.mjs` are already JS, JSON parsing is native (the
script currently shells out to `python3` a dozen times per invocation), tests are possible, and an
npm package with a bash entrypoint is a slightly embarrassing shape. Arguments to keep bash: it
works, it is obvious, it has no dependency tree, every operational failure so far was a browser
problem and not a language problem, and a rewrite is a chance to lose the traps encoded in its
comments. **Leaning: keep bash for the verbs, move anything structured into the existing .mjs
files, and revisit only when a second consumer needs it as a library.**

**Does the CLI belong in the Chromium repo at all?** A skill inside a 1.4 GB fork is not
installable by anyone but its author, and a `git clone` of this repo to get a shell script is
absurd. But splitting it now means two repos, two review paths, and a version skew between the
CLI and the engine *before* there is a protocol version to manage it with. **Leaning: protocol
version first, then split — in that order, not the reverse.**

**Is there a second client?** apl's browser adapter execs the CLI. If anything ever wants this as a
library rather than a process, the bash decision flips immediately. Nothing does today.

## Alternatives rejected

| Option | Why not |
|---|---|
| **Keep everything in the fork, ship nothing** | This is today. `browser:` cannot be sold, and a site fix requires a Chromium checkout. |
| **Vendor the CLI into apl** (what apl did until ADR-0009) | Two copies drift; apl ends up owning site knowledge it cannot verify because it does not run the browser. |
| **Put the site verbs in the fork as C++** | Ships a site re-skin as a browser rebuild. The worst possible version coupling. |
| **Make the CLI talk CDP so it works with stock Chrome** | Throws away the only differentiator. A detectable chrome-agent is not a cheaper chrome-agent, it is a different, worse product. |

## Consequences

- A protocol version becomes a real, maintained thing — `doctor` is the first consumer and the
  reason to add it.
- The CLI can be installed and updated on a server without touching the browser build (ADR 0003's
  weekly reality: sites change, engines do not).
- Two things now need release discipline where one did, and a skew bug becomes possible. The
  version handshake is what keeps that from being discovered as a hang.
- The embed-boundary decision keeps draining mechanics into the fork; this ADR says that is fine as
  long as each drained thing is *mechanism*, never *site knowledge*.

## What would prove this

1. `chrome-agent doctor` on a machine with a deliberately old fork reports the skew and names the
   verbs that will fail — instead of hanging.
2. The CLI installs from a package on a clean machine, finds a fork via `CHROME_AGENT_FORK`, and
   runs `auth`/`verify` with no repo checkout present.
3. A site fix ships as a site-asset change with no engine release.
