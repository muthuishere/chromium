# What is in this build

Full provenance for `chrome-agent-engine-152.0.7948.0+fork.1-linux-x64`. Nothing about the build is
hidden: the exact source patch, the build flags, the codec set, and the agent capabilities are all
below, and the patch itself is published beside the binary as `agent-fork.patch`.

## Provenance
- **Base:** upstream Chromium `152.0.7948.0`, commit `a9b5091aa0fa2f7aa769c1c6b2ceea8038e891dc`.
- **Fork:** a single patch, `agent-fork.patch` (56 files, +5,833 / -257 lines, sha256 `a68dc6d7…`).
  Download it and read exactly what was changed — the whole fork is that one file over the base.
- Reproduce: check out the base, `git apply agent-fork.patch`, build with the args below.

## Build flags (`args.gn`)
```
is_debug = false
is_component_build = false      # one relocatable binary, not 500 loose dylibs
dcheck_always_on = false
symbol_level = 0
blink_symbol_level = 0
is_official_build = false
enable_nacl = false
target_cpu = "x64"
```

## Codecs — read this
This build has **`USE_PROPRIETARY_CODECS` OFF** (verified in `media_buildflags.h`).

| plays | does NOT play |
|---|---|
| VP8, VP9, AV1, Opus, Vorbis, FLAC, WAV, Theora | H.264/AVC, AAC, MP3, AC3/EAC3, DTS |

Most YouTube (VP9/AV1) works; most MP4 files and H.264 WebRTC do not. A **codec-enabled build
(fork.2)** with `proprietary_codecs=true` + `ffmpeg_branding="Chrome"` is being produced separately;
distributing binaries that carry H.264/AAC decoders carries patent-licensing obligations, which is
why upstream Chromium ships them off by default.

## Agent capabilities in this binary
The fork adds a file-drop control protocol and per-tab media, all localhost-only:

- **control:** stable per-session tab ids, `goto`, `eval`, `evalasync` (incl. a CSP-safe base64 path
  for strict `script-src` pages), `screenshot`, `waitfor`, tab list/open/close, `netlog`.
- **media, per tab:** push PCM in as the microphone (`AUDIOSTART`), tap what a tab plays
  (`audio-out`), push frames in as the camera (`VIDEOSTART`).
- **undetectable:** `navigator.webdriver === false`; a real, persistent profile.

**Not in this binary:** the VERSION handshake, the instance registry, and cookie export/import are
newer engine work and ship in a later release. `chrome-agent doctor` reports what any given engine
actually supports.
