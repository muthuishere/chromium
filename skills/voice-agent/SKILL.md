---
name: voice-agent
description: The standalone voice brain for the Teams audio-call agent — speak (ElevenLabs TTS in the kathir_deemwar clone) and listen (ElevenLabs Scribe STT) from the shell, no Teams/browser needed. Layer 1 of ADR 0002.
---

# voice-agent (sample skill)

Layer 1 of `docs/adr/0002-teams-audio-call-agent.md`: prove the voice loop with
pure API calls **before** wiring it into a live Teams call through the chromium
fork. Zero npm deps (Node 18+ global `fetch`/`FormData`/`Blob`).

## Auth & voice
- `$ELEVENLABS_API_KEY` — read at runtime, never printed or written. Load it in a
  subshell without echoing: `export ELEVENLABS_API_KEY="$(zsh -ic 'echo $ELEVENLABS_API_KEY' 2>/dev/null)"`.
- Default voice: **`kathir_deemwar`** (`FOkcWCGUw290OfLTlKei`). Override with
  `--voice <name|id>` or `$VOICE_AGENT_VOICE_ID`.

## Commands
```bash
# speak: TTS -> file. Format chosen by extension:
#   .wav -> 16-bit PCM mono @24k (feeds the fork's PLAYWAV / /mic bridge)
#   .pcm -> raw int16 mono @24k        .mp3 -> mp3_44100_128 (for listening)
node voice-agent.cjs speak "Hello, joining the meeting." out.wav
node voice-agent.cjs speak "quick note" out.mp3 --voice muthu

# listen: STT (Scribe) -> transcript text
node voice-agent.cjs listen recording.wav        # or .mp3 / .m4a
node voice-agent.cjs listen clip.wav --lang en

# list cloned voices (find an id/name)
node voice-agent.cjs voices
```

Verified round-trip: `speak … out.wav` → `listen out.wav` returns the same words.

## How it becomes the Teams agent (next layers)
- **The brain** is Claude (this session / a headless turn) — voice-agent is only
  the ears+mouth. Don't use ElevenLabs' bundled conversational LLM.
- **Speak into a call:** the `.wav` output feeds the fork's shipped mic bridge —
  `PLAYWAV:<abs path>` (one-shot) or stream PCM to `ws://127.0.0.1:<port>/mic`
  after `AUDIOSTART:<port>` (resample 24k→48k first; `/mic` expects 48k mono).
- **Hear the call:** `TAPSTART:<port>` then read int16 mono 48k from
  `ws://127.0.0.1:<port>/tap` (shipped 2026-08-03), buffer an utterance, `listen`
  it. Gate STT while the agent is speaking.
- **The meeting itself:** create + get the join link via `apl`/Graph online
  meetings; chrome-agent `goto <joinWebUrl>` joins it (camera off).

See ADR 0002 for the full loop and the half-duplex v1 plan.
