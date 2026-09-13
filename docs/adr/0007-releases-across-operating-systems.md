# ADR 0007 — Releasing the fork across operating systems

- **Status:** **PROPOSED.** Nothing is released today. One load-bearing fact was **proven on
  2026-09-13**: the current macOS build is not shippable at all (see Context). The first Linux
  build is in progress under ADR 0008.
- **Date:** 2026-09-13
- **Owner:** Muthu (fork maintainer)
- **Author:** Claude Code (chrome-agent session)
- **Severity:** HIGH — it decides what "a release" even is, and whether `browser:` can be handed to
  anyone who is not sitting at this laptop.
- **Related:** ADR 0003 (server), ADR 0006 (CLI vs engine), ADR 0008 (producing the Linux build).

---

## Context

A "release" of this system is not one thing. It is three, and they change on completely different
clocks:

| Artifact | Changes when | Size | Ships how |
|---|---|---|---|
| **the engine** (patched Chromium) | an upstream rebase, or a fork patch | hundreds of MB | per OS + arch |
| **the CLI** (`chrome-agent`) | weekly | tens of KB | once, all platforms |
| **site assets** (`sites/`, recipes) | a site re-skins — daily | tens of KB | with the CLI, installed editable |

ADR 0006 already split these. This ADR is about the first one, because it is the only one that is
hard, and about the rule that binds all three: **a release is the set that was verified together.**

### The thing that forced this ADR

`out/Default` here is `is_component_build = true`. The `Chromium` executable in the bundle is
**34,944 bytes** — a launcher stub. The actual browser is **524 dylibs, 699 MB**, sitting loose in
`out/Default` *next to* the app, and the launcher's rpath list includes `@loader_path/../../..`,
which resolves to that build directory.

Copying `Chromium.app` (109 MB) somewhere else and running it gives, verbatim (2026-09-13):

```
dyld: Library not loaded: @rpath/libc++_chrome.dylib
  tried: …/Chromium.app/Contents/Frameworks/libc++_chrome.dylib (no such file)
         …/Chromium.app/Contents/MacOS/../../../libc++_chrome.dylib (no such file)
```

So the artifact that *looks* like the product — a 109 MB `.app` — is a shell that only runs while it
sits inside the build tree. **This is the failure mode that ships silently**: it works perfectly on
the machine that built it and dies on the first machine that isn't.

## Decision

### 1. Releases are built non-component, in their own output dir

`out/Release`, never `out/Default`. Iteration keeps the component build (fast links); releases get
`is_component_build=false`, `is_debug=false`, `dcheck_always_on=false`, `symbol_level=0`. Everything
links into one binary (Linux) or into `Chromium Framework.framework` inside the bundle (macOS), and
the loose libraries disappear.

The two configs coexist. Nobody should have to choose between "fast to build" and "shippable" —
they are different directories.

### 2. Relocation is a gate, not a hope

Every artifact must be **copied out of its build tree and launched from somewhere else** before it
is allowed to be called a release. That is the exact check that catches the dyld failure above, it
takes seconds, and no other test substitutes for it: an artifact verified in place proves nothing
about an artifact a user unpacks in `~/Downloads`.

### 3. The five gates every engine artifact passes

Run in this order, each fatal:

1. **Relocation** — unpack in a scratch dir, launch, get a DOM back.
2. **Undetectability** — `navigator.webdriver === false`. This is the product; a build that loses
   the patch is not a slower product, it is a different one.
3. **The spool protocol** — `chrome-agent-selftest.cjs`, which already launches a throwaway
   instance with its own profile and spool and asserts the tabId registry, eval-await and the
   screenshot ack.
4. **`chrome-agent doctor`** against the unpacked artifact — the same capability probe a user's
   first run does, so a skew is found here rather than as a hang there.
5. **A real read** — `chrome-agent read news.ycombinator.com` returns items. End to end, through
   the whole stack, on the artifact as shipped.

A build that fails any gate is not published. A build that fails gate 3 or 4 is a *fork* bug and
blocks the release; one that fails gate 5 may be a site change, and the release note says which.

### 4. Naming, versioning, and a manifest that makes a build reproducible

```
chrome-agent-engine-<chromium-version>+fork.<n>-<os>-<arch>.tar.gz
e.g. chrome-agent-engine-152.0.7948.0+fork.1-linux-x64.tar.gz
```

