# ADR 0002 — Teams audio-call agent (speak + listen in a live meeting)

- **Status:** Proposed — design accepted, phased build not yet started
- **Date:** 2026-08-03
- **Owner:** Muthu (fork maintainer)
- **Author:** Claude Code (chromium-agent session)
- **Severity:** MEDIUM — new capability, not a regression. Unblocks "an agent that
  attends/hosts a Teams call in Muthu's voice."

---

## Context

We want an AI agent to **join a Microsoft Teams meeting (audio-only), speak in Muthu's
cloned ElevenLabs voice, and hear/understand the other participants** — a two-way voice
conversation, with the agent as a real participant on the call. The agent should also be
able to **create the meeting and share the join link** so people can join it.

The key realization: **the chromium-agent fork already owns the tab's media pipeline**, so
we do NOT need BlackHole, a virtual audio driver, or the Microsoft calling-bot media SDK.
We drive audio in/out of the Teams *web* tab directly, over a localhost WebSocket, and use
ElevenLabs (voice) + Claude (brain) + a speech-to-text engine (ears) around it.

### What already exists in the fork (verified)

| Capability | Mechanism | Status |
|---|---|---|
| **Audio OUT** — push PCM into the tab as the microphone | `AUDIOSTART:<port>` → `ws://127.0.0.1:<port>/mic`, int16 mono 48 kHz; fork switch `kUseFakeAudioInputOnly` fakes ONLY the mic (real cam untouched); audio service forced in-process | ✅ **shipped + verified** 2026-07-23 (`b547abe64c`) |
| Video OUT (`/cam`) | `VIDEOSTART` + I420 frames | ✅ shipped (`05cd988a0e`) — not needed for audio-only |
| Binary WS **encode** (server→client) | `EncodeBinaryFrame`, `SendBinaryOverWebSocket` | ✅ shipped (needed by `/tap`) |
| Deterministic per-tab targeting | UUID tab registry, `TAB:<id>\|…` | ✅ shipped (ADR 0001) |

### The one missing fork piece

| Capability | Mechanism | Status |
|---|---|---|
| **Audio IN** — tap the tab's rendered output → agent | `TAPSTART:<port>` → `ws://127.0.0.1:<port>/tap`; snoop `audio::OutputController` (audio is in-process) → mono int16 → binary WS | ⚠️ **designed, NOT built** — see the `/tap` plan in memory `chromium-agent-audio-websocket-bridge` |

Without `/tap` the agent can **speak** into a call but cannot **hear** it. `/tap` is the
critical path and is the only fork build this ADR requires.

---

## Decision

Build the Teams audio-call agent as **three separable layers**, proved in order. Each layer
is independently testable; only Layer 2 requires a fork rebuild.

### Layer 1 — the voice brain (standalone sample skill, NO fork changes)

A separate skill `voice-agent` with a small CLI that closes the voice loop with **pure API
calls**, so we prove latency and quality before touching Teams:

```
speak  "<text>"  → ElevenLabs TTS (voice_id = muthu english) → out.pcm / out.mp3
listen  in.wav   → ElevenLabs Scribe STT → transcript text
turn   "<heard>" → (Claude, the session itself) → reply text → speak
```

- **Voice out:** ElevenLabs TTS, model `eleven_multilingual_v2` (or `eleven_turbo_v2_5`
  when latency matters). Output **`pcm_24000` mono** so it drops straight into the `/mic`
  bridge (the bridge resamples via its `src_rate` arg) — plus `mp3` for human listening.
- **Voice in:** ElevenLabs **Scribe** (`/v1/speech-to-text`) — "direct line to ElevenLabs"
  both directions, ONE vendor, ONE key already in the env. (STT provider is swappable;
  Gemini/Deepgram are drop-in alternates behind the same `listen` subcommand.)
- **Brain:** Claude — the running session, or a headless turn. ElevenLabs' *bundled*
  conversational-agent LLM is deliberately **not** used; we keep reasoning in Claude and use
  ElevenLabs only as voice.
- **Key handling:** `$ELEVENLABS_API_KEY` is read from the environment at call time and
  never printed, logged, or written to a file. Voice id `KUuOXc3i6FSizlq3R9X9`
  ("muthu english") is not secret and may be hard-coded.

