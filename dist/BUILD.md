# What is in this build

Full provenance for `chrome-agent-engine-152.0.7948.0+fork.2` — `linux-x64` and `mac-arm64`, same base and patch. Nothing is hidden: the exact
source patch, the build flags, the codec set, and the agent capabilities are all below, and the
patch is published beside the binary as `agent-fork.patch`.

## Provenance
- **Base:** upstream Chromium `152.0.7948.0`, commit `a9b5091aa0fa2f7aa769c1c6b2ceea8038e891dc`.
- **Fork:** one patch, `agent-fork.patch`, over that base. Read it to see exactly what changed.
- **Build config:** `build-config/agent-release.gn`, committed to the fork so it survives every
  upstream sync — the codec setting can no longer be silently lost (fork.1 was, and fork.2 fixes it).
- Reproduce: check out the base, `git apply agent-fork.patch`, `scripts/build-release.sh out/Release`.

## Codecs — plays everything
`USE_PROPRIETARY_CODECS = 1`, verified two ways: the generated `media_buildflags.h`, AND at runtime
on the shipped binary (`canPlayType` returns `probably` for H.264, AAC and MP3).

**Plays:** H.264/AVC, AAC, MP3, VP8, VP9, AV1, Opus, Vorbis, FLAC, WAV (+ platform HEVC).

Distributing decoders for H.264/AAC carries patent-licensing obligations — an owner-approved
distribution decision, recorded here, which is why upstream Chromium ships them off by default.

## Agent capabilities in this binary
Localhost-only, added by the fork:
- **control:** stable per-session tab ids, `goto`, `eval`, `evalasync` (incl. a CSP-safe base64 path),
  `screenshot`, `waitfor`, tab list/open/close, `netlog`.
- **media, per tab:** push PCM in as the mic (`AUDIOSTART`), tap tab audio out, push frames in as
  the camera (`VIDEOSTART`).
- **undetectable:** `navigator.webdriver === false`; a real, persistent profile.

Not in this binary: the VERSION handshake, instance registry and cookie export/import are newer
engine work shipping in a later release. `chrome-agent doctor` reports what any engine supports.

## macOS (darwin-arm64)
Same base, patch and codec flags, built on macOS 26.4 / arm64. Shipped as a self-contained
`Chromium.app`, signed inside-out with a Developer ID (hardened runtime; JIT entitlements on the
renderer/GPU helpers, mic + camera on the app for the media verbs), **notarized by Apple and
stapled** — `spctl` reports `accepted, source=Notarized Developer ID`, so it is not quarantined.
Verified on the unpacked tarball: codesign strict, Gatekeeper, spool protocol via the launcher,
`navigator.webdriver === false`, and `canPlayType` `probably` for H.264/AAC/MP3/VP9.