The Chromium version comes from `chrome/VERSION` (today 152.0.7948.0); `fork.<n>` counts our patch
stack releases against that base. Beside each artifact, a `manifest.json`:

```jsonc
{
  "chromium_version": "152.0.7948.0",
  "upstream_base": "a9b5091aa0fa2f7aa769c1c6b2ceea8038e891dc",  // the shallow root of this fork
  "fork_patch_sha256": "…",        // sha of the 298 KB patch that IS the fork
  "args_gn": "…",                  // verbatim
  "built_on": "ubuntu-24.04 / x86_64 / 12 cores",
  "built_at": "2026-09-13T…Z",
  "gates": {"relocation": "pass", "webdriver": "pass", "spool_selftest": "pass", "doctor": "pass", "read": "pass"}
}
```

**The fork is a 298 KB patch over one upstream commit** (56 files, +5,833 lines). That, plus the
upstream revision and `args.gn`, is the entire recipe — which is why the manifest can make a build
reproducible in three fields instead of shipping a source tarball.

`sync-upstream.sh` already maintains exactly this model: our stack replays onto a moved base. A
release is therefore always "upstream rev X + our stack + these args".

### 5. Platform matrix, and what each one actually needs

| Platform | Status | The hard part |
|---|---|---|
| **linux-x64** | first build in progress (ADR 0008) | nothing signs it; ship a tarball + sha256 |
| **darwin-arm64** | needs a non-component build | **codesign + notarization**, or Gatekeeper quarantines it |
| **darwin-x64** | not planned | only if someone has an Intel Mac |
| **win-x64** | deferred, explicitly | Authenticode, and nobody has built this fork on Windows |
| **linux-arm64** | deferred | no consumer today |

**macOS distribution is signing, not building.** An unsigned `.app` downloaded from anywhere gets
quarantined and refuses to launch; `xattr -d` is a workaround for the person who built it, not a
distribution strategy. The `applecert` tooling already mints Developer ID certificates from an App
Store Connect key, so the pieces exist — but a Developer ID **application** cert plus notarization
is a separate step from the App Store certs, and it must be treated as part of the release, not an
afterthought. Nothing is signed today.

### 6. Where releases are published

GitHub Releases on the fork repo, one release per `<chromium-version>+fork.<n>`, carrying every
platform artifact plus its manifest and `SHA256SUMS`. Artifacts are hundreds of MB and well under
the 2 GB per-asset limit. The CLI publishes separately on its own cadence (ADR 0006) and declares
which engine versions it needs.

## Alternatives rejected

| Option | Why not |
|---|---|
| **Ship the component build** (`.app` + 699 MB of dylibs) | Proven broken when relocated. Even if we shipped the whole tree it would be 13 GB and depend on its own absolute layout. |
| **Ship `out/Default` wholesale** | 13 GB, includes every test binary and object artifact. |
| **Tell users to build it** | Hours, ~100 GB, depot_tools. That is not a product, and it is the reason `browser:` cannot be sold today. |
| **One universal artifact** | Chromium is per-OS-and-arch by construction. |
| **Skip notarization, tell users to right-click-open** | Works exactly once per user and teaches them to bypass Gatekeeper. |
| **Publish unverified builds and fix reports** | The failure mode here is silent and machine-specific; the gates cost seconds. |

## Consequences

- Two build configurations per platform now exist, and the release one is slow (a non-component
  link is much heavier than a component one). Expect release builds to be a scheduled thing, not a
  per-commit thing.
- Storage and bandwidth per release is a few hundred MB per platform.
- Signing introduces a secret (the Apple key) into the release path. It stays out of any agent's
  context; the key lives where `applecert` already keeps it.
- Every release is reproducible from three fields, so "which build is on that server?" has an
  answer.

## What would prove this

1. A `linux-x64` artifact that passes all five gates **on a machine that did not build it**.
2. A `darwin-arm64` artifact that launches from `~/Downloads` on a second Mac with no quarantine
   prompt — i.e. signed and notarized, not just built.
3. `chrome-agent doctor` on a clean box reporting `ready: true` against an unpacked artifact, with
   no repo checkout present.
4. A build reproduced from the manifest alone: same upstream rev, same patch sha, same args — and
   the gates pass again.