This layer is runnable and demoable on its own (talk to it at a shell, no meeting).

### Layer 2 — the ears (`/tap` fork bridge) — the one build

Build `/tap` per the plan in `chromium-agent-audio-websocket-bridge`:
- `TAPSTART:<port>` / `TAPSTOP` watcher commands (mirror `AUDIOSTART`/`AUDIOSTOP`).
- Snoop `audio::OutputController::OnMoreData` via a `Snoopable::Snooper` (audio is already
  in-process, so we skip the mojo loopback pipe). New singleton
  `media/audio/agent_audio_tap_bridge.{h,cc}` (mono ring, `PushRenderedAudio` /
  `ReadMonoFloat`); glue in `services/audio/agent_audio_tap.{h,cc}`.
- Drain the ring → `SendBinaryOverWebSocket` int16 to `/tap` (binary encode already shipped).
- Coarse v1 acceptable: tap the mixed output of ALL tabs at the mixer level (one call at a
  time, single meeting tab) instead of per-tab mojo plumbing.

Verify: play known audio in a tab, confirm the client receives int16 mono PCM matching it.

### Layer 3 — the Teams glue (skill + chrome-agent)

- **Create meeting + link:** `apl` / Graph `POST /me/onlineMeetings` → `joinWebUrl`; share
  the link over email/Teams/Telegram to invitees.
- **Join:** chrome-agent `goto <joinWebUrl>` in the fork (headful or headless), dismiss
  pre-join prompts, join with **camera off, mic = the agent bridge** (Teams sees the fake
  "Agent" mic device; `--use-fake-ui-for-media-stream` auto-grants, so no permission dialog).
- **Run the loop** (half-duplex v1):
  1. `TAPSTART` → stream `/tap` → VAD detects a participant finished speaking →
  2. buffer that utterance → ElevenLabs Scribe → text →
  3. Claude produces a reply →
  4. ElevenLabs TTS (muthu voice) → PCM → `/mic` → Teams hears it.
  5. **Gate STT while the agent is speaking** (don't transcribe our own turn).

---

## Consequences / trade-offs

- **No BlackHole, no virtual driver, no MS media SDK** — the fork is the media plane.
  Localhost-only WS, started by explicit command, torn down after.
- **Half-duplex first.** Turn-based (listen-until-silence → think → speak) is realistic and
  ships fast. Full-duplex barge-in (interrupting, talking over) needs echo handling and is a
  later phase. `/tap` taps *remote* rendered output, so the agent does not normally hear its
  own `/mic` audio — self-hearing is a Teams-mix edge case, not the default.
- **Latency budget** (target < ~2.5 s/turn): Scribe STT + Claude + TTS first-chunk. Use
  `eleven_turbo_v2_5` and streaming TTS to cut the speak latency; start playing the first PCM
  chunk before the full reply is synthesized.
- **Teams web UI is a moving target.** Join/mute/leave are DOM-driven, so they inherit the
  ADR-0001 tab-targeting + navigate-then-eval discipline (pin `TAB:<uuid>`, wait for commit).
  Prefer stable selectors / keyboard shortcuts; capture a recipe like the other chrome-agent
  recipes.
- **ElevenLabs cost** is per-character (TTS) + per-minute (Scribe) — fine for calls, watch it
  on long meetings.

## Open questions (to confirm before Layer 1 build)

1. **Voice:** default is the existing clone **"muthu english"** (`KUuOXc3i6FSizlq3R9X9`).
   The request said "kathir voice" — is that (a) a typo for muthu, (b) a *new* voice to clone
   for this persona, or (c) an existing ElevenLabs voice? Blocks nothing but the voice_id.
2. **STT provider:** default ElevenLabs Scribe (one vendor). OK, or prefer Gemini/Deepgram?
3. **Duplex:** confirm half-duplex v1 is acceptable for the first demo.

## References
- Memory `chromium-agent-audio-websocket-bridge` — full `/mic` (shipped) + `/tap` (planned) design, file:line.
- Memory `chromium-agent-audio-devices` — per-tab mic/speaker background.
- ADR 0001 — deterministic per-tab targeting (Teams DOM ops depend on it).
- `CHROMIUM_SENDKEYS_SPEC.md` §3a (audio bridge).
- apl-skill — Graph online-meetings (create) + Teams chat (share link).
