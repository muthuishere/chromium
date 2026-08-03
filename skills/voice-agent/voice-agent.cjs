#!/usr/bin/env node
// voice-agent — the standalone voice brain for the Teams audio-call agent
// (Layer 1 of docs/adr/0002-teams-audio-call-agent.md). Zero npm deps: uses
// Node 18+ global fetch/FormData/Blob. Closes the voice loop with pure
// ElevenLabs calls, testable at a shell BEFORE any Teams / chrome-agent wiring.
//
//   node voice-agent.cjs speak  "<text>" out.wav        # TTS -> file
//   node voice-agent.cjs listen in.wav|in.mp3|in.m4a    # STT -> transcript
//   node voice-agent.cjs voices                         # list cloned voices
//
// Output format is chosen by the OUT extension:
//   .wav -> 16-bit PCM mono @24k (feeds the fork's PLAYWAV:<path> / /mic bridge)
//   .pcm -> raw int16 mono @24k
//   .mp3 -> mp3_44100_128 (for human listening)
//
// Voice: default is the "kathir_deemwar" clone. Override with --voice <name|id>
// or $VOICE_AGENT_VOICE_ID. Auth: $ELEVENLABS_API_KEY (read at runtime, never
// printed or written anywhere).
'use strict';
const fs = require('fs');
const path = require('path');

const API = 'https://api.elevenlabs.io/v1';
const DEFAULT_VOICE =
  process.env.VOICE_AGENT_VOICE_ID || 'FOkcWCGUw290OfLTlKei'; // kathir_deemwar
const TTS_MODEL = process.env.VOICE_AGENT_TTS_MODEL || 'eleven_multilingual_v2';
const STT_MODEL = process.env.VOICE_AGENT_STT_MODEL || 'scribe_v1';

function key() {
  const k = process.env.ELEVENLABS_API_KEY;
  if (!k) {
    console.error('voice-agent: $ELEVENLABS_API_KEY is not set in the environment.');
    process.exit(2);
  }
  return k;
}

// Parse `--flag value` pairs out of argv, returning {flags, rest}.
function parseFlags(argv) {
  const flags = {};
  const rest = [];
  for (let i = 0; i < argv.length; i++) {
    if (argv[i].startsWith('--')) {
      const name = argv[i].slice(2);
      flags[name] = argv[i + 1] && !argv[i + 1].startsWith('--') ? argv[++i] : true;
    } else {
      rest.push(argv[i]);
    }
  }
  return { flags, rest };
}

async function listVoices() {
  const r = await fetch(`${API}/voices`, { headers: { 'xi-api-key': key() } });
  if (!r.ok) throw new Error(`GET /voices ${r.status} ${await r.text()}`);
  const d = await r.json();
  return d.voices || [];
}

// Resolve a --voice value that may be an id or a (partial) name.
async function resolveVoice(v) {
  if (!v) return DEFAULT_VOICE;
  // A raw ElevenLabs id is 20 url-safe chars; treat anything else as a name.
  if (/^[A-Za-z0-9]{20}$/.test(v)) return v;
  const voices = await listVoices();
  const hit = voices.find((x) => x.name.toLowerCase().includes(v.toLowerCase()));
  if (!hit) throw new Error(`no voice matching "${v}" (try: voice-agent voices)`);
  return hit.voice_id;
}

// Minimal 16-bit PCM WAV wrapper (mono).
function wrapWav(pcm, rate, channels = 1, bits = 16) {
  const blockAlign = (channels * bits) >> 3;
  const byteRate = rate * blockAlign;
  const h = Buffer.alloc(44);
  h.write('RIFF', 0);
  h.writeUInt32LE(36 + pcm.length, 4);
  h.write('WAVE', 8);
  h.write('fmt ', 12);
  h.writeUInt32LE(16, 16);
  h.writeUInt16LE(1, 20); // PCM
  h.writeUInt16LE(channels, 22);
  h.writeUInt32LE(rate, 24);
  h.writeUInt32LE(byteRate, 28);
  h.writeUInt16LE(blockAlign, 32);
  h.writeUInt16LE(bits, 34);
  h.write('data', 36);
  h.writeUInt32LE(pcm.length, 40);
  return Buffer.concat([h, pcm]);
}

async function speak(text, out, flags) {
  if (!text || !out) {
    console.error('usage: voice-agent speak "<text>" <out.wav|.pcm|.mp3> [--voice <name|id>]');
    process.exit(2);
  }
  const voiceId = await resolveVoice(flags.voice);
  const ext = path.extname(out).toLowerCase();
  const pcmRate = 24000;
  const outputFormat =
    ext === '.mp3' ? 'mp3_44100_128' : `pcm_${pcmRate}`;
  const r = await fetch(
    `${API}/text-to-speech/${voiceId}?output_format=${outputFormat}`,
    {
      method: 'POST',
      headers: { 'xi-api-key': key(), 'content-type': 'application/json' },
      body: JSON.stringify({ text, model_id: TTS_MODEL }),
    },
  );
  if (!r.ok) throw new Error(`TTS ${r.status} ${await r.text()}`);
  const audio = Buffer.from(await r.arrayBuffer());
  if (ext === '.wav') {
    fs.writeFileSync(out, wrapWav(audio, pcmRate));
    console.log(`wrote ${out} (WAV 16-bit mono @${pcmRate}, ${audio.length} PCM bytes) voice=${voiceId}`);
    console.log(`  -> feed the fork: PLAYWAV:${path.resolve(out)}  (or resample to 48k for /mic)`);
  } else {
    fs.writeFileSync(out, audio);
    console.log(`wrote ${out} (${outputFormat}, ${audio.length} bytes) voice=${voiceId}`);
  }
}

async function listen(inPath, flags) {
  if (!inPath) {
    console.error('usage: voice-agent listen <in.wav|.mp3|.m4a> [--lang xx]');
    process.exit(2);
  }
  if (!fs.existsSync(inPath)) throw new Error(`no such file: ${inPath}`);
  const buf = fs.readFileSync(inPath);
  const fd = new FormData();
  fd.append('model_id', STT_MODEL);
  if (flags.lang) fd.append('language_code', String(flags.lang));
  fd.append('file', new Blob([buf]), path.basename(inPath));
  const r = await fetch(`${API}/speech-to-text`, {
    method: 'POST',
    headers: { 'xi-api-key': key() }, // fetch sets the multipart boundary
    body: fd,
  });
  if (!r.ok) throw new Error(`STT ${r.status} ${await r.text()}`);
  const d = await r.json();
  console.log(d.text != null ? d.text : JSON.stringify(d, null, 2));
}

async function voices() {
  const vs = await listVoices();
  for (const v of vs) {
    console.log(`${v.voice_id} | ${v.name} | ${v.category || ''}`);
  }
}

(async () => {
  const [cmd, ...argv] = process.argv.slice(2);
  const { flags, rest } = parseFlags(argv);
  try {
    switch (cmd) {
      case 'speak': await speak(rest[0], rest[1], flags); break;
      case 'listen': await listen(rest[0], flags); break;
      case 'voices': await voices(); break;
      default:
        console.error('voice-agent commands: speak | listen | voices  (see header)');
        process.exit(2);
    }
  } catch (e) {
    console.error('voice-agent error:', e && e.message ? e.message : e);
    process.exit(1);
  }
})();
